//go:build ignore

// Disposable namespace fixture launcher. It uses the real production SocketGuard
// and raw-IP TUN descriptor; it is never installed as an application helper.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/syntr0x79/qntw-client/mobile/tincbridge"
	"golang.org/x/sys/unix"
)

func main() {
	daemon := flag.String("daemon", "", "guarded tincd")
	dir := flag.String("config", "", "private fixture config")
	physical := flag.String("physical", "eth0", "fixture physical interface")
	maxseg := flag.Int("maxseg", 0, "optional fixture TCP MSS counterfactual")
	flag.Parse()
	if err := run(*daemon, *dir, *physical, *maxseg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(daemon, dir, physical string, maxseg int) error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	fd, err := unix.Open("/dev/net/tun", unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	packet := os.NewFile(uintptr(fd), "fixture-tun")
	defer packet.Close()
	ifr, err := unix.NewIfreq("qtm0")
	if err != nil {
		return err
	}
	ifr.SetUint16(unix.IFF_TUN | unix.IFF_NO_PI)
	if err = unix.IoctlIfreq(fd, unix.TUNSETIFF, ifr); err != nil {
		return err
	}
	var tcp, udp atomic.Int64
	guard, err := tincbridge.ListenSocketGuard(dir, func(fd int) error {
		kind, e := unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_TYPE)
		if e != nil {
			return e
		}
		if e = unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_MARK, 0x7133); e != nil {
			return e
		}
		if e = unix.SetsockoptString(fd, unix.SOL_SOCKET, unix.SO_BINDTODEVICE, physical); e != nil {
			return e
		}
		if maxseg > 0 && kind == unix.SOCK_STREAM {
			if e = unix.SetsockoptInt(fd, unix.IPPROTO_TCP, unix.TCP_MAXSEG, maxseg); e != nil {
				return e
			}
		}
		if kind == unix.SOCK_STREAM {
			tcp.Add(1)
		} else {
			udp.Add(1)
		}
		return nil
	})
	if err != nil {
		return err
	}
	defer guard.Close()
	// Protocol and traffic dumps print every packet and distort bulk timings;
	// SPTPS failures are DEBUG_ALWAYS, and negotiated state is read via ctl.
	cmd := exec.Command(daemon, "-D", "-d3", "-c", dir, "--pidfile="+filepath.Join(dir, "pid"))
	cmd.Env = append(os.Environ(), tincbridge.GuardEnvironment+"="+guard.Path())
	cmd.ExtraFiles = []*os.File{packet}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err = cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case err = <-done:
			return err
		case <-ticker.C:
			b, _ := json.Marshal(map[string]any{"fixture_guard_tcp": tcp.Load(), "fixture_guard_udp": udp.Load()})
			fmt.Println(string(b))
		case <-ctx.Done():
			cmd.Process.Signal(syscall.SIGTERM)
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				cmd.Process.Kill()
				<-done
			}
			return nil
		}
	}
}
