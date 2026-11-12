package xrayfetch

import (
	"errors"
	"fmt"
	"github.com/xtls/xray-core/app/proxyman"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/grpc"
	"github.com/xtls/xray-core/transport/internet/httpupgrade"
	"github.com/xtls/xray-core/transport/internet/reality"
	"github.com/xtls/xray-core/transport/internet/splithttp"
	xtls "github.com/xtls/xray-core/transport/internet/tls"
	"github.com/xtls/xray-core/transport/internet/websocket"
	"google.golang.org/protobuf/proto"
	"net/netip"
)

// ErrUnbindable means the outbound cannot be pinned without changing its meaning.
var ErrUnbindable = errors.New("outbound cannot be bound to the selected network")

// BindOutbound pins the outer server IP while preserving TLS SNI and transport
// host names. An empty iface leaves socket binding to the platform controller.
func BindOutbound(out *core.OutboundHandlerConfig, iface string, addr netip.Addr) (*core.OutboundHandlerConfig, error) {
	if !addr.IsValid() {
		return nil, fmt.Errorf("%w: пустой адрес сервера", ErrUnbindable)
	}
	c := proto.Clone(out).(*core.OutboundHandlerConfig)

	msg, err := c.GetProxySettings().GetInstance()
	if err != nil {
		return nil, fmt.Errorf("%w: настройки протокола: %w", ErrUnbindable, err)
	}
	ep, err := ServerEndpoint(msg)
	if err != nil {
		return nil, err
	}
	orig := ep.GetAddress().AsAddress()
	if orig == nil {
		return nil, fmt.Errorf("%w: у исходящего нет адреса сервера", ErrUnbindable)
	}
	// Имя для SNI и Host — только если сервер был задан именем: у входа по
	// IP xray и так подставлял IP.
	name := ""
	if orig.Family().IsDomain() {
		name = orig.Domain()
	}
	ip := xnet.IPAddress(addr.Unmap().AsSlice())
	ep.Address = xnet.NewIPOrDomain(ip)
	c.ProxySettings = serial.ToTypedMessage(msg)

	sender := &proxyman.SenderConfig{}
	if c.SenderSettings != nil {
		inst, err := c.SenderSettings.GetInstance()
		if err != nil {
			return nil, fmt.Errorf("%w: настройки отправителя: %w", ErrUnbindable, err)
		}
		s, ok := inst.(*proxyman.SenderConfig)
		if !ok {
			return nil, fmt.Errorf("%w: настройки отправителя %T", ErrUnbindable, inst)
		}
		sender = s
	}
	if sender.StreamSettings == nil {
		sender.StreamSettings = &internet.StreamConfig{}
	}
	if err := bindStream(sender.StreamSettings, iface, name, orig, ip); err != nil {
		return nil, err
	}
	c.SenderSettings = serial.ToTypedMessage(sender)
	return c, nil
}

// bindStream привязывает поток к iface и закрепляет имя сервера name (см.
// BindOutbound). Отдельный поток скачивания xhttp (downloadSettings)
// привязывается так же: его адрес — тот же вход (тогда заменяется на ip) или
// IP; другое имя разрешилось бы мимо DNS модема, и такое исходящее
// отвергается.
func bindStream(st *internet.StreamConfig, iface, name string, orig, ip xnet.Address) error {
	if st.SocketSettings == nil {
		st.SocketSettings = &internet.SocketConfig{}
	}
	if st.SocketSettings.DialerProxy != "" {
		return fmt.Errorf("%w: исходящее идёт через другое (dialerProxy %q)", ErrUnbindable, st.SocketSettings.DialerProxy)
	}
	st.SocketSettings.Interface = iface

	secured := false
	for i, tm := range st.SecuritySettings {
		m, err := tm.GetInstance()
		if err != nil {
			return fmt.Errorf("%w: настройки шифрования: %w", ErrUnbindable, err)
		}
		switch s := m.(type) {
		case *xtls.Config:
			secured = true
			if s.ServerName == "" {
				s.ServerName = name
			}
		case *reality.Config:
			secured = true
			if s.ServerName == "" {
				s.ServerName = name
			}
		default:
			continue
		}
		st.SecuritySettings[i] = serial.ToTypedMessage(m)
	}

	for _, tc := range st.TransportSettings {
		if tc.GetSettings() == nil {
			continue
		}
		m, err := tc.Settings.GetInstance()
		if err != nil {
			return fmt.Errorf("%w: настройки транспорта: %w", ErrUnbindable, err)
		}
		switch s := m.(type) {
		case *websocket.Config:
			if s.Host == "" && !secured {
				s.Host = name
			}
		case *httpupgrade.Config:
			if s.Host == "" && !secured {
				s.Host = name
			}
		case *grpc.Config:
			if s.Authority == "" && !secured {
				s.Authority = name
			}
		case *splithttp.Config:
			if s.Host == "" && !secured {
				s.Host = name
			}
			if d := s.DownloadSettings; d != nil {
				if a := d.GetAddress().AsAddress(); a != nil {
					switch {
					case a.Family().IsDomain() && a.Domain() == name:
						d.Address = xnet.NewIPOrDomain(ip)
					case a.Family().IsDomain():
						return fmt.Errorf("%w: downloadSettings xhttp на другом имени %q", ErrUnbindable, a.Domain())
					}
				}
				if err := bindStream(d, iface, name, orig, ip); err != nil {
					return err
				}
			}
		default:
			continue
		}
		tc.Settings = serial.ToTypedMessage(m)
	}
	return nil
}
