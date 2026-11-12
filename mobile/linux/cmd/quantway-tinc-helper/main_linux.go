//go:build linux && !android

// quantway-tinc-helper is the dedicated root transport for desktop tinc. It
// accepts credentials only on an inherited, peer-checked private packet socket.
package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/syntr0x79/qntw-client/mobile/linux/tincvpn"
	"golang.org/x/sys/unix"
)

func main() {
	if run() != nil {
		fmt.Fprintln(os.Stderr, "Tinc administrator helper failed")
		os.Exit(1)
	}
}
func run() error {
	if os.Geteuid() != 0 || len(os.Args) != 1 {
		return errors.New("invalid helper invocation")
	}
	uid, err := strconv.ParseUint(os.Getenv("PKEXEC_UID"), 10, 32)
	if err != nil {
		return errors.New("missing caller identity")
	}
	typ, err := unix.GetsockoptInt(int(os.Stdin.Fd()), unix.SOL_SOCKET, unix.SO_TYPE)
	if err != nil || typ != unix.SOCK_SEQPACKET {
		return errors.New("invalid private channel")
	}
	connection, err := net.FileConn(os.Stdin)
	if err != nil {
		return errors.New("invalid private channel")
	}
	defer connection.Close()
	peer, ok := connection.(*net.UnixConn)
	if !ok {
		return errors.New("invalid private channel")
	}
	// pkexec normally sanitizes the environment; define it explicitly before
	// any native or system command so caller PATH/loader settings cannot apply.
	os.Clearenv()
	_ = os.Setenv("PATH", "/usr/sbin:/usr/bin:/sbin:/bin")
	_ = os.Setenv("HOME", "/root")
	unix.Umask(0077)
	if os.Chdir("/") != nil {
		return errors.New("invalid helper directory")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	defer cancel()
	return tincvpn.Serve(ctx, peer, uint32(uid))
}
