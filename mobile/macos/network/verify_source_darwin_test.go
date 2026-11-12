//go:build darwin && !ios

package network

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

func TestSystemCheckBindsHTTPSAndDNSToTunnelAddress(t *testing.T) {
	for _, source := range []string{"10.66.0.27", "172.19.0.1", "198.18.94.1"} {
		t.Run(source, func(t *testing.T) {
			tcp, dns, err := systemCheckDialers(source)
			if err != nil {
				t.Fatal(err)
			}
			tcpSource, ok := tcp.LocalAddr.(*net.TCPAddr)
			if !ok || !tcpSource.IP.Equal(net.ParseIP(source)) || tcpSource.Port != 0 {
				t.Fatalf("HTTPS source=%v, want %s with ephemeral port", tcp.LocalAddr, source)
			}
			dnsSource, ok := dns.LocalAddr.(*net.UDPAddr)
			if !ok || !dnsSource.IP.Equal(net.ParseIP(source)) || dnsSource.Port != 0 {
				t.Fatalf("DNS source=%v, want %s with ephemeral port", dns.LocalAddr, source)
			}
			if tcp.Timeout != 4*time.Second || dns.Timeout != 2*time.Second {
				t.Fatal("system checks lost their bounded dial timeouts")
			}
			if tcp.Control != nil || tcp.ControlContext != nil || dns.Control != nil || dns.ControlContext != nil {
				t.Fatal("system checks must use the ordinary system path, without a physical socket bypass")
			}
		})
	}
}

func TestVerifySystemFromRejectsInvalidSourceBeforeCommands(t *testing.T) {
	for _, source := range []string{"", "vpn.example", "10.66.0.27/32", "2001:db8::1", "::ffff:10.66.0.27", "0.0.0.0", "127.0.0.1", "224.0.0.1", "255.255.255.255"} {
		t.Run(source, func(t *testing.T) {
			s, calls := fakeSession(t, "")
			if err := s.VerifySystemFrom(context.Background(), source); err == nil {
				t.Fatal("invalid source accepted")
			}
			if len(*calls) != 0 {
				t.Fatalf("invalid source triggered system commands: %v", *calls)
			}
		})
	}
}

func TestSystemCheckMessagesPreserveTincAndNameOrdinaryTunnel(t *testing.T) {
	tests := []struct {
		name   string
		verify func(*Session, context.Context) error
		want   string
	}{
		{
			name:   "Tinc",
			verify: (*Session).VerifySystem,
			want:   "Tinc отвечает, но системный туннель не передаёт HTTPS. Подключение остановлено.",
		},
		{
			name: "ordinary VPN",
			verify: func(s *Session, ctx context.Context) error {
				return s.VerifySystemFrom(ctx, "10.66.0.27")
			},
			want: "Системный туннель не передаёт HTTPS. Подключение остановлено.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s := &Session{System: &Device{Name: "utun99"}, controlIPs: []net.IP{net.ParseIP("192.0.2.1")}}
			s.run = func(ctx context.Context, _ string, name string, args ...string) ([]byte, error) {
				if name == "/sbin/route" && strings.Join(args, " ") == "-n get 1.1.1.1" {
					deadline, ok := ctx.Deadline()
					if !ok || time.Until(deadline) > 18*time.Second {
						t.Fatal("system verification lost its overall deadline")
					}
					// Cancel before HTTP so this regression uses no sockets or
					// system commands; all diagnostics also use this fake runner.
					cancel()
					return []byte("interface: utun99\n"), nil
				}
				return nil, nil
			}
			var got ConnectionError
			if err := tt.verify(s, ctx); !errors.As(err, &got) || got.Code != "system_tcp_failed" || got.Message != tt.want {
				t.Fatalf("unexpected verification failure: %#v (%v)", got, err)
			}
		})
	}
}
