//go:build darwin && !ios

package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCountryCommandAddressesOnlyOwnedControllerAndKeepsSessionUntilStop(t *testing.T) {
	dir, err := os.MkdirTemp("/private/tmp", "qwc-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	listener, err := net.Listen("unix", filepath.Join(dir, "control.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	wanted := make(chan string, 1)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/country" {
			t.Error("command escaped owned country endpoint")
			w.WriteHeader(400)
			return
		}
		var request struct {
			Country string `json:"country"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			t.Error("invalid country body")
		}
		wanted <- request.Country
		w.WriteHeader(202)
	})}
	go server.Serve(listener)
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var events []any
	consumeCommands(ctx, strings.NewReader("{\"action\":\"country\",\"country\":\"SE\"}\nstop\n"), func() string { return dir }, false, cancel, func(event any) { events = append(events, event) })
	select {
	case country := <-wanted:
		if country != "SE" {
			t.Fatalf("country changed unexpectedly: %s", country)
		}
	case <-time.After(time.Second):
		t.Fatal("country command did not reach the existing controller")
	}
	if ctx.Err() == nil || len(events) != 1 {
		t.Fatal("stop did not finish the session after country request")
	}
}

func TestDiagnosticCollectionIsExplicitAndCountryRequiresTinc(t *testing.T) {
	for _, tc := range []struct {
		command string
		dir     string
	}{
		{command: "{\"action\":\"collect\"}\n", dir: "/not-used"},
		{command: "{\"action\":\"country\",\"country\":\"SE\"}\n"},
	} {
		ctx, cancel := context.WithCancel(context.Background())
		count := 0
		consumeCommands(ctx, strings.NewReader(tc.command), func() string { return tc.dir }, false, cancel, func(any) { count++ })
		if ctx.Err() == nil || count != 0 {
			t.Fatal("unsupported command did not close the session without executing diagnostics")
		}
	}
}

func TestStopDoesNotWaitForAdmissionOrAccessAControlDirectory(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	consumeCommands(ctx, strings.NewReader("stop\n"), func() string {
		t.Fatal("stop attempted to access an unready control directory")
		return ""
	}, false, cancel, func(any) { t.Fatal("stop emitted a control event") })
	if ctx.Err() == nil {
		t.Fatal("stop did not cancel pending admission")
	}
}
