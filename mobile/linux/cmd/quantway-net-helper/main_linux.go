//go:build linux

// quantway-net-helper is a one-shot privileged helper. It receives its private
// SOCK_SEQPACKET channel on stdin and never receives subscription credentials.
package main

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/syntr0x79/qntw-client/mobile/linux/netlock"
	"github.com/syntr0x79/qntw-client/mobile/linux/privsep"
	"github.com/syntr0x79/qntw-client/mobile/linux/vpn"
	"golang.org/x/sys/unix"
)

func main() {
	if os.Geteuid() != 0 {
		fail("administrator privileges are required")
	}
	uid, err := strconv.ParseUint(os.Getenv("PKEXEC_UID"), 10, 32)
	if err != nil {
		fail("missing caller identity")
	}
	typ, err := unix.GetsockoptInt(int(os.Stdin.Fd()), unix.SOL_SOCKET, unix.SO_TYPE)
	if err != nil || typ != unix.SOCK_SEQPACKET {
		fail("invalid private channel")
	}
	connection, err := net.FileConn(os.Stdin)
	if err != nil {
		fail("invalid private channel")
	}
	peer, ok := connection.(*net.UnixConn)
	if !ok {
		connection.Close()
		fail("invalid private channel")
	}
	lock, err := netlock.Acquire()
	if err != nil {
		peer.Close()
		fail("another desktop VPN is active or the VPN lock is unavailable")
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	done := make(chan struct{})
	go func() {
		select {
		case <-signals:
			_ = peer.Close()
		case <-done:
		}
	}()
	err = privsep.Serve(peer, uint32(uid), vpn.Resolved{})
	_ = lock.Close()
	close(done)
	signal.Stop(signals)
	if err != nil {
		fail("network setup or cleanup failed")
	}
}
func fail(message string) { fmt.Fprintln(os.Stderr, message); os.Exit(1) }
