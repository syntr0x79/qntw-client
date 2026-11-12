//go:build windows

// Package network owns Windows VPN policy without invoking shell commands.
// Only interfaces newly created by this session's native engines are mutated.
package network

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"github.com/syntr0x79/qntw-client/mobile/windows/network/wfp"
	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

type Options struct {
	NativeTincPath string
	Interface      string
}

type ownedRoute struct {
	luid   winipcfg.LUID
	prefix netip.Prefix
}

type Session struct {
	mu           sync.Mutex
	physical     *Physical
	filters      *wfp.Session
	system       winipcfg.LUID
	generation   uint64
	overlay      winipcfg.LUID
	overlayIP    netip.Addr
	dnsInstalled bool
	routes       []ownedRoute
	closed       bool
}

// Prepare captures the original uplink and enables protection before either
// tunnel or any route is installed. Existing Quantway adapters are conflicts;
// a new connection never adopts or overwrites another session's interface.
func Prepare(ctx context.Context, options Options) (*Session, error) {
	physical, err := Discover(ctx, options.Interface)
	if err != nil {
		return nil, err
	}
	for _, name := range []string{"qtc0", "qtm0"} {
		if _, e := net.InterfaceByName(name); e == nil {
			return nil, errors.New("Quantway tunnel already exists; disconnect its owning session")
		}
	}
	filters, err := wfp.Open(options.NativeTincPath)
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		_ = filters.Close()
		return nil, err
	}
	return &Session{physical: physical, filters: filters}, nil
}

func (s *Session) PhysicalName() string                { return s.physical.Name() }
func (s *Session) PhysicalIndex() uint32               { return s.physical.Index() }
func (s *Session) PrepareSocket(fd int) error          { return s.physical.PrepareSocket(fd) }
func (s *Session) Resolve(host string) (string, error) { return s.physical.Resolve(host) }
func (s *Session) Transport() *http.Transport          { return s.physical.Transport() }
func (s *Session) NativeEnvironment() string           { return s.physical.NativeEnvironment() }
func (s *Session) Physical() *Physical                 { return s.physical }

func (s *Session) SetTincPeers(peers []netip.AddrPort) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("network session closed")
	}
	if err := ValidatePeers(peers); err != nil {
		return err
	}
	return s.filters.SetPeers(peers)
}

func waitAdapter(ctx context.Context, name string) (winipcfg.LUID, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		if device, err := net.InterfaceByName(name); err == nil && device.Index > 0 {
			luid, err := winipcfg.LUIDFromIndex(uint32(device.Index))
			if err == nil {
				return luid, nil
			}
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-tick.C:
		}
	}
}

func configureIP(luid winipcfg.LUID) error {
	row, err := luid.IPInterface(windows.AF_INET)
	if err != nil {
		return err
	}
	row.NLMTU = 1280
	row.UseAutomaticMetric = false
	row.Metric = 0
	row.RouterDiscoveryBehavior = winipcfg.RouterDiscoveryDisabled
	row.DadTransmits = 0
	return row.Set()
}

