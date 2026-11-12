//go:build darwin && !ios

// Package network owns the macOS utun, scoped DNS and session firewall.
// It never changes the physical default route or a user's DNS preferences.
package network

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// ErrCleanupFailed lets the UI distinguish an unsafe cleanup from a failed start.
var ErrCleanupFailed = errors.New("network cleanup failed")

type Device struct {
	File *os.File
	Name string
}

func NewTUN() (*Device, error) {
	fd, e := unix.Socket(unix.AF_SYSTEM, unix.SOCK_DGRAM, 2)
	if e != nil {
		return nil, e
	}
	unix.CloseOnExec(fd)
	ok := false
	defer func() {
		if !ok {
			unix.Close(fd)
		}
	}()
	info := &unix.CtlInfo{}
	copy(info.Name[:], "com.apple.net.utun_control")
	if e = unix.IoctlCtlInfo(fd, info); e != nil {
		return nil, e
	}
	if e = unix.Connect(fd, &unix.SockaddrCtl{ID: info.Id, Unit: 0}); e != nil {
		return nil, e
	}
	name, e := unix.GetsockoptString(fd, 2, 2)
	if e != nil {
		return nil, e
	}
	if e = unix.SetNonblock(fd, true); e != nil {
		return nil, e
	}
	ok = true
	return &Device{os.NewFile(uintptr(fd), name), name}, nil
}
func (d *Device) Close() {
	if d != nil && d.File != nil {
		d.File.Close()
	}
}
func Command(ctx context.Context, input, name string, args ...string) ([]byte, error) {
	c := exec.CommandContext(ctx, name, args...)
	c.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin"}
	if input != "" {
		c.Stdin = strings.NewReader(input)
	}
	b, e := c.CombinedOutput()
	if e != nil {
		if name == "/sbin/route" || name == "/sbin/ifconfig" || name == "/sbin/pfctl" || name == "/usr/sbin/scutil" {
			detail := strings.Join(strings.Fields(string(b)), " ")
			if len(detail) > 400 {
				detail = detail[:400]
			}
			return nil, fmt.Errorf("%s: %s", name, detail)
		}
		return nil, fmt.Errorf("%s: operation failed", name)
	}
	return b, nil
}
func Configure(ctx context.Context, d *Device, address string) error {
	_, e := Command(ctx, "", "/sbin/ifconfig", d.Name, "inet", address, address, "netmask", "255.255.255.255", "mtu", "1280", "up")
	return e
}
func Route(ctx context.Context, op, prefix, iface string) error {
	args := []string{"-n", op, "-net", prefix, "-interface", iface}
	if strings.Contains(prefix, ":") {
		args = []string{"-n", op, "-inet6", "-net", prefix, "-interface", iface}
	}
	_, e := Command(ctx, "", "/sbin/route", args...)
	return e
}

type Session struct {
	Physical       string
	Gateway        string
	physicalRoutes map[string]bool
	System         *Device
	routes         []string
	dnsKey         string
	pfToken        string
	firewall       bool
	once           sync.Once
	cleanupErr     error
	active         bool
	ownedPIDs      []int
	overlay        string
	assignedIP     string
	bridgeMu       sync.Mutex
	bridgeErrors   map[string]string
	pendingDNS     []string
	controlIPs     []net.IP
	run            func(context.Context, string, string, ...string) ([]byte, error)
}

func (s *Session) RecordBridgeError(direction string, err error) {
	s.bridgeMu.Lock()
	defer s.bridgeMu.Unlock()
	if s.bridgeErrors == nil {
		s.bridgeErrors = make(map[string]string)
	}
	s.bridgeErrors[direction] = err.Error()
}

func (s *Session) ObserveNative(pid int, overlay, target string) {
	s.ownedPIDs = []int{os.Getpid(), pid}
	s.overlay, s.assignedIP = overlay, target
}

func Open(ctx context.Context, system bool) (s *Session, err error) {
	if os.Geteuid() != 0 {
		return nil, errors.New("administrator permission is required")
	}
	b, e := Command(ctx, "", "/sbin/route", "-n", "get", "default")
	if e != nil {
		return nil, e
	}
	var iface, gateway string
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[0] == "interface:" {
			iface = f[1]
		}
		if len(f) == 2 && f[0] == "gateway:" {
			gateway = f[1]
		}
	}
	if !regexp.MustCompile(`^(en|bridge|pdp_ip)[0-9]+$`).MatchString(iface) {
		return nil, errors.New("no supported physical network")
	}
	if ip := net.ParseIP(gateway); ip == nil || ip.To4() == nil {
		return nil, errors.New("physical IPv4 gateway unavailable")
	}
	s = &Session{Physical: iface, Gateway: gateway, physicalRoutes: make(map[string]bool)}
	defer func(candidate *Session) {
		if err != nil {
			candidate.Close()
		}
	}(s)
	if !system {
		return s, nil
	}
	s.System, err = NewTUN()
	if err != nil {
		return nil, err
	}
	if err = Configure(ctx, s.System, "198.18.94.1"); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Session) command(ctx context.Context, input, name string, args ...string) ([]byte, error) {
	if s.run != nil {
		return s.run(ctx, input, name, args...)
	}
	return Command(ctx, input, name, args...)
}
func (s *Session) route(ctx context.Context, op, prefix, iface string) error {
	args := []string{"-n", op, "-net", prefix, "-interface", iface}
	if strings.Contains(prefix, ":") {
		args = []string{"-n", op, "-inet6", "-net", prefix, "-interface", iface}
	}
	_, e := s.command(ctx, "", "/sbin/route", args...)
	return e
}

