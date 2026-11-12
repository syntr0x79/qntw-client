//go:build linux

// Package desktopvpn connects the unprivileged VPN engine to the experimental
// privileged network helper. It is not yet connected to the user interface.
package desktopvpn

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/syntr0x79/qntw-client/mobile/engine"
	"github.com/syntr0x79/qntw-client/mobile/linux/privsep"
)

const helperPath = "/usr/libexec/quantway/quantway-net-helper"

type Config struct {
	Job        string
	Routing    string
	Address    netip.Prefix
	DNS        netip.Addr
	Network    engine.Network
	ControlURL string
	// verify and launch are used only by isolated package tests.
	verify func() error
	launch func(context.Context, *os.File) (<-chan error, error)
}
type Session struct {
	once     sync.Once
	channel  *net.UnixConn
	tun      *os.File
	done     <-chan error
	closeErr error
	closed   chan struct{}
	cancel   context.CancelFunc
}

var lifecycle struct {
	sync.Mutex
	active bool
}

// trustedHelper rejects a user-writable or symlinked executable and any
// user-writable parent. The installed binary must be managed by the OS admin.
func trustedHelper(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("invalid helper path")
	}
	for current := path; ; current = filepath.Dir(current) {
		fi, err := os.Lstat(current)
		if err != nil {
			return err
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != 0 || fi.Mode()&os.ModeSymlink != 0 || fi.Mode().Perm()&0022 != 0 {
			return errors.New("untrusted helper ownership or permissions")
		}
		if current == path && (!fi.Mode().IsRegular() || fi.Mode().Perm()&0111 == 0) {
			return errors.New("invalid helper executable")
		}
		if current != path && !fi.IsDir() {
			return errors.New("invalid helper directory")
		}
		if current == "/" {
			break
		}
	}
	return nil
}
func launchHelper(ctx context.Context, child *os.File) (<-chan error, error) {
	if err := trustedHelper(helperPath); err != nil {
		return nil, fmt.Errorf("network helper is not installed securely: %w", err)
	}
	cmd := exec.CommandContext(ctx, "/usr/bin/pkexec", helperPath)
	cmd.Stdin = child
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait(); close(done) }()
	return done, nil
}

func Start(ctx context.Context, c Config) (session *Session, err error) {
	if c.Network == nil || c.Job == "" || c.ControlURL == "" || !c.Address.IsValid() || !c.DNS.IsValid() {
		return nil, errors.New("incomplete VPN configuration")
	}
	lifecycle.Lock()
	if lifecycle.active {
		lifecycle.Unlock()
		return nil, errors.New("VPN already active")
	}
	lifecycle.active = true
	lifecycle.Unlock()
	success := false
	defer func() {
		if !success {
			lifecycle.Lock()
			lifecycle.active = false
			lifecycle.Unlock()
		}
	}()
	parent, child, err := privsep.NewPair()
	if err != nil {
		return nil, err
	}
	launchCtx, cancel := context.WithCancel(ctx)
	defer func() {
		if !success {
			cancel()
		}
	}()
	launch := c.launch
	if launch == nil {
		launch = launchHelper
	}
	done, err := launch(launchCtx, child)
	child.Close()
	if err != nil {
		parent.Close()
		return nil, err
	}
	s := &Session{channel: parent, done: done, cancel: cancel, closed: make(chan struct{})}
	engineStarted := false
	activated := false
	defer func() {
		if !success {
			if activated {
				_ = privsep.Stop(parent)
			}
			if engineStarted {
				engine.StopVPN()
			}
			if s.tun != nil {
				_ = s.tun.Close()
			}
			_ = parent.Close()
		}
	}()
	s.tun, _, err = privsep.RequestTunnel(parent, c.Address, c.DNS)
	if err != nil {
		return nil, fmt.Errorf("create tunnel: %w", err)
	}
	routing := c.Routing
	if routing == "" {
		routing = "{}"
	}
	if err = engine.ConfigureRouting(routing); err != nil {
		return nil, err
	}
	if err = engine.StartVPN(int(s.tun.Fd()), c.Job, c.Network); err != nil {
		return nil, fmt.Errorf("start engine: %w", err)
	}
	engineStarted = true
	if err = privsep.Activate(parent); err != nil {
		return nil, fmt.Errorf("activate network: %w", err)
	}
	activated = true
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if c.verify != nil {
		err = c.verify()
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
	// A helper crash closes its side of the socket. Stop the engine and release
	// the duplicate TUN descriptor; the helper owns route and DNS cleanup.
	go func() { <-done; _ = s.Close() }()
	return s, nil
}
func (s *Session) Close() error {
	if s == nil {
		return nil
	}
	s.once.Do(func() {
		defer close(s.closed)
		s.closeErr = privsep.Stop(s.channel)
		engine.StopVPN()
		if s.tun != nil {
			s.closeErr = errors.Join(s.closeErr, s.tun.Close())
		}
		s.closeErr = errors.Join(s.closeErr, s.channel.Close())
		s.cancel()
		lifecycle.Lock()
		lifecycle.active = false
		lifecycle.Unlock()
	})
	return s.closeErr
}

// Done closes when the tunnel stops, including an unexpected helper exit.
func (s *Session) Done() <-chan struct{} { return s.closed }
