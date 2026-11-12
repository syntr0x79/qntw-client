//go:build linux

// Package vpn contains the experimental, non-UI Linux tunnel lifecycle.
// It is not a release-ready VPN: leak prevention, crash recovery, privilege
// separation, real DNS integration and reconnection still require validation.
package vpn

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sync"

	"github.com/syntr0x79/qntw-client/mobile/engine"
	"github.com/syntr0x79/qntw-client/mobile/linux/tun"
)

type DNS interface {
	Apply(context.Context, string, netip.Addr) error
	Revert(context.Context, string) error
}
type Config struct {
	Job        string
	Routing    string
	Address    netip.Prefix
	DNSAddress netip.Addr
	Network    engine.Network
	ControlURL string
	DNS        DNS
	// Verify is intended for controlled tests. A nil verifier uses a real HTTPS check.
	Verify func(context.Context) error
}

var lifecycle struct {
	sync.Mutex
	active bool
}

type Session struct {
	once          sync.Once
	device        *tun.Device
	dns           DNS
	closeErr      error
	engineStarted bool
	dnsStarted    bool
}

// Start is transactional: every successful stage is unwound on an error.
// The caller must provide privileged execution and a pinned physical network.
func Start(ctx context.Context, c Config) (session *Session, err error) {
	if c.Network == nil || c.DNS == nil || !c.Address.IsValid() || !c.Address.Addr().Is4() || !c.DNSAddress.IsValid() || c.Job == "" || c.ControlURL == "" {
		return nil, errors.New("incomplete VPN configuration")
	}
	lifecycle.Lock()
	if lifecycle.active {
		lifecycle.Unlock()
		return nil, errors.New("VPN already active")
	}
	lifecycle.active = true
	lifecycle.Unlock()
	s := &Session{dns: c.DNS}
	success := false
	defer func() {
		if !success {
			_ = s.Close()
		}
	}()
	s.device, err = tun.Create()
	if err != nil {
		return nil, err
	}
	if err = s.device.Configure(c.Address, 1280); err != nil {
		return nil, err
	}
	routing := c.Routing
	if routing == "" {
		routing = "{}"
	}
	if err = engine.ConfigureRouting(routing); err != nil {
		return nil, fmt.Errorf("routing configuration: %w", err)
	}
	if err = engine.StartVPN(int(s.device.File.Fd()), c.Job, c.Network); err != nil {
		return nil, fmt.Errorf("start tunnel: %w", err)
	}
	s.engineStarted = true
	s.dnsStarted = true
	if err = c.DNS.Apply(ctx, s.device.Name, c.DNSAddress); err != nil {
		return nil, fmt.Errorf("configure DNS: %w", err)
	}
	if err = s.device.InstallRoutes(); err != nil {
		return nil, err
	}
	if c.Verify != nil {
		err = c.Verify(ctx)
	} else {
		var ok bool
		ok, err = engine.CheckSystemVPN(c.ControlURL)
		if err == nil && !ok {
			err = errors.New("VPN health check failed")
		}
	}
	if err != nil {
		return nil, err
	}
	success = true
	return s, nil
}
func (s *Session) Close() error {
	if s == nil {
		return nil
	}
	s.once.Do(func() {
		if s.device != nil && s.dnsStarted {
			s.closeErr = errors.Join(s.closeErr, s.dns.Revert(context.Background(), s.device.Name))
		}
		if s.engineStarted {
			engine.StopVPN()
		}
		if s.device != nil {
			s.closeErr = errors.Join(s.closeErr, s.device.Close())
		}
		lifecycle.Lock()
		lifecycle.active = false
		lifecycle.Unlock()
	})
	return s.closeErr
}
