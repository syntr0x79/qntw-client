//go:build linux && !android

package clienttinc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const socketMark = 0x7133
const routeTable = "28979" // decimal socketMark; private pilot route table.

type tunDevice struct {
	file *os.File
	name string
}

func newTUN(name string) (*tunDevice, error) {
	if _, e := net.InterfaceByName(name); e == nil {
		return nil, RuntimeError{Code: "route_conflict"}
	}
	fd, e := unix.Open("/dev/net/tun", unix.O_RDWR|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, RuntimeError{Code: "tun_unavailable"}
	}
	req, e := unix.NewIfreq(name)
	if e != nil {
		unix.Close(fd)
		return nil, e
	}
	req.SetUint16(unix.IFF_TUN | unix.IFF_NO_PI)
	if e = unix.IoctlIfreq(fd, unix.TUNSETIFF, req); e != nil {
		unix.Close(fd)
		return nil, e
	}
	return &tunDevice{file: os.NewFile(uintptr(fd), req.Name()), name: req.Name()}, nil
}
func (d *tunDevice) close() {
	if d != nil && d.file != nil {
		d.file.Close()
	}
}

func command(ctx context.Context, input string, name string, args ...string) ([]byte, error) {
	c := exec.CommandContext(ctx, name, args...)
	if input != "" {
		c.Stdin = strings.NewReader(input)
	}
	b, e := c.CombinedOutput()
	if e != nil {
		message := strings.TrimSpace(string(b))
		if len(message) > 2048 {
			message = message[:2048]
		}
		return nil, fmt.Errorf("%s network operation failed: %w: %s", filepath.Base(name), e, message)
	}
	return b, nil
}
func ip(ctx context.Context, args ...string) error { _, e := command(ctx, "", "ip", args...); return e }

type network struct {
	iface                      string
	system                     *tunDevice
	table, rule, firewall, dns bool
	dnsMode                    string
	bootstrapDNS               string
}

