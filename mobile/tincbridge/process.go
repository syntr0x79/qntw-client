//go:build linux || darwin

package tincbridge

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ProcessOptions refers to a local trusted tinc configuration prepared by the
// spike, never a user-supplied config or backend command. DeviceType must be fd,
// Device must be 3. Packet must preserve datagram boundaries (TUN or socketpair).
// This package does not install a system VPN, choose an exit or apply an ACL.
type ProcessOptions struct {
	Binary        string
	ConfigDir     string
	Packet        *os.File
	PrepareSocket func(int) error
	ExpiresAt     time.Time
}

type Process struct {
	cmd   *exec.Cmd
	guard *SocketGuard
	done  chan struct{}
	once  sync.Once
	err   error // Written before closing done.
}

// Start lends a packet descriptor to the child as fd 3. The caller keeps
// ownership of its own descriptor. Native sockets fail closed on guard errors.
func Start(ctx context.Context, o ProcessOptions) (*Process, error) {
	if ctx == nil || ctx.Err() != nil || o.Packet == nil || o.Binary == "" || o.ConfigDir == "" || !o.ExpiresAt.After(time.Now()) {
		return nil, errors.New("tinc process requires a context, binary, private config, packet device and live lease")
	}
	if !filepath.IsAbs(o.Binary) || !filepath.IsAbs(o.ConfigDir) {
		return nil, errors.New("tinc process paths must be absolute")
	}
	g, err := ListenSocketGuard(o.ConfigDir, o.PrepareSocket)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(o.Binary, "-c", o.ConfigDir, "-D", "--pidfile="+filepath.Join(o.ConfigDir, "tinc.pid"), "--logfile="+filepath.Join(o.ConfigDir, "tinc.log"))
	cmd.ExtraFiles = []*os.File{o.Packet}
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, GuardEnvironment+"=") {
			cmd.Env = append(cmd.Env, value)
		}
	}
	cmd.Env = append(cmd.Env, GuardEnvironment+"="+g.Path())
	if err = cmd.Start(); err != nil {
		g.Close()
		return nil, err
	}
	p := &Process{cmd: cmd, guard: g, done: make(chan struct{})}
	go func() {
		p.err = cmd.Wait()
		g.Close()
		close(p.done)
	}()
	go func() {
		timer := time.NewTimer(time.Until(o.ExpiresAt))
		defer timer.Stop()
		select {
		case <-ctx.Done():
			p.Close()
		case <-timer.C:
			p.Close()
		case <-p.done:
		}
	}()
	return p, nil
}

func (p *Process) Running() bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

func (p *Process) Wait() error {
	<-p.done
	return p.err
}

func (p *Process) Close() error {
	p.once.Do(func() {
		p.guard.Close()
		p.cmd.Process.Signal(syscall.SIGTERM)
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		select {
		case <-p.done:
		case <-timer.C:
			p.cmd.Process.Kill()
			<-p.done
		}
	})
	return nil
}
