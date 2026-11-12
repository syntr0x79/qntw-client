//go:build linux && !android

package awgprobe

import (
	"context"
	"github.com/amnezia-vpn/amneziawg-go/v3/device"
	"github.com/amnezia-vpn/amneziawg-go/v3/tun"
	"golang.org/x/sys/unix"
	"io"
)

// StartTunnel owns a duplicate of the caller-provided Linux TUN descriptor.
// The caller must configure interface addresses, routes and protected UDP sockets.
func StartTunnel(ctx context.Context, fd int, conf string, options Options) (io.Closer, error) {
	c, err := parseConf(conf)
	if err != nil {
		return nil, err
	}
	uapi, endpoint, err := c.uapi(ctx, options.Resolve)
	if err != nil {
		return nil, err
	}
	ownedFD, err := unix.Dup(fd)
	if err != nil {
		return nil, err
	}
	nic, _, err := tun.CreateUnmonitoredTUNFromFD(ownedFD)
	if err != nil {
		unix.Close(ownedFD)
		return nil, err
	}
	network := "udp4"
	if endpoint.Addr().Is6() {
		network = "udp6"
	}
	dev := device.NewDevice(nic, newBind(options.Listen, network), device.NewLogger(device.LogLevelSilent, ""))
	if err = dev.IpcSet(uapi); err != nil {
		dev.Close()
		return nil, err
	}
	if err = dev.Up(); err != nil {
		dev.Close()
		return nil, err
	}
	return awgTunnel{dev}, nil
}

type awgTunnel struct{ dev *device.Device }

func (t awgTunnel) Close() error { t.dev.Close(); return nil }
