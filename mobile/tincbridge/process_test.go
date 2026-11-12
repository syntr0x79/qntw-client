//go:build linux || darwin

package tincbridge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func packetPair(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_DGRAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, fd := range fds {
		unix.CloseOnExec(fd)
		if err = unix.SetNonblock(fd, true); err != nil {
			t.Fatal(err)
		}
	}
	a, b := os.NewFile(uintptr(fds[0]), "native-packets"), os.NewFile(uintptr(fds[1]), "app-packets")
	t.Cleanup(func() { a.Close(); b.Close() })
	return a, b
}

func TestProcessLeaseAndCancellationStopChildAndReleaseGuard(t *testing.T) {
	for _, reason := range []string{"cancel", "expire"} {
		t.Run(reason, func(t *testing.T) {
			dir := stateDir(t)
			binary := filepath.Join(dir, "native-fixture")
			if runtime.GOOS == "android" {
				binary = os.Getenv("CLIENT_TINC_FIXTURE_BIN")
				if binary == "" {
					t.Skip("Android lifecycle fixture must be packaged in nativeLibraryDir")
				}
			} else {
				if err := os.WriteFile(binary, []byte("#!/bin/sh\nexec sleep 60\n"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			packet, app := packetPair(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			expires := time.Now().Add(time.Minute)
			if reason == "expire" {
				expires = time.Now().Add(250 * time.Millisecond)
			}
			p, err := Start(ctx, ProcessOptions{Binary: binary, ConfigDir: dir, Packet: packet, ExpiresAt: expires, PrepareSocket: func(int) error { return nil }})
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			if reason == "cancel" {
				cancel()
			}
			select {
			case <-p.done:
			case <-time.After(4 * time.Second):
				t.Fatal("native child outlived cancellation/lease")
			}
			if p.Running() {
				t.Fatal("exited child reported running")
			}
			if _, err = os.Stat(filepath.Join(dir, "socket-guard")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("guard retained after child exit: %v", err)
			}
			// Start/Close only lend fd 3; the caller's descriptors remain usable.
			if _, err = packet.Write([]byte("still owned by caller")); err != nil {
				t.Fatal(err)
			}
			app.SetReadDeadline(time.Now().Add(time.Second))
			if _, err = app.Read(make([]byte, 64)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestFailedStartReleasesGuard(t *testing.T) {
	dir := stateDir(t)
	packet, _ := packetPair(t)
	if _, err := Start(context.Background(), ProcessOptions{Binary: filepath.Join(dir, "missing-tinc"), ConfigDir: dir, Packet: packet, ExpiresAt: time.Now().Add(time.Minute), PrepareSocket: func(int) error { return nil }}); err == nil {
		t.Fatal("missing binary accepted")
	}
	g, err := ListenSocketGuard(dir, func(int) error { return nil })
	if err != nil {
		t.Fatalf("failed start leaked socket guard: %v", err)
	}
	g.Close()
}