func prepareNetwork(ctx context.Context, iface string, system bool, dnsMode string) (n *network, err error) {
	if os.Geteuid() != 0 {
		return nil, errors.New("the standalone prototype requires root in its network namespace")
	}
	for _, tool := range []string{"ip", "nft"} {
		if _, e := exec.LookPath(tool); e != nil {
			return nil, RuntimeError{Code: "network_tools_missing"}
		}
	}
	var defaults []struct {
		Dev     string `json:"dev"`
		Gateway string `json:"gateway"`
		Metric  int    `json:"metric"`
	}
	b, e := command(ctx, "", "ip", "-j", "-4", "route", "show", "default")
	if e != nil {
		return nil, e
	}
	if json.Unmarshal(b, &defaults) != nil {
		return nil, errors.New("cannot read physical route")
	}
	var dev, gw string
	best := int(^uint(0) >> 1)
	for _, r := range defaults {
		if (iface == "" || r.Dev == iface) && r.Metric < best {
			dev, gw, best = r.Dev, r.Gateway, r.Metric
		}
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,15}$`).MatchString(dev) {
		return nil, errors.New("no usable physical default route")
	}
	n = &network{iface: dev, dnsMode: dnsMode}
	// A systemd-resolved loopback stub cannot be bound to the physical link.
	// Capture the real uplink resolver before installing the VPN's DNS policy.
	for _, path := range []string{"/run/systemd/resolve/resolv.conf", "/etc/resolv.conf"} {
		data, _ := os.ReadFile(path)
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 2 && fields[0] == "nameserver" {
				addr, e := netip.ParseAddr(fields[1])
				if e == nil && addr.Is4() && !addr.IsLoopback() && !addr.IsUnspecified() {
					n.bootstrapDNS = addr.String()
					break
				}
			}
		}
		if n.bootstrapDNS != "" {
			break
		}
	}
	if n.bootstrapDNS == "" {
		n.bootstrapDNS = "1.1.1.1"
	}
	defer func(candidate *network) {
		if err != nil {
			candidate.close()
		}
	}(n)
	b, _ = command(ctx, "", "ip", "-4", "route", "show", "table", routeTable)
	if len(strings.TrimSpace(string(b))) != 0 {
		return nil, RuntimeError{Code: "route_conflict"}
	}
	b, e = command(ctx, "", "ip", "-j", "rule", "show")
	if e != nil {
		return nil, e
	}
	if strings.Contains(string(b), routeTable) {
		return nil, RuntimeError{Code: "route_conflict"}
	}
	args := []string{"-4", "route", "add", "table", routeTable, "default"}
	if gw != "" {
		args = append(args, "via", gw)
	}
	args = append(args, "dev", dev)
	if gw != "" {
		args = append(args, "onlink")
	}
	if e = ip(ctx, args...); e != nil {
		return nil, e
	}
	n.table = true
	if e = ip(ctx, "-4", "rule", "add", "priority", "10512", "fwmark", routeTable, "lookup", routeTable); e != nil {
		return nil, e
	}
	n.rule = true
	if system {
		if _, e = command(ctx, "", "nft", "list", "table", "inet", "qclient_tinc"); e == nil {
			return nil, RuntimeError{Code: "route_conflict"}
		}
		if dnsMode != "resolved" && dnsMode != "none" {
			return nil, errors.New("DNS mode must be resolved or none")
		}
		if dnsMode == "resolved" {
			if _, e = exec.LookPath("resolvectl"); e != nil {
				return nil, RuntimeError{Code: "dns_unavailable"}
			}
		}
		n.system, e = newTUN("qtc0")
		if e != nil {
			return nil, e
		}
		if e = ip(ctx, "addr", "add", "198.18.94.1/30", "dev", n.system.name); e != nil {
			return nil, e
		}
		if e = ip(ctx, "link", "set", n.system.name, "mtu", "1280", "up"); e != nil {
			return nil, e
		}
		for _, prefix := range []string{"0.0.0.0/1", "128.0.0.0/1"} {
			if e = ip(ctx, "route", "add", prefix, "dev", n.system.name); e != nil {
				return nil, e
			}
		}
		rules := fmt.Sprintf(`table inet qclient_tinc {
 chain output {
  type filter hook output priority -50; policy accept;
  meta mark %d accept
  oifname "lo" accept
  oifname "qtc0" meta nfproto ipv4 accept
  oifname "qtm0" ip daddr 10.76.0.0/24 tcp dport 18443 accept
  reject
 }
}
`, socketMark)
		if _, e = command(ctx, rules, "nft", "-f", "-"); e != nil {
			return nil, e
		}
		n.firewall = true
	}
	return n, nil
}

func (n *network) prepare(fd int) error {
	if e := unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_MARK, socketMark); e != nil {
		return e
	}
	return unix.SetsockoptString(fd, unix.SOL_SOCKET, unix.SO_BINDTODEVICE, n.iface)
}
func (n *network) transport() *http.Transport {
	control := func(_, address string, c syscall.RawConn) error {
		var result error
		e := c.Control(func(fd uintptr) {
			host, _, _ := net.SplitHostPort(address)
			if a, e := netip.ParseAddr(host); e == nil && a.IsLoopback() {
				result = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_MARK, socketMark)
			} else {
				result = n.prepare(int(fd))
			}
		})
		if e != nil {
			return e
		}
		return result
	}
	dialer := &net.Dialer{Timeout: 8 * time.Second, Control: control}
	dialer.Resolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
		d := net.Dialer{Timeout: 5 * time.Second, Control: control}
		return d.DialContext(ctx, network, net.JoinHostPort(n.bootstrapDNS, "53"))
	}}
	return &http.Transport{Proxy: nil, DialContext: dialer.DialContext, ForceAttemptHTTP2: true, TLSHandshakeTimeout: 8 * time.Second}
}
func (n *network) applyDNS(ctx context.Context, addresses []string) error {
	if n.system == nil || n.dnsMode == "none" {
		return nil
	}
	args := append([]string{"dns", n.system.name}, addresses...)
	if _, e := command(ctx, "", "resolvectl", args...); e != nil {
		return RuntimeError{Code: "dns_unavailable"}
	}
	n.dns = true
	if _, e := command(ctx, "", "resolvectl", "domain", n.system.name, "~."); e != nil {
		return RuntimeError{Code: "dns_unavailable"}
	}
	_, e := command(ctx, "", "resolvectl", "default-route", n.system.name, "yes")
	if e != nil {
		return RuntimeError{Code: "dns_unavailable"}
	}
	return e
}
func (n *network) close() error {
	if n == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if n.dns && n.system != nil {
		command(ctx, "", "resolvectl", "revert", n.system.name)
	}
	// Remove the fail-closed filter only on explicit lifecycle shutdown. Lease
	// expiry stops the data path while retaining this filter and system TUN.
	if n.firewall {
		command(ctx, "", "nft", "delete", "table", "inet", "qclient_tinc")
	}
	n.system.close()
	if n.rule {
		ip(ctx, "-4", "rule", "del", "priority", "10512", "fwmark", strconv.Itoa(socketMark), "lookup", routeTable)
	}
	if n.table {
		ip(ctx, "-4", "route", "flush", "table", routeTable)
	}
	return nil
}

func (n *network) activate(context.Context) error { return nil }

func (n *network) checkActiveHealth(context.Context) (bool, error) { return false, nil }