// Activate is called only after the authenticated data path has forwarded a
// health request. Preparing/failed handshakes leave the user's Internet alone.
func (s *Session) Activate(ctx context.Context) error {
	if s.active || s.System == nil {
		return nil
	}
	// Only use Apple's existing wildcard anchor; never replace the global ruleset.
	active, e := s.command(ctx, "", "/sbin/pfctl", "-sr")
	if e != nil || !strings.Contains(string(active), `anchor "com.apple/*"`) {
		return errors.New("compatible system firewall anchor is required")
	}
	// This is an egress guard activated after authenticated TCP sessions exist.
	// flags any + keep state still reconstructs midstream TCP without its SYN
	// window scale (pf.conf(5)); transfer can start and later stall. Evaluate
	// each packet against the owned interface/UID rules instead. Do not flush
	// or alter states owned by macOS or another application.
	rules := fmt.Sprintf("pass out quick on lo0 all flags any no state\npass out quick on %s inet all flags any no state\npass out quick on %s inet proto tcp to any port != 53 user root flags any no state\npass out quick on %s inet proto udp to any port != 53 user root no state\npass out quick inet proto tcp to 10.76.0.0/24 port 18443 user root flags any no state\nblock drop out quick all\n", s.System.Name, s.Physical, s.Physical)
	if _, err := s.command(ctx, rules, "/sbin/pfctl", "-a", "com.apple/quantway", "-f", "-"); err != nil {
		return err
	}
	s.firewall = true
	enabled, e := s.command(ctx, "", "/sbin/pfctl", "-E")
	if e != nil {
		return e
	}
	match := regexp.MustCompile(`Token\s*:\s*([0-9]+)`).FindSubmatch(enabled)
	if len(match) != 2 {
		return errors.New("firewall reference token missing")
	}
	s.pfToken = string(match[1])
	// Expiry keeps these routes and firewall until an explicit disconnect.
	for _, p := range []string{"0.0.0.0/1", "128.0.0.0/1", "::/1", "8000::/1"} {
		if err := s.route(ctx, "add", p, s.System.Name); err != nil {
			return err
		}
		s.routes = append(s.routes, p)
	}
	s.active = true
	if len(s.pendingDNS) > 0 {
		return s.applyDNSNow(ctx, s.pendingDNS)
	}
	return nil
}
func (s *Session) PrepareSocket(fd int) error {
	iface, e := net.InterfaceByName(s.Physical)
	if e != nil || iface.Flags&net.FlagUp == 0 {
		return errors.New("physical network changed")
	}
	a, e := unix.Getsockname(fd)
	if e != nil {
		return e
	}
	if _, ok := a.(*unix.SockaddrInet6); ok {
		return errors.New("external IPv6 transport is not enabled")
	}
	return unix.SetsockoptInt(fd, unix.IPPROTO_IP, unix.IP_BOUND_IF, iface.Index)
}

