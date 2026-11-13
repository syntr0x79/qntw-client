// Package vpn implements the ordinary desktop VPN using an owned Windows TUN.
package vpn

import (
	"encoding/json"
	"errors"
	"net/netip"
	"net/url"
	"strings"

	"github.com/syntr0x79/qntw-client/internal/awgprobe"
	"github.com/syntr0x79/qntw-client/internal/mobileprobe/wire"
	"github.com/syntr0x79/qntw-client/internal/xrayfetch"
	"github.com/xtls/xray-core/app/proxyman"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	hyproxy "github.com/xtls/xray-core/proxy/hysteria"
	masqueproxy "github.com/xtls/xray-core/proxy/masque"
	"github.com/xtls/xray-core/proxy/shadowsocks"
	"github.com/xtls/xray-core/proxy/trojan"
	"github.com/xtls/xray-core/proxy/vless"
	vlessout "github.com/xtls/xray-core/proxy/vless/outbound"
	"github.com/xtls/xray-core/proxy/vmess"
	vmessout "github.com/xtls/xray-core/proxy/vmess/outbound"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/finalmask/fragment"
	"github.com/xtls/xray-core/transport/internet/grpc"
	headerhttp "github.com/xtls/xray-core/transport/internet/headers/http"
	headernoop "github.com/xtls/xray-core/transport/internet/headers/noop"
	"github.com/xtls/xray-core/transport/internet/httpupgrade"
	"github.com/xtls/xray-core/transport/internet/hysteria"
	"github.com/xtls/xray-core/transport/internet/kcp"
	masque "github.com/xtls/xray-core/transport/internet/masque"
	"github.com/xtls/xray-core/transport/internet/reality"
	"github.com/xtls/xray-core/transport/internet/splithttp"
	"github.com/xtls/xray-core/transport/internet/tcp"
	xtls "github.com/xtls/xray-core/transport/internet/tls"
	"github.com/xtls/xray-core/transport/internet/websocket"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

var errProfile = errors.New("invalid or unsafe Windows VPN profile")

// ValidateJob runs before elevation and again in the elevated owner. It accepts
// protocol credentials, never a complete core JSON document or arbitrary typed
// plugin configuration. Rejected options cannot read/write administrator files.
func ValidateJob(raw json.RawMessage) error { _, _, err := parseJob(raw); return err }

func ValidateControlURL(value string) error {
	u, err := url.Parse(value)
	if err != nil || len(value) > 4096 || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || !safeHost(u.Hostname()) {
		return errors.New("valid HTTPS control URL required")
	}
	return nil
}

func parseJob(raw json.RawMessage) (wire.Job, *core.OutboundHandlerConfig, error) {
	var job wire.Job
	if len(raw) == 0 || len(raw) > 256<<10 || json.Unmarshal(raw, &job) != nil || job.ID == "" || len(job.ID) > 256 {
		return job, nil, errProfile
	}
	switch job.Engine {
	case "amneziawg":
		if len(job.Outbound) != 0 || len(job.AWGConf) > 128<<10 || awgprobe.ValidateConfig(job.AWGConf) != nil {
			return job, nil, errProfile
		}
		// The shared strict parser already rejects unknown sections/keys, including
		// wg-quick script hooks. Keep this explicit at the privilege boundary too.
		for _, line := range strings.Split(job.AWGConf, "\n") {
			key, _, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			switch strings.ToLower(strings.TrimSpace(key)) {
			case "preup", "postup", "predown", "postdown", "saveconfig", "table":
				return job, nil, errProfile
			}
		}
		return job, nil, nil
	case "xray":
		if job.AWGConf != "" || len(job.Outbound) == 0 {
			return job, nil, errProfile
		}
		out := new(core.OutboundHandlerConfig)
		if proto.Unmarshal(job.Outbound, out) != nil || out.ProxySettings == nil {
			return job, nil, errProfile
		}
		budget := 4096
		if validateMessage(out.ProtoReflect(), 0, &budget) != nil {
			return job, nil, errProfile
		}
		message, err := out.ProxySettings.GetInstance()
		if err != nil {
			return job, nil, errProfile
		}
		endpoint, err := xrayfetch.ServerEndpoint(message)
		if err != nil || endpoint == nil || endpoint.Address == nil || endpoint.Port == 0 || endpoint.Port > 65535 || !safeAddress(endpoint.Address) {
			return job, nil, errProfile
		}
		if out.SenderSettings != nil {
			message, err = out.SenderSettings.GetInstance()
			if err != nil {
				return job, nil, errProfile
			}
			if _, ok := message.(*proxyman.SenderConfig); !ok {
				return job, nil, errProfile
			}
		}
		return job, out, nil
	default:
		return job, nil, errProfile
	}
}

func safeHost(host string) bool {
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip.Is4() && !ip.IsUnspecified() && !ip.IsMulticast()
	}
	if len(host) == 0 || len(host) > 253 || strings.ContainsAny(host, " /\\:\r\n\t\x00") {
		return false
	}
	for _, part := range strings.Split(host, ".") {
		if part == "" || len(part) > 63 || part[0] == '-' || part[len(part)-1] == '-' {
			return false
		}
		for _, r := range part {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}
func safeAddress(address *xnet.IPOrDomain) bool {
	if address == nil {
		return false
	}
	switch value := address.Address.(type) {
	case *xnet.IPOrDomain_Ip:
		ip, ok := netip.AddrFromSlice(value.Ip)
		return ok && ip.Is4() && !ip.IsUnspecified() && !ip.IsMulticast()
	case *xnet.IPOrDomain_Domain:
		return safeHost(value.Domain)
	default:
		return false
	}
}

var allowedTyped = func() map[string]bool {
	result := map[string]bool{}
	for _, value := range []proto.Message{
		&proxyman.SenderConfig{}, &vlessout.Config{}, &vmessout.Config{}, &trojan.ClientConfig{}, &shadowsocks.ClientConfig{}, &hyproxy.ClientConfig{}, &masqueproxy.ClientConfig{},
		&vless.Account{}, &vmess.Account{}, &trojan.Account{}, &shadowsocks.Account{}, &masqueproxy.Account{},
		&xtls.Config{}, &reality.Config{}, &headerhttp.Config{}, &headernoop.Config{}, &tcp.Config{}, &websocket.Config{}, &httpupgrade.Config{}, &grpc.Config{}, &splithttp.Config{}, &kcp.Config{}, &hysteria.Config{}, &masque.Config{},
		&fragment.Config{},
	} {
		result[string(value.ProtoReflect().Descriptor().FullName())] = true
	}
	return result
}()

func validateMessage(message protoreflect.Message, depth int, budget *int) error {
	*budget--
	if depth > 24 || *budget < 0 || len(message.GetUnknown()) != 0 {
		return errProfile
	}
	if typed, ok := message.Interface().(*serial.TypedMessage); ok {
		if !allowedTyped[typed.Type] || len(typed.Value) > 256<<10 {
			return errProfile
		}
		decoded, err := typed.GetInstance()
		if err != nil {
			return errProfile
		}
		return validateMessage(decoded.ProtoReflect(), depth+1, budget)
	}
	switch value := message.Interface().(type) {
	case *xnet.IPOrDomain:
		if !safeAddress(value) {
			return errProfile
		}
	case *internet.StreamConfig:
		if len(value.SecuritySettings) > 1 || (value.SecurityType == "" && len(value.SecuritySettings) > 0) {
			return errProfile
		}
		for _, setting := range value.SecuritySettings {
			if setting == nil || setting.Type != value.SecurityType {
				return errProfile
			}
		}
		if !safeTransport(value.ProtocolName) {
			return errProfile
		}
		if value.SecurityType != "" && value.SecurityType != "xray.transport.internet.tls.Config" && value.SecurityType != "xray.transport.internet.reality.Config" {
			return errProfile
		}
	case *internet.TransportConfig:
		if !safeTransport(value.ProtocolName) {
			return errProfile
		}
		if value.Settings != nil {
			expected := map[string]string{"": "tcp", "raw": "tcp", "tcp": "tcp", "websocket": "websocket", "httpupgrade": "httpupgrade", "grpc": "grpc.encoding", "splithttp": "splithttp", "kcp": "kcp", "hysteria": "hysteria", "masque": "masque"}[value.ProtocolName]
			if value.Settings.Type != "xray.transport.internet."+expected+".Config" {
				return errProfile
			}
		}
	case *internet.SocketConfig:
		if value.Mark != 0 || value.Tproxy != 0 || value.DialerProxy != "" || value.Interface != "" || len(value.CustomSockopt) != 0 || value.Penetrate || value.AcceptProxyProtocol || value.ReceiveOriginalDestAddress {
			return errProfile
		}
	case *xtls.Config:
		// Remote ECH discovery uses independent resolvers; only inline ECH is safe
		// inside this helper's pinned physical-network lifetime.
		if len(value.EchServerKeys) != 0 || strings.Contains(value.EchConfigList, "://") {
			return errProfile
		}
	case *reality.Config:
		if value.Dest != "" || value.Type != "" || len(value.PrivateKey) != 0 || len(value.ServerNames) != 0 || value.Xver != 0 {
			return errProfile
		}
	case *hysteria.Config:
		if value.MasqType != "" || value.MasqFile != "" || value.MasqUrl != "" || value.MasqString != "" {
			return errProfile
		}
	case *masque.Config:
		// Header values go to the MASQUE server only; CR/LF would split the
		// request. WARP mode needs Cloudflare-side state the helper never
		// manages, so only the provider's own CONNECT-IP endpoint is accepted.
		if value.Warp != nil {
			return errProfile
		}
		for k, v := range value.Headers {
			if strings.ContainsAny(k+v, "\r\n\x00") {
				return errProfile
			}
		}
	}
	var result error
	message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		inspect := func(v protoreflect.Value, descriptor protoreflect.FieldDescriptor) bool {
			if descriptor.Kind() == protoreflect.MessageKind {
				result = validateMessage(v.Message(), depth+1, budget)
				return result == nil
			}
			if descriptor.Kind() == protoreflect.StringKind && v.String() != "" {
				name := strings.ToLower(string(field.Name()))
				if strings.Contains(name, "file") || strings.Contains(name, "command") || strings.Contains(name, "exec") || strings.Contains(name, "environment") || strings.Contains(name, "key_log") {
					result = errProfile
					return false
				}
				if strings.Contains(name, "path") {
					owner := string(message.Descriptor().FullName())
					if name != "path" || (owner != "xray.transport.internet.websocket.Config" && owner != "xray.transport.internet.httpupgrade.Config" && owner != "xray.transport.internet.splithttp.Config" && owner != "xray.transport.internet.masque.Config") {
						result = errProfile
						return false
					}
					if !strings.HasPrefix(v.String(), "/") || strings.ContainsAny(v.String(), "\r\n\x00") {
						result = errProfile
						return false
					}
				}
			}
			return true
		}
		if field.IsMap() {
			value.Map().Range(func(_ protoreflect.MapKey, v protoreflect.Value) bool { return inspect(v, field.MapValue()) })
			return result == nil
		}
		if field.IsList() {
			list := value.List()
			for i := 0; i < list.Len(); i++ {
				if !inspect(list.Get(i), field) {
					return false
				}
			}
			return true
		}
		return inspect(value, field)
	})
	return result
}

func safeTransport(value string) bool {
	switch value {
	case "", "tcp", "raw", "websocket", "httpupgrade", "grpc", "splithttp", "kcp", "hysteria", "masque":
		return true
	default:
		return false
	}
}
