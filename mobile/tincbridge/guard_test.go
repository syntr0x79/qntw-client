//go:build linux || darwin

package tincbridge

import (
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// Short paths also work on Darwin and inside Android's Unix socket path limit.
func stateDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "tinc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func request(t *testing.T, path string, payload []byte, fds ...int) byte {
	t.Helper()
	c, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(time.Second))
	var ancillary []byte
	if len(fds) > 0 {
		ancillary = unix.UnixRights(fds...)
	}
	if _, _, err = c.WriteMsgUnix(payload, ancillary, nil); err != nil {
		t.Fatal(err)
	}
	var ack [1]byte
	if _, err = io.ReadFull(c, ack[:]); err != nil {
		t.Fatal(err)
	}
	return ack[0]
}

func socket(t *testing.T, family, kind int) int {
	t.Helper()
	fd, err := unix.Socket(family, kind, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unix.Close(fd) })
	return fd
}

func TestGuardPreparesActualSocketBeforeAcknowledging(t *testing.T) {
	for _, kind := range []int{unix.SOCK_STREAM, unix.SOCK_DGRAM} {
		t.Run(map[int]string{unix.SOCK_STREAM: "TCP", unix.SOCK_DGRAM: "UDP"}[kind], func(t *testing.T) {
			fd := socket(t, unix.AF_INET, kind)
			var calls atomic.Int32
			g, err := ListenSocketGuard(stateDir(t), func(borrowed int) error {
				calls.Add(1)
				// Bind through the received duplicate; observing the original
				// afterwards proves preparation affected the same socket.
				return unix.Bind(borrowed, &unix.SockaddrInet4{Addr: [4]byte{127, 0, 0, 1}})
			})
			if err != nil {
				t.Fatal(err)
			}
			defer g.Close()
			if ack := request(t, g.Path(), []byte{1}, fd); ack != 0 {
				t.Fatalf("preparation rejected: %d", ack)
			}
			addr, err := unix.Getsockname(fd)
			if err != nil || addr.(*unix.SockaddrInet4).Port == 0 || calls.Load() != 1 {
				t.Fatalf("original socket not prepared: %v, %v; calls %d", addr, err, calls.Load())
			}
			// Preparation is not accepted for an already bound socket.
			if ack := request(t, g.Path(), []byte{1}, fd); ack == 0 || calls.Load() != 1 {
				t.Fatal("bound socket reached preparation callback")
			}
		})
	}
}

func TestGuardDeniesFailuresAndMalformedDescriptors(t *testing.T) {
	var calls atomic.Int32
	g, err := ListenSocketGuard(stateDir(t), func(int) error {
		calls.Add(1)
		return errors.New("physical network has disappeared")
	})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	fd := socket(t, unix.AF_INET, unix.SOCK_DGRAM)
	if request(t, g.Path(), []byte{1}, fd) == 0 || calls.Load() != 1 {
		t.Fatal("callback failure accepted")
	}
	local := socket(t, unix.AF_UNIX, unix.SOCK_DGRAM)
	file, err := os.CreateTemp(stateDir(t), "not-a-socket")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	for _, tc := range []struct {
		payload []byte
		fds     []int
	}{
		{[]byte{1}, nil}, {[]byte{2}, []int{fd}}, {[]byte{1, 2}, []int{fd}},
		{[]byte{1}, []int{fd, fd}}, {[]byte{1}, []int{local}}, {[]byte{1}, []int{int(file.Fd())}},
	} {
		if request(t, g.Path(), tc.payload, tc.fds...) == 0 {
			t.Fatal("malformed request accepted")
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("malformed request reached callback %d times", calls.Load()-1)
	}
}

func TestGuardOwnsOnlyPrivateSocketAndClosesIdleConnections(t *testing.T) {
	dir := stateDir(t)
	if _, err := ListenSocketGuard(dir, nil); err == nil {
		t.Fatal("missing socket preparation accepted")
	}
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := ListenSocketGuard(dir, func(int) error { return nil }); err == nil {
		t.Fatal("public state directory accepted")
	}
	os.Chmod(dir, 0700)
	g, err := ListenSocketGuard(dir, func(int) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	if _, err = ListenSocketGuard(dir, func(int) error { return nil }); err == nil {
		t.Fatal("second guard stole socket")
	}
	c, err := net.Dial("unix", g.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	g.Close()
	c.SetReadDeadline(time.Now().Add(time.Second))
	var b [1]byte
	if _, err = c.Read(b[:]); err == nil {
		t.Fatal("closed guard retained idle connection")
	}
	if _, err = os.Stat(filepath.Join(dir, "socket-guard")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket path not removed: %v", err)
	}
}
