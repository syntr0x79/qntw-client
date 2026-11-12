package desktopvpn

import (
	"bufio"
	"context"
	"errors"
	"github.com/syntr0x79/qntw-client/mobile/windows/elevation"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

type testChannel struct {
	net.Conn
	done      chan struct{}
	exitError error
}

func (c *testChannel) Done() <-chan struct{} { return c.done }
func (c *testChannel) ExitError() error      { return c.exitError }
func testStart() elevation.Packet {
	return elevation.Packet{Type: "start", Mode: "tinc", Origin: "https://qntw.link", Token: strings.Repeat("a", 32), Country: "NL"}
}
func testBridge(t *testing.T, publish func(elevation.Packet), serve func(net.Conn, *testChannel), input io.Reader) (<-chan error, *testChannel) {
	t.Helper()
	parent, child := net.Pipe()
	channel := &testChannel{Conn: parent, done: make(chan struct{})}
	t.Cleanup(func() { parent.Close(); child.Close() })
	go func() { defer child.Close(); defer close(channel.done); serve(child, channel) }()
	finished := make(chan error, 1)
	go func() {
		finished <- run(context.Background(), testStart(), bufio.NewReader(input), publish, func(context.Context, string) (privateChannel, error) { return channel, nil })
	}()
	return finished, channel
}
func readStart(t *testing.T, c net.Conn) {
	t.Helper()
	p, e := elevation.Read(c)
	if e != nil || p.Type != "start" {
		t.Errorf("start: %v %+v", e, p)
	}
}
func await[T any](t *testing.T, c <-chan T) T {
	t.Helper()
	select {
	case value := <-c:
		return value
	case <-time.After(4 * time.Second):
		t.Fatal("timed out")
		var zero T
		return zero
	}
}
func TestStopKeepsPipeOpenUntilCleanupFailureWasDelivered(t *testing.T) {
	input, controls := io.Pipe()
	defer input.Close()
	defer controls.Close()
	ready := make(chan struct{})
	cleanup := make(chan struct{})
	release := make(chan struct{})
	failure := make(chan elevation.Packet, 1)
	finished, _ := testBridge(t, func(p elevation.Packet) {
		if p.Type == "failed" {
			failure <- p
		}
	}, func(c net.Conn, channel *testChannel) {
		readStart(t, c)
		close(ready)
		p, e := elevation.Read(c)
		if e != nil || p.Type != "stop" {
			t.Errorf("stop: %v %+v", e, p)
			return
		}
		close(cleanup)
		<-release
		channel.exitError = errors.New("cleanup failed")
		if e := (&elevation.Writer{To: c}).Write(elevation.Packet{Type: "failed", Code: "cleanup_failed"}); e != nil {
			t.Error("pipe closed before cleanup result", e)
		}
	}, input)
	await(t, ready)
	if _, e := io.WriteString(controls, "stop\n"); e != nil {
		t.Fatal(e)
	}
	await(t, cleanup)
	select {
	case <-finished:
		t.Fatal("returned before cleanup finished")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if p := await(t, failure); p.Code != "cleanup_failed" {
		t.Fatal(p)
	}
	if e := await(t, finished); e != nil {
		t.Fatal("specific cleanup error already delivered", e)
	}
}
func TestBlockedStatusOutputCannotPreventPrivilegedCleanup(t *testing.T) {
	input, controls := io.Pipe()
	defer input.Close()
	defer controls.Close()
	blocked := make(chan struct{})
	unblock := make(chan struct{})
	cleanup := make(chan struct{})
	var once sync.Once
	finished, _ := testBridge(t, func(elevation.Packet) { once.Do(func() { close(blocked) }); <-unblock }, func(c net.Conn, _ *testChannel) {
		readStart(t, c)
		if e := (&elevation.Writer{To: c}).Write(elevation.Packet{Type: "status"}); e != nil {
			t.Error(e)
			return
		}
		p, e := elevation.Read(c)
		if e != nil || p.Type != "stop" {
			t.Errorf("stop: %v %+v", e, p)
			return
		}
		close(cleanup)
		_ = (&elevation.Writer{To: c}).Write(elevation.Packet{Type: "stopped"})
	}, input)
	await(t, blocked)
	_, _ = io.WriteString(controls, "stop\n")
	await(t, cleanup)
	if e := await(t, finished); e != nil {
		t.Error(e)
	}
	close(unblock)
}
func TestFastStartupFailureIsFlushedBeforeBridgeExit(t *testing.T) {
	input, controls := io.Pipe()
	defer input.Close()
	defer controls.Close()
	failures := make(chan string, 1)
	finished, _ := testBridge(t, func(p elevation.Packet) {
		if p.Type == "failed" {
			failures <- p.Code
		}
	}, func(c net.Conn, channel *testChannel) {
		readStart(t, c)
		channel.exitError = errors.New("setup failure")
		_ = (&elevation.Writer{To: c}).Write(elevation.Packet{Type: "failed", Code: "invalid_profile"})
	}, input)
	if e := await(t, finished); e != nil {
		t.Fatal(e)
	}
	if code := await(t, failures); code != "invalid_profile" {
		t.Fatal(code)
	}
}
func TestChildCrashWithoutFailureFrameIsNotSuccess(t *testing.T) {
	input, controls := io.Pipe()
	defer input.Close()
	defer controls.Close()
	finished, _ := testBridge(t, nil, func(c net.Conn, channel *testChannel) {
		readStart(t, c)
		channel.exitError = errors.New("child crashed")
	}, input)
	if e := await(t, finished); e == nil {
		t.Fatal("crashed child became successful connection")
	}
}
