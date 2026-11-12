package clienttinc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWindowsAdmissionWaitDoesNotPrepareNetworkAndStopCancels(t *testing.T) {
	for _, blocked := range []string{"identity", "country", "config"} {
		t.Run(blocked, func(t *testing.T) {
			state := t.TempDir()
			native := filepath.Join(state, "native")
			if err := os.MkdirAll(native, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(native, "public-key"), []byte("/YsLMYpYiJIl5dU9irpkQebvuP+0YvrAW8bOiYwsmyH\n"), 0600); err != nil {
				t.Fatal(err)
			}
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/client-tinc/v1/"+blocked {
					w.Header().Set("Retry-After", "60")
					w.WriteHeader(503)
					_ = json.NewEncoder(w).Encode(map[string]string{"code": "capacity_exhausted"})
					return
				}
				w.WriteHeader(200)
			}))
			defer api.Close()
			credentials := Credentials{Origin: api.URL, Token: "windows-fixture-credential-1234", AllowLoopbackHTTP: true}
			seen := make(chan Status, 1)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				done <- Run(ctx, Options{Credentials: &credentials, StateDir: state, TincControl: filepath.Join(state, "absent-tinc.exe"), TincDaemon: filepath.Join(state, "absent-tincd.exe"), Country: "PL", OnStatus: func(s Status) {
					if s.Code == "capacity_exhausted" {
						select {
						case seen <- s:
						default:
						}
					}
				}})
			}()
			select {
			case s := <-seen:
				if s.State != "waiting" || s.RetryAfter != 60 {
					t.Fatalf("bad admission state %+v", s)
				}
			case err := <-done:
				t.Fatalf("reached network setup before admission: %v", err)
			case <-time.After(3 * time.Second):
				t.Fatal("admission state absent")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("bad canceled admission: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("Stop waited for retry delay")
			}
		})
	}
}

func TestWindowsCleanupFailureWaitsForExplicitStop(t *testing.T) {
	controller := &controller{}
	controller.set(Status{State: "connected", Country: "PL"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	failure := errors.New("route cleanup blocked")
	done := make(chan error, 1)
	go func() { done <- waitForExplicitWindowsStop(ctx, controller, failure) }()
	until := time.Now().Add(time.Second)
	for controller.get().Code != "cleanup_failed" && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	if s := controller.get(); s.State != "failed" || s.Code != "cleanup_failed" {
		t.Fatalf("bad fail-closed state %+v", s)
	}
	select {
	case err := <-done:
		t.Fatalf("supervisor returned before explicit Stop: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, failure) {
			t.Fatalf("cleanup error lost: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("explicit Stop did not release supervisor")
	}
}
