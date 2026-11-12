//go:build darwin && !ios

package privilege

import (
	"context"
	"encoding/json"
	"errors"
	"golang.org/x/sys/unix"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func PeerUID(c *net.UnixConn) (uint32, error) {
	r, e := c.SyscallConn()
	if e != nil {
		return 0, e
	}
	var uid uint32
	var inner error
	e = r.Control(func(fd uintptr) {
		v, err := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		inner = err
		if err == nil {
			uid = v.Uid
		}
	})
	if e != nil {
		return 0, e
	}
	return uid, inner
}

// Secrets travel only over the private, kernel-authenticated Unix channel.
func Run(ctx context.Context, request any, commands io.Reader, publish func(json.RawMessage)) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	exe, e := os.Executable()
	if e != nil {
		return e
	}
	helper := filepath.Join(filepath.Dir(exe), "quantway-vpn-helper")
	dir, e := os.MkdirTemp("/private/tmp", "qw-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "session.sock")
	l, e := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if e != nil {
		return e
	}
	defer l.Close()
	stopAccept := context.AfterFunc(ctx, func() { l.Close() })
	defer stopAccept()
	// A stop/EOF must cancel authorization too, before the elevated peer exists.
	// Other bounded commands wait for the authenticated channel instead of
	// implicitly terminating this process and replacing the system tunnel.
	commandQueue := make(chan SessionCommand, 4)
	go func() {
		_ = ReadSessionCommands(commands, func(command SessionCommand) error {
			if command.Action == "stop" {
				cancel()
				return nil
			}
			select {
			case commandQueue <- command:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			default:
				return errors.New("too many pending session commands")
			}
		})
		cancel()
	}()
	os.Chmod(socket, 0600)
	script := `on run argv
 do shell script "exec " & quoted form of (item 1 of argv) & " " & quoted form of (item 2 of argv) with administrator privileges
end run`
	cmd := gracefulLauncherCommand(ctx, "/usr/bin/osascript", "-e", script, helper, socket)
	if e = cmd.Start(); e != nil {
		return e
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait(); l.Close() }()
	conn, e := l.AcceptUnix()
	if e != nil {
		if ctx.Err() != nil {
			<-done
			return nil
		}
		return errors.New("Разрешение администратора не получено.")
	}
	defer conn.Close()
	uid, e := PeerUID(conn)
	if e != nil || uid != 0 {
		return errors.New("invalid helper identity")
	}
	if e = json.NewEncoder(conn).Encode(request); e != nil {
		return e
	}
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			if ctx.Err() != nil {
				requestPeerStop(conn)
				return
			}
			select {
			case <-ctx.Done():
				requestPeerStop(conn)
				return
			case command := <-commandQueue:
				if ctx.Err() != nil {
					requestPeerStop(conn)
					return
				}
				conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
				if json.NewEncoder(conn).Encode(command) != nil {
					cancel()
					return
				}
			case <-stop:
				return
			}
		}
	}()

	dec := json.NewDecoder(conn)
	for {
		var v json.RawMessage
		if e = dec.Decode(&v); e != nil {
			break
		}
		publish(v)
	}
	select {
	case e = <-done:
	case <-time.After(15 * time.Second):
		cmd.Process.Kill()
		return errors.New("Не удалось завершить сетевой помощник.")
	}
	if ctx.Err() != nil {
		return nil
	}
	return e
}

func requestPeerStop(conn net.Conn) {
	// Keep the response half open: cleanup failures must still reach the app.
	// The elevated reader cancels its session on this line or on EOF.
	conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.WriteString(conn, "stop\n"); err != nil {
		conn.Close()
	}
}

func gracefulLauncherCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	// Cancellation closes the authenticated channel. Let the elevated peer
	// restore its network before terminating the launcher. This also bounds
	// cancellation of an authorization prompt which never produced a peer.
	cmd.Cancel = func() error { return nil }
	cmd.WaitDelay = 15 * time.Second
	return cmd
}
