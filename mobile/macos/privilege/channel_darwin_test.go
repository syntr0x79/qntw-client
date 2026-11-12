//go:build darwin && !ios

package privilege

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestChannelUsesKernelIdentity(t *testing.T) {
	p := filepath.Join(t.TempDir(), "peer")
	l, e := net.ListenUnix("unix", &net.UnixAddr{Name: p, Net: "unix"})
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	ch := make(chan error, 1)
	go func() {
		c, e := net.DialUnix("unix", nil, &net.UnixAddr{Name: p, Net: "unix"})
		if e == nil {
			defer c.Close()
			uid, err := PeerUID(c)
			e = err
			if e == nil && uid != uint32(os.Geteuid()) {
				t.Error("server identity mismatch")
			}
		}
		ch <- e
	}()
	c, e := l.AcceptUnix()
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	uid, e := PeerUID(c)
	if e != nil || uid != uint32(os.Geteuid()) {
		t.Fatalf("peer identity mismatch %v", e)
	}
	if e = <-ch; e != nil {
		t.Fatal(e)
	}
}

func TestStopRetainsTheChannelForCleanupResult(t *testing.T) {
	app, helper := net.Pipe()
	defer app.Close()
	defer helper.Close()
	go requestPeerStop(app)
	line, err := bufio.NewReader(helper).ReadString('\n')
	if err != nil || line != "stop\n" {
		t.Fatalf("stop request: %q %v", line, err)
	}
	result := []byte("{\"status\":\"failed\",\"error\":\"cleanup failed\"}\n")
	go func() { helper.Write(result) }()
	got := make([]byte, len(result))
	if _, err := io.ReadFull(app, got); err != nil || string(got) != string(result) {
		t.Fatalf("cleanup result was lost on cancellation: %q %v", got, err)
	}
}

func TestCancellationLetsThePeerFinishCleanupBeforeLauncherExit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// A harmless local child represents the launcher waiting for its peer.
	cmd := gracefulLauncherCommand(ctx, "/bin/cat")
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	cancel()
	if _, err := input.Write([]byte("network cleanup completed\n")); err != nil {
		t.Fatal(err)
	}
	read := make(chan string, 1)
	go func() { line, _ := bufio.NewReader(output).ReadString('\n'); read <- line }()
	select {
	case line := <-read:
		if line != "network cleanup completed\n" {
			t.Fatalf("launcher killed before cleanup: %q", line)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("launcher did not survive cancellation long enough for cleanup")
	}
	input.Close()
	if err := cmd.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("graceful launcher completion: %v", err)
	}
}
