//go:build darwin && !ios

package main

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/syntr0x79/qntw-client/internal/awgprobe"
	"github.com/syntr0x79/qntw-client/internal/mobileprobe/wire"
	"github.com/syntr0x79/qntw-client/internal/xrayfetch"
	"github.com/syntr0x79/qntw-client/mobile/engine"
	macnet "github.com/syntr0x79/qntw-client/mobile/macos/network"
	"github.com/xtls/xray-core/app/proxyman"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/splithttp"
	"google.golang.org/protobuf/proto"
)

var errOrdinaryRoute = errors.New("ordinary VPN transport could not be prepared")

// prepareOrdinaryRoute protects only the actual transport destinations. The
// returned profile uses the same literal addresses, so starting the engine
// cannot resolve a different server after the system routes have changed.
// Raw profile/parser errors may contain credentials and never leave this helper.
func prepareOrdinaryRoute(ctx context.Context, job wire.Job, network engine.Network, allow func(context.Context, []string) error) (wire.Job, error) {
	if network == nil || allow == nil || ctx.Err() != nil {
		return wire.Job{}, errOrdinaryRoute
	}
	var addresses []string
	switch job.Engine {
	case "amneziawg":
		if len(job.AWGConf) > 256<<10 || awgprobe.ValidateConfig(job.AWGConf) != nil {
			return wire.Job{}, errOrdinaryRoute
		}
		lines := strings.Split(job.AWGConf, "\n")
		section, endpointLine, endpoint := "", -1, ""
		for i, line := range lines {
			body, _, _ := strings.Cut(line, "#")
			body = strings.TrimSpace(body)
			if strings.HasPrefix(body, "[") {
				section = strings.ToLower(body)
				continue
			}
			key, value, ok := strings.Cut(body, "=")
			if ok && section == "[peer]" && strings.EqualFold(strings.TrimSpace(key), "endpoint") {
				if endpointLine >= 0 {
					return wire.Job{}, errOrdinaryRoute
				}
				endpointLine, endpoint = i, strings.TrimSpace(value)
			}
		}
		host, port, err := net.SplitHostPort(endpoint)
		portNumber, portErr := strconv.ParseUint(port, 10, 16)
		if endpointLine < 0 || err != nil || portErr != nil || portNumber == 0 {
			return wire.Job{}, errOrdinaryRoute
		}
		address, err := resolveOrdinaryIPv4(ctx, network, host)
		if err != nil {
			return wire.Job{}, err
		}
		oldLine := lines[endpointLine]
		body, comment, hasComment := strings.Cut(oldLine, "#")
		key, _, _ := strings.Cut(body, "=")
		lines[endpointLine] = key + "= " + net.JoinHostPort(address.String(), port)
		if hasComment {
			lines[endpointLine] += " #" + comment
		} else if strings.HasSuffix(oldLine, "\r") {
			lines[endpointLine] += "\r"
		}
		job.AWGConf = strings.Join(lines, "\n")
		addresses = []string{address.String()}
	case "xray":
		var out core.OutboundHandlerConfig
		if len(job.Outbound) > 256<<10 || proto.Unmarshal(job.Outbound, &out) != nil || out.ProxySettings == nil {
			return wire.Job{}, errOrdinaryRoute
		}
		settings, err := out.ProxySettings.GetInstance()
		if err != nil {
			return wire.Job{}, errOrdinaryRoute
		}
		endpoint, err := xrayfetch.ServerEndpoint(settings)
		if err != nil || endpoint == nil || endpoint.Address == nil || endpoint.Address.GetAddress() == nil || endpoint.Port == 0 || endpoint.Port > 65535 {
			return wire.Job{}, errOrdinaryRoute
		}
		host := endpoint.Address.AsAddress()
		if host == nil || host.Family().IsIPv6() {
			return wire.Job{}, errOrdinaryRoute
		}
		address, err := resolveOrdinaryIPv4(ctx, network, host.String())
		if err != nil {
			return wire.Job{}, err
		}
		addresses = []string{address.String()}
		if out.SenderSettings != nil {
			settings, err := out.SenderSettings.GetInstance()
			if err != nil {
				return wire.Job{}, errOrdinaryRoute
			}
			sender, ok := settings.(*proxyman.SenderConfig)
			if !ok {
				return wire.Job{}, errOrdinaryRoute
			}
			if err = ordinaryDownloadAddresses(sender.StreamSettings, host.String(), address, &addresses, 0); err != nil {
				return wire.Job{}, err
			}
		}
		pinned, err := xrayfetch.BindOutbound(&out, "", address)
		if err != nil {
			return wire.Job{}, errOrdinaryRoute
		}
		job.Outbound, err = proto.Marshal(pinned)
		if err != nil {
			return wire.Job{}, errOrdinaryRoute
		}
	default:
		return wire.Job{}, errOrdinaryRoute
	}
	if ctx.Err() != nil {
		return wire.Job{}, errOrdinaryRoute
	}
	if err := allow(ctx, addresses); err != nil {
		return wire.Job{}, macnet.ConnectionError{Code: "physical_route_failed", Message: "Не удалось закрепить VPN-сервер за обычной сетью. Подключение остановлено."}
	}
	return job, nil
}

