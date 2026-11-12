//go:build linux

// Package tun owns a non-persistent Linux TUN device. It never changes routes or DNS.
package tun

import (
	"errors"
	"fmt"
	"github.com/vishvananda/netlink"
	"net"
	"net/netip"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

type Device struct {
	File *os.File
	Name string
}

// Create requires CAP_NET_ADMIN. Closing File removes the non-persistent device.
func Create() (*Device, error) {
	fd, err := unix.Open("/dev/net/tun", unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open TUN: %w", err)
	}
	req, err := unix.NewIfreq("qw%d")
	if err != nil {
		unix.Close(fd)
		return nil, err
	}
	req.SetUint16(unix.IFF_TUN | unix.IFF_NO_PI)
	if err = unix.IoctlIfreq(fd, unix.TUNSETIFF, req); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("create TUN: %w", err)
	}
	name := strings.TrimSpace(req.Name())
	if name == "" {
		unix.Close(fd)
		return nil, errors.New("missing TUN name")
	}
	return &Device{File: os.NewFile(uintptr(fd), name), Name: name}, nil
}
func (d *Device) Close() error {
	if d == nil || d.File == nil {
		return nil
	}
	return d.File.Close()
}

// Configure assigns the tunnel's local address and MTU. Routing and DNS are
// deliberately separate so failures can be rolled back before traffic changes.
func (d *Device) Configure(address netip.Prefix, mtu int) error {
	if d == nil || d.File == nil || !address.IsValid() || !address.Addr().Is4() || mtu < 576 || mtu > 9000 {
		return errors.New("invalid tunnel configuration")
	}
	link, err := netlink.LinkByName(d.Name)
	if err != nil {
		return fmt.Errorf("find TUN: %w", err)
	}
	if err = netlink.LinkSetMTU(link, mtu); err != nil {
		return fmt.Errorf("set MTU: %w", err)
	}
	a, err := netlink.ParseAddr(address.String())
	if err != nil {
		return err
	}
	if err = netlink.AddrAdd(link, a); err != nil {
		return fmt.Errorf("add TUN address: %w", err)
	}
	if err = netlink.LinkSetUp(link); err != nil {
		return fmt.Errorf("start TUN: %w", err)
	}
	return nil
}

// InstallRoutes adds IPv4 and IPv6 split-default routes to the TUN without
// deleting previous default routes. More-specific routes can still take priority. All routes are attached to this non-persistent link
// and therefore disappear if the owning process exits. On a conflict, any
// routes added by this call are rolled back.
func (d *Device) InstallRoutes() error {
	if d == nil || d.File == nil {
		return errors.New("invalid tunnel")
	}
	link, err := netlink.LinkByName(d.Name)
	if err != nil {
		return fmt.Errorf("find TUN: %w", err)
	}
	prefixes := []string{"0.0.0.0/1", "128.0.0.0/1", "::/1", "8000::/1"}
	added := make([]netlink.Route, 0, len(prefixes))
	for _, prefix := range prefixes {
		_, network, err := net.ParseCIDR(prefix)
		if err != nil {
			return err
		}
		route := netlink.Route{LinkIndex: link.Attrs().Index, Dst: network, Scope: netlink.SCOPE_LINK}
		if err = netlink.RouteAdd(&route); err != nil {
			for i := len(added) - 1; i >= 0; i-- {
				_ = netlink.RouteDel(&added[i])
			}
			return fmt.Errorf("install tunnel route: %w", err)
		}
		added = append(added, route)
	}
	return nil
}

// RemoveRoutes removes the split-default routes only when they still point to
// this interface. It is safe to call when a route was already removed.
func (d *Device) RemoveRoutes() error {
	if d == nil || d.File == nil {
		return errors.New("invalid tunnel")
	}
	link, err := netlink.LinkByName(d.Name)
	if err != nil {
		return fmt.Errorf("find TUN: %w", err)
	}
	prefixes := []string{"0.0.0.0/1", "128.0.0.0/1", "::/1", "8000::/1"}
	var result error
	for _, prefix := range prefixes {
		_, network, parseErr := net.ParseCIDR(prefix)
		if parseErr != nil {
			return parseErr
		}
		routes, listErr := netlink.RouteListFiltered(netlink.FAMILY_ALL, &netlink.Route{LinkIndex: link.Attrs().Index, Dst: network}, netlink.RT_FILTER_OIF|netlink.RT_FILTER_DST)
		if listErr != nil {
			result = errors.Join(result, listErr)
			continue
		}
		for _, route := range routes {
			if err := netlink.RouteDel(&route); err != nil && !errors.Is(err, unix.ESRCH) {
				result = errors.Join(result, err)
			}
		}
	}
	return result
}
