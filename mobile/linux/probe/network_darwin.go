//go:build darwin && !ios

package probe

import (
	"context"
	"errors"
	"net"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/syntr0x79/qntw-client/mobile/engine"
	"golang.org/x/sys/unix"
)

// network pins every probe socket to the interface selected at run start.
// A changed interface or address set makes in-flight checks fail closed.
type network struct {
	name        string
	fingerprint string
	vpn         bool
}

func VPNNetwork() (engine.Network, error) {
	n, err := currentNetwork()
	if err != nil {
		return nil, err
	}
	n.vpn = true
	n.fingerprint, err = n.snapshot()
	if err != nil {
		return nil, err
	}
	return n, nil
}

func currentNetwork() (*network, error) {
	route, err := currentDarwinRoute()
	if err != nil {
		return nil, err
	}
	n := &network{name: route.iface}
	n.fingerprint, err = n.snapshot()
	if err != nil {
		return nil, err
	}
	return n, nil
}

type darwinRoute struct {
	iface   string
	gateway string
}

// Ask the kernel to resolve an unscoped destination. Walking the routing table
// can instead select a scoped default belonging to an idle virtual interface.
// This query only reads routing state and never sends a packet to the address.
func currentDarwinRoute() (darwinRoute, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/sbin/route", "-n", "get", "1.1.1.1")
	cmd.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "LC_ALL=C"}
	cmd.WaitDelay = 100 * time.Millisecond
	output, err := cmd.Output()
	if err != nil {
		return darwinRoute{}, errors.New("no active network")
	}
	return parseDarwinRoute(string(output))
}

func parseDarwinRoute(output string) (darwinRoute, error) {
	var route darwinRoute
	seen := make(map[string]bool)
	for _, line := range strings.Split(output, "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), ":")
		if !found || (key != "interface" && key != "gateway") {
			continue
		}
		fields := strings.Fields(value)
		if seen[key] || len(fields) != 1 {
			return darwinRoute{}, errors.New("invalid network route")
		}
		seen[key] = true
		if key == "interface" {
			route.iface = fields[0]
		} else {
			route.gateway = fields[0]
		}
	}
	if route.iface == "" {
		return darwinRoute{}, errors.New("no active network")
	}
	// Directly connected and point-to-point routes need not have an IP gateway.
	// Interface existence and state are checked by snapshot, without a name list.
	return route, nil
}

func (n *network) snapshot() (string, error) {
	iface, err := net.InterfaceByName(n.name)
	if err != nil || iface.Flags&net.FlagUp == 0 {
		return "", errors.New("network unavailable")
	}
	if n.vpn {
		addresses, e := iface.Addrs()
		if e != nil {
			return "", e
		}
		list := make([]string, 0, len(addresses))
		for _, a := range addresses {
			list = append(list, a.String())
		}
		sort.Strings(list)
		return strings.Join(list, ","), nil
	}
	route, err := currentDarwinRoute()
	if err != nil {
		return "", err
	}
	addresses, err := iface.Addrs()
	if err != nil {
		return "", err
	}
	return n.routeFingerprint(route, addresses)
}

func (n *network) routeFingerprint(route darwinRoute, addresses []net.Addr) (string, error) {
	if route.iface != n.name {
		return "", errors.New("network changed")
	}
	list := make([]string, 0, len(addresses))
	for _, a := range addresses {
		list = append(list, a.String())
	}
	sort.Strings(list)
	return route.gateway + "|" + strings.Join(list, ","), nil
}
func (n *network) PrepareSocket(fd int) error {
	current, err := n.snapshot()
	if err != nil || current != n.fingerprint {
		return errors.New("network changed")
	}
	iface, err := net.InterfaceByName(n.name)
	if err != nil {
		return err
	}
	family, err := unix.Getsockname(fd)
	if err != nil {
		return err
	}
	if _, ok := family.(*unix.SockaddrInet6); ok {
		return unix.SetsockoptInt(fd, unix.IPPROTO_IPV6, unix.IPV6_BOUND_IF, iface.Index)
	}
	return unix.SetsockoptInt(fd, unix.IPPROTO_IP, unix.IP_BOUND_IF, iface.Index)
}
func (n *network) Resolve(host string) (string, error) {
	before, err := n.snapshot()
	if err != nil || before != n.fingerprint {
		return "", errors.New("network changed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", host)
	if err != nil || len(ips) == 0 {
		return "", errors.New("DNS lookup failed")
	}
	after, err := n.snapshot()
	if err != nil || after != n.fingerprint {
		return "", errors.New("network changed")
	}
	return ips[0].String(), nil
}
