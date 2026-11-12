//go:build linux || darwin

package tincbridge

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// This test uses actual native daemons and the packet-FD adapter, without root,
// a kernel TUN or any current app code. The intermediate nodes are servers in
// this transport fixture; it does not authorize clients to relay other users.
func TestNativeTincPacketFDThroughThreeServerLinks(t *testing.T) {
	daemon, ctl := os.Getenv("CLIENT_TINC_DAEMON"), os.Getenv("CLIENT_TINC_CTL")
	if daemon == "" || ctl == "" {
		t.Skip("set CLIENT_TINC_DAEMON and CLIENT_TINC_CTL to guarded client-build binaries")
	}
	type peer struct {
		name, dir, host string
		port            int
		ip              netip.Addr
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	var peers []peer
	var reserved []net.Listener
	for i := 0; i < 4; i++ {
		p := peer{name: fmt.Sprintf("server%d", i), dir: stateDir(t), ip: netip.MustParseAddr(fmt.Sprintf("fd71:44::%d", i+1))}
		cmd := exec.CommandContext(ctx, ctl, "-c", p.dir, "init", p.name)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("native identity generation: %v: %s", err, out)
		}
		// Newer tinc init normalizes config directories to 0755. The app
		// owns the private state and must restore it before starting its broker.
		if err := os.Chmod(p.dir, 0700); err != nil {
			t.Fatal(err)
		}
		keyFile, err := os.ReadFile(filepath.Join(p.dir, "hosts", p.name))
		if err != nil {
			t.Fatal(err)
		}
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		reserved = append(reserved, listener)
		p.port = listener.Addr().(*net.TCPAddr).Port
		p.host = fmt.Sprintf("%s\nAddress = 127.0.0.1\nPort = %d\nSubnet = %s/128\nIndirectData = yes\n", keyFile, p.port, p.ip)
		peers = append(peers, p)
	}
	for _, l := range reserved {
		l.Close()
	}
	var endpoints []*os.File
	var prepared atomic.Int32
	for i, p := range peers {
		packet, app := packetPair(t)
		endpoints = append(endpoints, app)
		conf := fmt.Sprintf("Name = %s\nDeviceType = fd\nDevice = 3\nMode = router\nAddressFamily = ipv4\nBindToAddress = 127.0.0.1\nPort = %d\nExperimentalProtocol = yes\nStrictSubnets = yes\nAutoConnect = no\nBroadcast = no\nLocalDiscovery = no\nTCPOnly = yes\nPingInterval = 2\nPingTimeout = 3\nMaxTimeout = 2\n", p.name, p.port)
		if i+1 < len(peers) {
			conf += "ConnectTo = " + peers[i+1].name + "\n"
		}
		for _, other := range peers {
			if err := os.WriteFile(filepath.Join(p.dir, "hosts", other.name), []byte(other.host), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(p.dir, "tinc.conf"), []byte(conf), 0600); err != nil {
			t.Fatal(err)
		}
		proc, err := Start(ctx, ProcessOptions{Binary: daemon, ConfigDir: p.dir, Packet: packet, ExpiresAt: time.Now().Add(time.Minute), PrepareSocket: func(fd int) error {
			prepared.Add(1)
			if path := os.Getenv("CLIENT_TINC_ANDROID_GUARD"); path != "" {
				return androidSocketPreparation(path, fd)
			}
			return nil
		}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			proc.Close()
			if t.Failed() {
				b, _ := os.ReadFile(filepath.Join(p.dir, "tinc.log"))
				t.Logf("%s: %s", p.name, b)
			}
		})
	}
	packet := func(source, dest netip.Addr, body []byte) []byte {
		b := make([]byte, 40+len(body))
		b[0], b[6], b[7] = 0x60, 59, 64
		binary.BigEndian.PutUint16(b[4:6], uint16(len(body)))
		a, z := source.As16(), dest.As16()
		copy(b[8:24], a[:])
		copy(b[24:40], z[:])
		copy(b[40:], body)
		return b
	}
	for _, direction := range [][2]int{{0, 3}, {3, 0}} {
		from, to := direction[0], direction[1]
		body := []byte(fmt.Sprintf("client-tinc-fd-proof-%d-%d", from, to))
		sent := packet(peers[from].ip, peers[to].ip, body)
		buf := make([]byte, 4096)
		arrived := false
		for ctx.Err() == nil {
			if _, err := endpoints[from].Write(sent); err != nil {
				t.Fatal(err)
			}
			endpoints[to].SetReadDeadline(time.Now().Add(300 * time.Millisecond))
			n, err := endpoints[to].Read(buf)
			if err == nil && bytes.Equal(buf[:n], sent) {
				arrived = true
				break
			}
		}
		if !arrived {
			t.Fatalf("native packet did not cross three links %d -> %d; prepare calls %d", from, to, prepared.Load())
		}
	}
	if prepared.Load() < 4 {
		t.Fatalf("native Internet sockets bypassed guard: %d callbacks", prepared.Load())
	}
}

// The isolated Android instrumentation owns a real VpnService and selected
// physical Network. Its Java broker performs protect + bindSocket, after this
// Go test receives the duplicate from native tinc. No callback is simulated
// when CLIENT_TINC_ANDROID_GUARD is set by that harness.
func androidSocketPreparation(path string, fd int) error {
	c, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	if _, _, err = c.WriteMsgUnix([]byte{1}, unix.UnixRights(fd), nil); err != nil {
		return err
	}
	var ack [1]byte
	if _, err = io.ReadFull(c, ack[:]); err != nil {
		return err
	}
	if ack[0] != 0 {
		return errors.New("Android rejected physical network preparation")
	}
	return nil
}
