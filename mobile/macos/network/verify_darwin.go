//go:build darwin && !ios

package network

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// ConnectionError reports the layer which failed without exposing credentials.
type ConnectionError struct {
	Code, Message string
	Diagnostics   map[string]string
}

func (e ConnectionError) Error() string { return e.Message }

// Once routes are active, loss of the real system path is a connection error,
// rather than an indefinite wait for another SOCKS readiness response.
func (s *Session) VerifyIfActive(ctx context.Context) (bool, error) {
	if !s.active {
		return false, nil
	}
	return true, s.VerifySystem(ctx)
}

// VerifySystem uses ordinary, unprotected sockets with the system TUN's local
// address. A successful SOCKS probe alone cannot certify the OS routing path.
func (s *Session) VerifySystem(ctx context.Context) error {
	return s.verifySystemFrom(ctx, "198.18.94.1", "Tinc отвечает, но системный туннель не передаёт HTTPS. Подключение остановлено.")
}

// VerifySystemFrom checks an ordinary VPN from the IPv4 address configured on
// its system TUN. Both HTTPS and DNS must traverse that same interface address.
func (s *Session) VerifySystemFrom(ctx context.Context, sourceAddress string) error {
	return s.verifySystemFrom(ctx, sourceAddress, "Системный туннель не передаёт HTTPS. Подключение остановлено.")
}

func systemCheckDialers(sourceAddress string) (tcp, dns *net.Dialer, err error) {
	address, err := netip.ParseAddr(sourceAddress)
	if err != nil || !address.Is4() || address.IsUnspecified() || address.IsMulticast() || address.IsLoopback() || address == netip.MustParseAddr("255.255.255.255") {
		return nil, nil, errors.New("invalid system tunnel IPv4 address")
	}
	local := net.IP(address.AsSlice())
	return &net.Dialer{Timeout: 4 * time.Second, LocalAddr: &net.TCPAddr{IP: local}},
		&net.Dialer{Timeout: 2 * time.Second, LocalAddr: &net.UDPAddr{IP: local}}, nil
}

func (s *Session) verifySystemFrom(ctx context.Context, sourceAddress, tcpFailureMessage string) error {
	tcpDialer, dnsDialer, err := systemCheckDialers(sourceAddress)
	if err != nil {
		return err
	}
	if s.System == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 18*time.Second)
	defer cancel()
	route, e := s.command(ctx, "", "/sbin/route", "-n", "get", "1.1.1.1")
	routed := false
	for _, line := range strings.Split(string(route), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "interface:" && fields[1] == s.System.Name {
			routed = true
		}
	}
	if e != nil || !routed {
		return ConnectionError{Code: "system_route_failed", Message: "macOS не направила интернет в интерфейс Quantway. Подключение остановлено."}
	}
	if len(s.controlIPs) == 0 {
		return errors.New("control addresses were not pinned")
	}
	var nextAddress atomic.Uint32
	transport := &http.Transport{DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(address)
		if e != nil || host != "qntw.link" {
			return nil, errors.New("unexpected system check address")
		}
		start := int(nextAddress.Add(1)-1) % len(s.controlIPs)
		var last error
		for offset := range s.controlIPs {
			address := s.controlIPs[(start+offset)%len(s.controlIPs)]
			conn, err := tcpDialer.DialContext(ctx, "tcp4", net.JoinHostPort(address.String(), port))
			if err == nil {
				return conn, nil
			}
			last = err
			if ctx.Err() != nil {
				break
			}
		}
		return nil, last
	}, TLSHandshakeTimeout: 4 * time.Second}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 7 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var response *http.Response
	for attempt := 0; attempt < 2; attempt++ {
		request, _ := http.NewRequestWithContext(ctx, "GET", "https://qntw.link/healthz", nil)
		response, e = client.Do(request)
		if e == nil || ctx.Err() != nil {
			break
		}
		transport.CloseIdleConnections()
	}

	if e != nil {
		detail := s.diagnostics()
		detail["cause"] = e.Error()
		return ConnectionError{Code: "system_tcp_failed", Message: tcpFailureMessage, Diagnostics: detail}
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		return ConnectionError{Code: "system_http_failed", Message: fmt.Sprintf("Проверка интернета через системный туннель вернула HTTP %d.", response.StatusCode)}
	}
	if len(s.pendingDNS) == 0 {
		return ConnectionError{Code: "system_dns_missing", Message: "Для туннеля не получены DNS-серверы."}
	}
	dnsCtx, dnsCancel := context.WithTimeout(ctx, 3*time.Second)
	defer dnsCancel()
	resolver := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return dnsDialer.DialContext(ctx, "udp4", net.JoinHostPort(s.pendingDNS[0], "53"))
	}}
	ips, e := resolver.LookupIP(dnsCtx, "ip4", "qntw.link")
	if e != nil || len(ips) == 0 {
		return ConnectionError{Code: "system_dns_failed", Message: "HTTPS через туннель работает, но DNS-запросы через него не проходят. Подключение остановлено."}
	}
	dnsState, e := s.command(ctx, "", "/usr/sbin/scutil", "--dns")
	if e != nil {
		return ConnectionError{Code: "system_dns_policy_failed", Message: "Не удалось проверить выбор DNS в macOS."}
	}
	first := strings.Split(string(dnsState), "resolver #2")[0]
	if !defaultResolverUses(first, s.pendingDNS[0]) {
		return ConnectionError{Code: "system_dns_policy_failed", Message: "macOS продолжает использовать DNS другого соединения. Подключение остановлено.", Diagnostics: map[string]string{"dns_state": string(dnsState)}}
	}
	return nil
}

