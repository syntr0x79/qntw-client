//go:build linux

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/syntr0x79/qntw-client/internal/mobileprobe/wire"
	"github.com/syntr0x79/qntw-client/mobile/linux/desktopvpn"
	"github.com/syntr0x79/qntw-client/mobile/linux/probe"
)

func tunnelSettings(job wire.Job) (netip.Prefix, netip.Addr, error) {
	address := netip.MustParsePrefix("172.19.0.1/32")
	dns := netip.MustParseAddr("1.1.1.1")
	if job.Engine == "xray" {
		return address, dns, nil
	}
	if job.Engine != "amneziawg" {
		return netip.Prefix{}, netip.Addr{}, errors.New("unsupported engine")
	}
	found := false
	section := ""
	for _, line := range strings.Split(job.AWGConf, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			section = strings.ToLower(line)
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || section != "[interface]" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "address":
			for _, item := range strings.Split(value, ",") {
				if prefix, err := netip.ParsePrefix(strings.TrimSpace(item)); err == nil && prefix.Addr().Is4() {
					address = netip.PrefixFrom(prefix.Addr(), 32)
					found = true
					break
				}
			}
		case "dns":
			for _, item := range strings.Split(value, ",") {
				if ip, err := netip.ParseAddr(strings.TrimSpace(item)); err == nil && ip.Is4() && !ip.IsLoopback() && !ip.IsUnspecified() && !ip.IsMulticast() {
					dns = ip
					break
				}
			}
		}
	}
	if !found {
		return netip.Prefix{}, netip.Addr{}, errors.New("missing tunnel address")
	}
	return address, dns, nil
}

func connect(req request, reader *bufio.Reader) {
	var job wire.Job
	if len(req.Job) == 0 || json.Unmarshal(req.Job, &job) != nil {
		fail(true, "Некорректный маршрут.")
	}
	address, dns, err := tunnelSettings(job)
	if err != nil {
		fail(true, "Этот маршрут не поддерживается.")
	}
	network, err := probe.VPNNetwork()
	if err != nil {
		fail(true, "Не удалось определить сеть.")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()
	session, err := desktopvpn.Start(ctx, desktopvpn.Config{Job: string(req.Job), Address: address, DNS: dns, Network: network, ControlURL: req.ControlURL})
	if err != nil {
		fail(true, "Не удалось подключиться. Проверьте разрешение администратора, сеть и настройки DNS.")
	}
	_ = json.NewEncoder(responseOut).Encode(map[string]string{"status": "connected"})
	stopped := make(chan struct{})
	go func() { _, _ = reader.ReadString('\n'); close(stopped) }()
	select {
	case <-stopped:
	case <-ctx.Done():
	case <-session.Done():
	}
	if err := session.Close(); err != nil {
		fail(true, "Не удалось полностью восстановить сетевые настройки. Новое подключение не запущено.")
	}
}
