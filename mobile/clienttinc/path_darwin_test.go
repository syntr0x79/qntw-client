//go:build darwin && !ios

package clienttinc

import (
	"bytes"
	"context"
	"encoding/binary"
	_ "github.com/syntr0x79/qntw-client/mobile/engine"
	"github.com/syntr0x79/qntw-client/mobile/tincbridge"
	"golang.org/x/sys/unix"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestDarwinPacketBridgePreservesDatagramsAndAddressFamily(t *testing.T) {
	a, e := unix.Socketpair(unix.AF_UNIX, unix.SOCK_DGRAM, 0)
	if e != nil {
		t.Fatal(e)
	}
	b, e := unix.Socketpair(unix.AF_UNIX, unix.SOCK_DGRAM, 0)
	if e != nil {
		t.Fatal(e)
	}
	for _, fd := range append(a[:], b[:]...) {
		unix.SetNonblock(fd, true)
	}
	files := []*os.File{os.NewFile(uintptr(a[0]), "utun"), os.NewFile(uintptr(a[1]), "kernel"), os.NewFile(uintptr(b[0]), "bridge"), os.NewFile(uintptr(b[1]), "native")}
	for _, f := range files {
		defer f.Close()
	}
	go bridgeUTUN(files[0], files[2])
	for _, version := range []byte{4, 6} {
		payload := []byte{version << 4, 1, 2, 3, 4, 5}
		header := make([]byte, 4)
		family := uint32(unix.AF_INET)
		if version == 6 {
			family = unix.AF_INET6
		}
		binary.BigEndian.PutUint32(header, family)
		files[3].SetReadDeadline(time.Now().Add(time.Second))
		if _, e = files[1].Write(append(header, payload...)); e != nil {
			t.Fatal(e)
		}
		buf := make([]byte, 128)
		n, e := files[3].Read(buf)
		if e != nil || !bytes.Equal(buf[:n], payload) {
			t.Fatalf("raw native packet damaged: %v", e)
		}
		files[1].SetReadDeadline(time.Now().Add(time.Second))
		if _, e = files[3].Write(payload); e != nil {
			t.Fatal(e)
		}
		n, e = files[1].Read(buf)
		if e != nil || !bytes.Equal(buf[:n], append(header, payload...)) {
			t.Fatalf("utun frame damaged: %v", e)
		}
	}
}
func TestDarwinNativeIdentityIsStableAndDoesNotRotate(t *testing.T) {
	ctl := os.Getenv("QUANTWAY_TEST_TINC")
	if ctl == "" {
		t.Skip("set QUANTWAY_TEST_TINC to the bundled native control binary")
	}
	dir := filepath.Join(t.TempDir(), "native")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first, e := identity(ctx, ctl, dir)
	if e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{dir, filepath.Join(dir, "hosts")} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0700 {
			t.Fatalf("native init exposed identity directory: %s (%v)", path, err)
		}
	}
	second, e := identity(ctx, ctl, dir)
	if e != nil || first != second {
		t.Fatal("identity rotated")
	}
	if e = os.Remove(filepath.Join(dir, "public-key")); e != nil {
		t.Fatal(e)
	}
	if _, e = identity(ctx, ctl, dir); e == nil {
		t.Fatal("orphaned identity was silently rotated")
	}
}

func TestDarwinNativeDaemonUsesPacketFDAndNeverExecutesHooks(t *testing.T) {
	ctl := os.Getenv("QUANTWAY_TEST_TINC")
	if ctl == "" {
		t.Skip("set QUANTWAY_TEST_TINC")
	}
	dir, e := os.MkdirTemp("/private/tmp", "qtn-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	public, e := identity(ctx, ctl, dir)
	if e != nil {
		t.Fatal(e)
	}
	config := configFixture(t)
	config.Ingresses = nil
	if e = renderNative(dir, public, config); e != nil {
		t.Fatal(e)
	}
	marker := filepath.Join(dir, "hook-ran")
	if e = os.WriteFile(filepath.Join(dir, "tinc-up"), []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0700); e != nil {
		t.Fatal(e)
	}
	pair, e := unix.Socketpair(unix.AF_UNIX, unix.SOCK_DGRAM, 0)
	if e != nil {
		t.Fatal(e)
	}
	defer unix.Close(pair[0])
	f := os.NewFile(uintptr(pair[1]), "packet-fd")
	defer f.Close()
	guard, e := tincbridge.ListenSocketGuard(dir, func(int) error { return nil })
	if e != nil {
		t.Fatal(e)
	}
	defer guard.Close()
	cmd := exec.CommandContext(ctx, filepath.Join(filepath.Dir(ctl), "tincd"), "-D", "-c", dir, "--pidfile="+filepath.Join(dir, "tinc.pid"), "--logfile="+filepath.Join(dir, "tinc.log"))
	cmd.ExtraFiles = []*os.File{f}
	cmd.Env = []string{"PATH=/usr/bin:/bin", tincbridge.GuardEnvironment + "=" + guard.Path()}
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer func() {
		cmd.Process.Signal(unix.SIGTERM)
		select {
		case <-done:
		case <-time.After(time.Second):
			cmd.Process.Kill()
		}
	}()
	deadline := time.Now().Add(3 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		b, err := command(ctx, "", ctl, "-c", dir, "--pidfile="+filepath.Join(dir, "tinc.pid"), "dump", "connections")
		if err == nil {
			_ = b
			ready = true
			break
		}
		select {
		case err := <-done:
			t.Fatalf("native daemon stopped: %v", err)
		default:
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !ready {
		t.Fatal("native packet-FD daemon did not start")
	}
	if _, e = os.Stat(marker); !os.IsNotExist(e) {
		t.Fatal("privileged native hook was executed")
	}
}
