//go:build linux && !android

package clienttinc

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestMemoryCredentialsAndPublicStatusAreIndependentCopies(t *testing.T) {
	input := Credentials{Origin: "https://qntw.link/", Token: "test-device-credential-1234567890"}
	c, err := optionCredentials(Options{Credentials: &input})
	if err != nil || c.Origin != "https://qntw.link" || input.Origin != "https://qntw.link/" {
		t.Fatalf("memory validation: %v", err)
	}
	if _, err = optionCredentials(Options{Credentials: &input, CredentialsPath: "/ignored"}); err == nil {
		t.Fatal("ambiguous credential sources accepted")
	}
	var ctl *controller
	ctl = &controller{onStatus: func(s Status) {
		// Callback may safely obtain status and mutate its own snapshot.
		if ctl.get().State != "connected" {
			t.Error("callback ran before status update")
		}
		s.Ingresses[0] = "changed"
	}}
	s := Status{State: "connected", Ingresses: []string{"entry"}}
	ctl.set(s)
	s.Ingresses[0] = "caller-changed"
	got := ctl.get()
	got.Ingresses[0] = "reader-changed"
	if ctl.get().Ingresses[0] != "entry" {
		t.Fatal("status callback or caller mutated live state")
	}
}

func TestForwardingHealthClosesIdleSOCKSConnections(t *testing.T) {
	var requests, active, remoteNames atomic.Int32
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(204) }))
	defer health.Close()
	socks, err := net.Listen("tcp4", "127.0.0.1:1089")
	if err != nil {
		t.Fatal(err)
	}
	defer socks.Close()
	go func() {
		for {
			conn, err := socks.Accept()
			if err != nil {
				return
			}
			active.Add(1)
			go func() {
				defer conn.Close()
				defer active.Add(-1)
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				header := make([]byte, 2)
				if _, err := io.ReadFull(conn, header); err != nil || header[0] != 5 {
					return
				}
				if _, err := io.CopyN(io.Discard, conn, int64(header[1])); err != nil {
					return
				}
				if _, err := conn.Write([]byte{5, 0}); err != nil {
					return
				}
				request := make([]byte, 5)
				if _, err := io.ReadFull(conn, request); err != nil || request[0] != 5 || request[1] != 1 || request[3] != 3 {
					return
				}
				name := make([]byte, int(request[4]))
				if _, err := io.ReadFull(conn, name); err != nil || string(name) != "health.invalid" {
					return
				}
				if _, err := io.CopyN(io.Discard, conn, 2); err != nil {
					return
				}
				remoteNames.Add(1)
				destination, err := net.Dial("tcp", health.Listener.Addr().String())
				if err != nil {
					return
				}
				defer destination.Close()
				if _, err = conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0}); err != nil {
					return
				}
				go func() { _, _ = io.Copy(destination, conn); destination.Close() }()
				_, _ = io.Copy(conn, destination)
			}()
		}
	}()
	for i := 0; i < 30; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err = CheckSOCKS(ctx, "http://health.invalid/")
		cancel()
		if err != nil {
			t.Fatal(err)
		}
	}
	until := time.Now().Add(time.Second)
	for active.Load() != 0 && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	if active.Load() != 0 {
		t.Fatalf("health check leaked %d idle SOCKS connections", active.Load())
	}
	if requests.Load() != 30 || remoteNames.Load() != 30 {
		t.Fatal("health did not forward each request and hostname through SOCKS")
	}
}

func TestRepeatedPrivateControlClosesIdleConnections(t *testing.T) {
	dir, err := os.MkdirTemp("", "qct-control-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	listener, err := net.Listen("unix", filepath.Join(dir, "control.sock"))
	if err != nil {
		t.Fatal(err)
	}
	var active atomic.Int32
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(202) }), ConnState: func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			active.Add(1)
		}
		if state == http.StateClosed {
			active.Add(-1)
		}
	}}
	defer server.Close()
	go server.Serve(listener)
	for i := 0; i < 50; i++ {
		if _, err = Control(context.Background(), dir, "country", "NL"); err != nil {
			t.Fatal(err)
		}
	}
	until := time.Now().Add(time.Second)
	for active.Load() != 0 && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	if active.Load() != 0 {
		t.Fatalf("private control leaked %d idle connections", active.Load())
	}
}