// AttachOverlay configures the Wintun device created by the trusted tinc child.
// The mesh carries only the assigned exit subnet, never default Internet routes.
func (s *Session) AttachOverlay(ctx context.Context, name string, selfIP netip.Addr) error {
	if name != "qtm0" || !selfIP.Is4() || !netip.MustParsePrefix("10.77.0.0/16").Contains(selfIP) {
		return errors.New("invalid mesh interface or client address")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.overlay != 0 {
		return errors.New("mesh interface already attached or session closed")
	}
	luid, err := waitAdapter(ctx, name)
	if err != nil {
		return err
	}
	if err = configureIP(luid); err != nil {
		return err
	}
	address := netip.PrefixFrom(selfIP, 32)
	if err = luid.AddIPAddress(address); err != nil {
		return fmt.Errorf("configure mesh address: %w", err)
	}
	s.overlay, s.overlayIP = luid, selfIP
	prefix := netip.MustParsePrefix("10.76.0.0/24")
	if err = luid.AddRoute(prefix, netip.IPv4Unspecified(), 0); err != nil {
		rollback := luid.DeleteIPAddress(address)
		if gone(rollback) {
			s.overlay, s.overlayIP = 0, netip.Addr{}
			rollback = nil
		}
		return errors.Join(fmt.Errorf("configure assigned exit route: %w", err), rollback)
	}
	s.routes = append(s.routes, ownedRoute{luid: luid, prefix: prefix})
	return nil
}

// AttachSystem uses the freshly-created Xray Wintun adapter. It never changes
// the physical adapter's DNS, IP addresses or original default routes.
func (s *Session) AttachSystem(ctx context.Context, name string, dns []netip.Addr) error {
	if name != "qtc0" || ValidateDNS(dns) != nil {
		return errors.New("invalid system tunnel or DNS")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.system != 0 {
		return errors.New("system interface already attached or session closed")
	}
	luid, err := waitAdapter(ctx, name)
	if err != nil {
		return err
	}
	if err = configureIP(luid); err != nil {
		return err
	}
	if err = luid.SetDNS(windows.AF_INET, dns, nil); err != nil {
		return fmt.Errorf("configure tunnel DNS: %w", err)
	}
	s.system, s.dnsInstalled = luid, true
	if err = s.filters.SetTUN(uint64(luid)); err != nil {
		rollback := luid.FlushDNS(windows.AF_INET)
		if gone(rollback) {
			s.system, s.dnsInstalled = 0, false
			rollback = nil
		}
		return errors.Join(err, rollback)
	}
	s.generation++
	return nil
}

// PrepareOverlaySocket pins only the currently owned mesh LUID/address. It
// never adopts another adapter which happens to reuse the qtm0 display name.
func (s *Session) PrepareOverlaySocket(fd int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.overlay == 0 || !s.overlayIP.IsValid() {
		return errors.New("owned mesh interface is unavailable")
	}
	return pinOwnedSocket(fd, s.overlay, s.overlayIP)
}

// PrepareSystemSocket makes a health request enter the owned TUN even on a
// machine with weak-host sending enabled. A source address alone is not proof.
func (s *Session) PrepareSystemSocket(fd int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.system == 0 {
		return errors.New("owned system tunnel is unavailable")
	}
	return pinOwnedSocket(fd, s.system, netip.MustParseAddr("198.18.94.1"))
}

func pinOwnedSocket(fd int, luid winipcfg.LUID, address netip.Addr) error {
	row, err := luid.Interface()
	if err != nil || row.OperStatus != winipcfg.IfOperStatusUp {
		return errors.New("owned tunnel interface is down")
	}
	if _, err := luid.IPAddress(address); err != nil {
		return errors.New("owned tunnel address is unavailable")
	}
	return pinIPv4Socket(windows.Handle(fd), row.InterfaceIndex)
}

type TrafficSnapshot struct {
	Generation uint64
	RXBytes    uint64
	TXBytes    uint64
}

// Traffic observes the exact owned LUID, without finding a device by name.
// Generation distinguishes adapter restarts even if Windows reuses its LUID.
func (s *Session) Traffic() (*TrafficSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.system == 0 {
		return nil, errors.New("owned system tunnel is unavailable")
	}
	row, err := s.system.Interface()
	if err != nil {
		return nil, err
	}
	return &TrafficSnapshot{Generation: s.generation, RXBytes: row.InOctets, TXBytes: row.OutOctets}, nil
}

func (s *Session) InstallSystemRoutes() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.system == 0 {
		return errors.New("system tunnel not attached")
	}
	for _, route := range s.routes {
		if route.luid == s.system {
			return errors.New("system routes already installed")
		}
	}
	retained, err := installRoutes(func(prefix netip.Prefix) error {
		return s.system.AddRoute(prefix, netip.IPv4Unspecified(), 0)
	}, func(prefix netip.Prefix) error {
		rollback := s.system.DeleteRoute(prefix, netip.IPv4Unspecified())
		if gone(rollback) {
			return nil
		}
		return rollback
	})
	for _, prefix := range retained {
		s.routes = append(s.routes, ownedRoute{luid: s.system, prefix: prefix})
	}
	return err
}

func gone(err error) bool {
	return err == nil || errors.Is(err, windows.ERROR_NOT_FOUND) || errors.Is(err, windows.ERROR_FILE_NOT_FOUND) ||
		errors.Is(err, windows.ERROR_DEVICE_NOT_CONNECTED)
}

// DetachPath blocks ordinary traffic first, then removes owned routes and DNS.
// The caller closes Xray/tinc afterwards; WFP stays active through reconnects,
// revoked/expired grants and engine failures until explicit Session.Close.
func (s *Session) DetachPath() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.detachLocked()
}

func (s *Session) detachLocked() error {
	if s.closed {
		return nil
	}
	if err := s.filters.SetTUN(0); err != nil {
		return err
	}
	var result error
	var retained []ownedRoute
	for i := len(s.routes) - 1; i >= 0; i-- {
		route := s.routes[i]
		if err := route.luid.DeleteRoute(route.prefix, netip.IPv4Unspecified()); !gone(err) {
			result = errors.Join(result, err)
			retained = append(retained, route)
		}
	}
	s.routes = retained
	if s.system != 0 && s.dnsInstalled {
		if err := s.system.FlushDNS(windows.AF_INET); !gone(err) {
			result = errors.Join(result, err)
		} else {
			s.dnsInstalled = false
		}
	}
	if s.overlay != 0 && s.overlayIP.IsValid() {
		if err := s.overlay.DeleteIPAddress(netip.PrefixFrom(s.overlayIP, 32)); !gone(err) {
			result = errors.Join(result, err)
		}
	}
	if result == nil {
		s.system, s.overlay, s.overlayIP = 0, 0, netip.Addr{}
	}
	if err := s.filters.SetPeers(nil); err != nil {
		result = errors.Join(result, err)
	}
	return result
}

func (s *Session) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	err := s.detachLocked()
	// Explicit disconnect restores ordinary connectivity. Native engines must
	// be closed by the owner before releasing this last protection handle.
	err = errors.Join(err, s.filters.Close())
	s.closed = true
	return err
}