func (s *Session) diagnostics() map[string]string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result := map[string]string{}
	s.bridgeMu.Lock()
	if len(s.bridgeErrors) > 0 {
		if b, err := json.Marshal(s.bridgeErrors); err == nil {
			result["bridge_errors"] = string(b)
		}
	}
	s.bridgeMu.Unlock()
	commands := []struct {
		key, name string
		args      []string
	}{{"system_counters", "/usr/sbin/netstat", []string{"-ibn", "-I", s.System.Name}}, {"system_config", "/sbin/ifconfig", []string{s.System.Name}}, {"filter", "/sbin/pfctl", []string{"-a", "com.apple/quantway", "-vvsr"}}, {"filter_counters", "/sbin/pfctl", []string{"-si"}}, {"dns_state", "/usr/sbin/scutil", []string{"--dns"}}}
	if len(s.controlIPs) > 0 {
		commands = append(commands, struct {
			key, name string
			args      []string
		}{"target_route", "/sbin/route", []string{"-n", "get", s.controlIPs[0].String()}})
	}
	for _, c := range commands {
		b, e := s.command(ctx, "", c.name, c.args...)
		if e == nil {
			if len(b) > 8192 {
				b = b[:8192]
			}
			result[c.key] = string(b)
		}
	}
	if s.assignedIP != "" {
		for _, scoped := range []bool{false, true} {
			args := []string{"-n", "get", s.assignedIP}
			key := "assigned_route"
			if scoped {
				args = append(args, "-ifscope", s.overlay)
				key += "_scoped"
			}
			if b, err := s.command(ctx, "", "/sbin/route", args...); err == nil {
				result[key] = string(b)
			}
		}
	}
	if b, err := s.command(ctx, "", "/usr/sbin/netstat", "-anv", "-p", "tcp"); err == nil {
		var owned []string
		for _, line := range strings.Split(string(b), "\n") {
			fields := strings.Fields(line)
			for _, pid := range s.ownedPIDs {
				for _, field := range fields {
					if strings.HasSuffix(field, ":"+strconv.Itoa(pid)) {
						owned = append(owned, line)
						break
					}
				}
			}
		}
		result["owned_tcp"] = strings.Join(owned, "\n")
	}
	return result
}

// A supplemental default DNS may be globally selected without an if_index.
// The explicit UDP check and route check already establish its TUN path.
func defaultResolverUses(block, address string) bool {
	for _, line := range strings.Split(block, "\n") {
		f := strings.Fields(line)
		if len(f) == 3 && strings.HasPrefix(f[0], "nameserver[") && f[1] == ":" && f[2] == address {
			return true
		}
	}
	return false
}
