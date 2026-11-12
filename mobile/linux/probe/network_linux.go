//go:build linux

package probe

import (
	"context"
	"errors"
	"net"
	"sort"
	"strings"
	"time"

	netroute "github.com/libp2p/go-netroute"
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
	router, err := netroute.New()
	if err != nil {
		return nil, err
	}
	iface, _, _, err := router.Route(net.ParseIP("1.1.1.1"))
	if err != nil || iface == nil {
		return nil, errors.New("no active network")
	}
	n := &network{name: iface.Name}
	n.fingerprint, err = n.snapshot()
	if err != nil {
		return nil, err
	}
	return n, nil
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
	router, err := netroute.New()
	if err != nil {
		return "", err
	}
	active, gateway, source, err := router.Route(net.ParseIP("1.1.1.1"))
	if err != nil || active == nil || active.Name != n.name {
		return "", errors.New("network changed")
	}
	addresses, err := iface.Addrs()
	if err != nil {
		return "", err
	}
	list := make([]string, 0, len(addresses))
	for _, a := range addresses {
		list = append(list, a.String())
	}
	sort.Strings(list)
	return gateway.String() + "|" + source.String() + "|" + strings.Join(list, ","), nil
}
func (n *network) PrepareSocket(fd int) error {
	current, err := n.snapshot()
	if err != nil || current != n.fingerprint {
		return errors.New("network changed")
	}
	return unix.SetsockoptString(fd, unix.SOL_SOCKET, unix.SO_BINDTODEVICE, n.name)
}
func (n *network) Resolve(host string) (string, error) {
	before, err := n.snapshot()
	if err != nil || before != n.fingerprint {
		return "", errors.New("network changed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return "", errors.New("DNS lookup failed")
	}
	after, err := n.snapshot()
	if err != nil || after != n.fingerprint {
		return "", errors.New("network changed")
	}
	return ips[0].String(), nil
}