// Resolve the fixed API origin before installing any VPN routes or DNS.
func (s *Session) PinControlPlane(ctx context.Context) error {
	ips, e := net.DefaultResolver.LookupIP(ctx, "ip4", "qntw.link")
	if e != nil || len(ips) == 0 {
		return errors.New("control service DNS unavailable")
	}
	s.controlIPs = ips
	addresses := make([]string, 0, len(ips))
	for _, ip := range ips {
		addresses = append(addresses, ip.String())
	}
	return s.AllowPhysicalIPs(ctx, addresses)
}
func (s *Session) Transport() *http.Transport {
	control := func(_, address string, c syscall.RawConn) error {
		var result error
		e := c.Control(func(fd uintptr) { result = s.PrepareSocket(int(fd)) })
		if e != nil {
			return e
		}
		return result
	}
	d := &net.Dialer{Timeout: 8 * time.Second, Control: control}
	return &http.Transport{DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(address)
		if e != nil || host != "qntw.link" || len(s.controlIPs) == 0 {
			return nil, errors.New("unknown control origin")
		}
		var last error
		for _, ip := range s.controlIPs {
			c, e := d.DialContext(ctx, "tcp4", net.JoinHostPort(ip.String(), port))
			if e == nil {
				return c, nil
			}
			last = e
		}
		return nil, last
	}, TLSHandshakeTimeout: 8 * time.Second}
}
func (s *Session) ApplyDNS(ctx context.Context, addresses []string) error {
	s.pendingDNS = append([]string(nil), addresses...)
	if !s.active {
		return nil
	}
	return s.applyDNSNow(ctx, addresses)
}
func (s *Session) applyDNSNow(ctx context.Context, addresses []string) error {
	if s.System == nil {
		return nil
	}
	for _, a := range addresses {
		if net.ParseIP(a) == nil {
			return errors.New("invalid DNS address")
		}
	}
	if len(addresses) == 0 {
		return errors.New("missing DNS")
	}
	s.dnsKey = "State:/Network/Service/Quantway-" + s.System.Name + "/DNS"
	script := "d.init\nd.add ServerAddresses * " + strings.Join(addresses, " ") + "\nd.add SupplementalMatchDomains * \"\"\nd.add SearchOrder # 1\nd.add SupplementalMatchDomainsNoSearch # 1\nd.add InterfaceName " + s.System.Name + "\nset " + s.dnsKey + "\nquit\n"
	_, e := s.command(ctx, script, "/usr/sbin/scutil")
	return e
}
func (s *Session) Close() error {
	if s == nil {
		return nil
	}
	s.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if s.dnsKey != "" {
			_, e := s.command(ctx, "remove "+s.dnsKey+"\nquit\n", "/usr/sbin/scutil")
			s.cleanupErr = errors.Join(s.cleanupErr, e)
		}
		for _, p := range s.routes {
			s.cleanupErr = errors.Join(s.cleanupErr, s.route(ctx, "delete", p, s.System.Name))
		}
		s.System.Close()
		for address := range s.physicalRoutes {
			_, e := s.command(ctx, "", "/sbin/route", "-n", "delete", "-host", address, "-ifscope", s.Physical)
			s.cleanupErr = errors.Join(s.cleanupErr, e)
		}
		// Keep the guard on a failed cleanup. Never report a successful disconnect.
		if s.cleanupErr == nil && s.firewall {
			_, e := s.command(ctx, "", "/sbin/pfctl", "-a", "com.apple/quantway", "-F", "rules")
			s.cleanupErr = errors.Join(s.cleanupErr, e)
		}
		if s.cleanupErr == nil && s.pfToken != "" {
			_, e := s.command(ctx, "", "/sbin/pfctl", "-X", s.pfToken)
			s.cleanupErr = errors.Join(s.cleanupErr, e)
		}
	})
	if s.cleanupErr != nil {
		return errors.Join(ErrCleanupFailed, s.cleanupErr)
	}
	return nil
}

// IP_BOUND_IF needs a physical scoped route more specific than the VPN's
// split defaults on macOS. Scope exceptions to the selected uplink so ordinary
// unbound application sockets continue through the system TUN.
func (s *Session) AllowPhysicalIPs(ctx context.Context, addresses []string) error {
	if s.physicalRoutes == nil {
		s.physicalRoutes = make(map[string]bool)
	}
	for _, address := range addresses {
		ip := net.ParseIP(address)
		if ip == nil || ip.To4() == nil {
			return errors.New("invalid physical transport address")
		}
		address = ip.String()
		if s.physicalRoutes[address] {
			continue
		}
		_, e := s.command(ctx, "", "/sbin/route", "-n", "add", "-host", address, s.Gateway, "-ifscope", s.Physical)
		if e != nil {
			existing, query := s.command(ctx, "", "/sbin/route", "-n", "get", address, "-ifscope", s.Physical)
			fields := map[string]string{}
			for _, line := range strings.Split(string(existing), "\n") {
				f := strings.Fields(line)
				if len(f) == 2 {
					fields[f[0]] = f[1]
				}
			}
			if query == nil && fields["destination:"] == address && fields["interface:"] == s.Physical && fields["gateway:"] == s.Gateway {
				if !strings.Contains(string(existing), "WASCLONED") {
					continue
				}
				// Replace only an ephemeral kernel clone for this exact
				// transport; a static route owned by another app is reused.
				if _, remove := s.command(ctx, "", "/sbin/route", "-n", "delete", "-host", address, "-ifscope", s.Physical); remove == nil {
					if _, add := s.command(ctx, "", "/sbin/route", "-n", "add", "-host", address, s.Gateway, "-ifscope", s.Physical); add == nil {
						s.physicalRoutes[address] = true
						continue
					}
				}
			}
			return ConnectionError{Code: "physical_route_failed", Message: "Не удалось закрепить транспорт Tinc за обычной сетью. Подключение остановлено."}
		}
		s.physicalRoutes[address] = true
	}
	return nil
}
