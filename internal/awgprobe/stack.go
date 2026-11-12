package awgprobe

import (
	"context"
	"github.com/amnezia-vpn/amneziawg-go/v3/device"
	"github.com/amnezia-vpn/amneziawg-go/v3/tun/netstack"
	"net"
	"net/netip"
	"sync"
)

type Stack struct {
	net  *netstack.Net
	dev  *device.Device
	once sync.Once
}

func OpenStack(ctx context.Context, conf string, o Options) (*Stack, error) {
	c, e := parseConf(conf)
	if e != nil {
		return nil, e
	}
	uapi, endpoint, e := c.uapi(ctx, o.Resolve)
	if e != nil {
		return nil, e
	}
	tn, n, e := netstack.CreateNetTUN([]netip.Addr{c.address}, c.dns, c.mtu)
	if e != nil {
		return nil, e
	}
	network := "udp4"
	if endpoint.Addr().Is6() {
		network = "udp6"
	}
	dev := device.NewDevice(tn, newBind(o.Listen, network), device.NewLogger(device.LogLevelSilent, ""))
	if e = dev.IpcSet(uapi); e != nil {
		dev.Close()
		return nil, e
	}
	flushStaleRead(n)
	if e = dev.Up(); e != nil {
		dev.Close()
		return nil, e
	}
	return &Stack{net: n, dev: dev}, nil
}
func (s *Stack) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return s.net.DialContext(ctx, network, address)
}
func (s *Stack) Close() error { s.once.Do(func() { s.dev.Close() }); return nil }
