//go:build linux || darwin

// Package tincbridge is the experimental native-process adapter for the client
// tinc spike. It is intentionally separate from the current app VPN engine.
package tincbridge

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const GuardEnvironment = "TINC_SOCKET_GUARD"

// SocketGuard receives duplicate descriptors from the pinned native tinc build.
// The native process must wait for ACK 0 before using an Internet socket. The
// callback protects AND binds the socket to the selected physical network.
// It must return promptly, and must not retain or close the borrowed descriptor.
type SocketGuard struct {
	listener *net.UnixListener
	prepare  func(int) error
	mu       sync.Mutex
	active   *net.UnixConn
	closed   bool
	once     sync.Once
}

// ListenSocketGuard requires a private existing state directory. It never
// removes an existing socket: a second process must not steal a running guard.
func ListenSocketGuard(dir string, prepare func(int) error) (*SocketGuard, error) {
	if prepare == nil {
		return nil, errors.New("tinc socket preparation is required")
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 || !ok || stat.Uid != uint32(os.Geteuid()) {
		return nil, errors.New("tinc state directory must be private and owned by this process")
	}
	path := filepath.Join(dir, "socket-guard")
	if len(path) >= len(unix.RawSockaddrUnix{}.Path) {
		return nil, errors.New("tinc socket guard path is too long")
	}
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	if err = os.Chmod(path, 0600); err != nil {
		l.Close()
		return nil, err
	}
	g := &SocketGuard{listener: l, prepare: prepare}
	go g.serve()
	return g, nil
}

func (g *SocketGuard) Path() string { return g.listener.Addr().String() }

func (g *SocketGuard) Close() error {
	var err error
	g.once.Do(func() {
		g.mu.Lock()
		g.closed = true
		if g.active != nil {
			g.active.Close()
		}
		g.mu.Unlock()
		err = g.listener.Close()
	})
	return err
}

func (g *SocketGuard) serve() {
	for {
		c, err := g.listener.AcceptUnix()
		if err != nil {
			return
		}
		g.mu.Lock()
		if g.closed {
			g.mu.Unlock()
			c.Close()
			return
		}
		g.active = c
		g.mu.Unlock()
		g.handle(c)
		c.Close()
		g.mu.Lock()
		g.active = nil
		g.mu.Unlock()
	}
}

func (g *SocketGuard) handle(c *net.UnixConn) {
	c.SetDeadline(time.Now().Add(5 * time.Second))
	data, ancillary := make([]byte, 2), make([]byte, unix.CmsgSpace(16*4))
	n, oobn, flags, _, readErr := c.ReadMsgUnix(data, ancillary)
	var fds []int
	defer func() {
		for _, fd := range fds {
			unix.Close(fd)
		}
	}()
	// Parse even a truncated request to close every descriptor delivered by the
	// kernel. Extra descriptors and malformed requests never invoke prepare.
	msgs, parseErr := unix.ParseSocketControlMessage(ancillary[:oobn])
	for _, msg := range msgs {
		if msg.Header.Level != unix.SOL_SOCKET || msg.Header.Type != unix.SCM_RIGHTS {
			parseErr = errors.New("unexpected control message")
			continue
		}
		rights, err := unix.ParseUnixRights(&msg)
		if err != nil {
			parseErr = err
			continue
		}
		for _, fd := range rights {
			unix.CloseOnExec(fd)
			fds = append(fds, fd)
		}
	}
	result := byte(1)
	if readErr == nil && parseErr == nil && flags&(unix.MSG_CTRUNC|unix.MSG_TRUNC) == 0 && n == 1 && data[0] == 1 && len(fds) == 1 {
		if err := unconnectedInternetSocket(fds[0]); err == nil {
			if err = g.prepare(fds[0]); err == nil {
				result = 0
			}
		}
	}
	c.Write([]byte{result})
}

func unconnectedInternetSocket(fd int) error {
	kind, err := unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_TYPE)
	if err != nil || (kind != unix.SOCK_STREAM && kind != unix.SOCK_DGRAM) {
		return errors.New("expected a TCP or UDP socket")
	}
	addr, err := unix.Getsockname(fd)
	if err != nil {
		return err
	}
	switch a := addr.(type) {
	case *unix.SockaddrInet4:
		if a.Port != 0 {
			return errors.New("socket already bound")
		}
	case *unix.SockaddrInet6:
		if a.Port != 0 {
			return errors.New("socket already bound")
		}
	default:
		return errors.New("expected an Internet socket")
	}
	if _, err = unix.Getpeername(fd); !errors.Is(err, unix.ENOTCONN) {
		return fmt.Errorf("socket must be unconnected: %v", err)
	}
	return nil
}