func resolveOrdinaryIPv4(ctx context.Context, network engine.Network, host string) (netip.Addr, error) {
	if host == "" {
		return netip.Addr{}, errOrdinaryRoute
	}
	address, err := netip.ParseAddr(host)
	if err != nil {
		if strings.ContainsAny(host, "[]:/\\ \t\r\n") {
			return netip.Addr{}, errOrdinaryRoute
		}
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		type answer struct {
			address string
			err     error
		}
		done := make(chan answer, 1)
		go func() {
			address, err := network.Resolve(host)
			done <- answer{address, err}
		}()
		select {
		case <-ctx.Done():
			return netip.Addr{}, errOrdinaryRoute
		case resolved := <-done:
			if resolved.err != nil {
				return netip.Addr{}, errOrdinaryRoute
			}
			address, err = netip.ParseAddr(strings.TrimSpace(resolved.address))
		}
	}
	address = address.Unmap()
	if err != nil || !address.Is4() || address.IsUnspecified() || address.IsMulticast() {
		return netip.Addr{}, errOrdinaryRoute
	}
	return address, nil
}

// BindOutbound permits an XHTTP download on the same hostname or a separate
// literal IP. Inspect every nested stream before changing any system routes.
func ordinaryDownloadAddresses(stream *internet.StreamConfig, host string, primary netip.Addr, addresses *[]string, depth int) error {
	if stream == nil {
		return nil
	}
	if depth > 8 {
		return errOrdinaryRoute
	}
	for _, transport := range stream.TransportSettings {
		if transport.GetProtocolName() != stream.GetEffectiveProtocol() {
			continue
		}
		if transport.GetSettings() == nil {
			return nil
		}
		settings, err := transport.Settings.GetInstance()
		if err != nil {
			return errOrdinaryRoute
		}
		xhttp, ok := settings.(*splithttp.Config)
		if !ok || xhttp.DownloadSettings == nil {
			return nil
		}
		if xhttp.Mode == "stream-one" {
			return errOrdinaryRoute
		}
		download := xhttp.DownloadSettings
		if download.Address == nil || download.Address.GetAddress() == nil || download.Port == 0 || download.Port > 65535 {
			return errOrdinaryRoute
		}
		endpoint := download.Address.AsAddress()
		if endpoint == nil {
			return errOrdinaryRoute
		}
		address := primary
		if endpoint.Family().IsDomain() {
			if endpoint.Domain() != host {
				return errOrdinaryRoute
			}
		} else {
			address, err = netip.ParseAddr(endpoint.String())
			address = address.Unmap()
			if err != nil || !address.Is4() || address.IsUnspecified() || address.IsMulticast() {
				return errOrdinaryRoute
			}
		}
		found := false
		for _, existing := range *addresses {
			found = found || existing == address.String()
		}
		if !found {
			*addresses = append(*addresses, address.String())
		}
		if err := ordinaryDownloadAddresses(download, host, primary, addresses, depth+1); err != nil {
			return err
		}
		return nil
	}
	return nil
}
