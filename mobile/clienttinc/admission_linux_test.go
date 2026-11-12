//go:build linux && !android

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

func TestCapacityWaitPrecedesEveryNetworkMutationAndStopCancels(t *testing.T) {
	for _, blocked := range []string{"identity", "country", "config"} {
		t.Run(blocked, func(t *testing.T) {
			state := t.TempDir()
			if err := os.Chmod(state, 0700); err != nil {
				t.Fatal(err)
			}
			native := filepath.Join(state, "native")
			os.Mkdir(native, 0700)
			os.WriteFile(filepath.Join(native, "public-key"), []byte("/YsLMYpYiJIl5dU9irpkQebvuP+0YvrAW8bOiYwsmyH\n"), 0600)
			seen := make(chan Status, 1)
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/client-tinc/v1/"+blocked {
					w.Header().Set("Retry-After", "60")
					w.WriteHeader(503)
					json.NewEncoder(w).Encode(map[string]string{"code": "capacity_exhausted"})
					return
				}
				w.WriteHeader(200)
			}))
			defer api.Close()
			creds := Credentials{Origin: api.URL, Token: "private-test-token-123456", AllowLoopbackHTTP: true}
			// No network tools exist in this PATH. Reaching capacity waiting proves
			// prepareNetwork has not even checked tools, much less changed routes.
			t.Setenv("PATH", t.TempDir())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				done <- Run(ctx, Options{Credentials: &creds, StateDir: state, TincControl: "/absent/tinc", TincDaemon: "/absent/tincd", Country: "PL", OnStatus: func(s Status) {
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
					t.Fatalf("bad capacity state: %+v", s)
				}
			case err := <-done:
				t.Fatalf("network preparation ran before admission: %v", err)
			case <-time.After(3 * time.Second):
				t.Fatal("capacity status absent")
			}
			if _, err := Control(context.Background(), state, "stop", ""); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("stop: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("Stop waited for Retry-After")
			}
		})
	}
}

func TestCapacityDoesNotReplaceLiveGrantAnd402ClosesOnlyUserPath(t *testing.T) {
	overlay, peer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	system, spare, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer system.Close()
	defer spare.Close()
	n := &network{system: &tunDevice{file: system}, firewall: true}
	path := &dataPath{overlay: &tunDevice{file: overlay}}
	original := path
	ttl := time.Now().Add(time.Minute)
	c := &controller{}
	c.set(Status{State: "connected", Country: "PL", DesiredCountry: "PL", ExpiresAt: ttl, Ingresses: []string{"in1"}})
	signature := "same-grant"
	reportAPIError(c, &path, &signature, "PL", APIError{Status: 503, Code: "capacity_exhausted", RetryAfter: 7})
	s := c.get()
	if path != original || signature != "same-grant" || s.State != "connected" || s.Country != "PL" || s.DesiredCountry != "PL" || !s.ExpiresAt.Equal(ttl) || s.Code != "capacity_exhausted" {
		t.Fatalf("capacity changed live grant: %+v", s)
	}
	if _, err := overlay.Stat(); err != nil {
		t.Fatal("capacity closed live path")
	}
	reportAPIError(c, &path, &signature, "PL", APIError{Status: 402})
	s = c.get()
	if path != nil || signature != "" || s.State != "expired" || s.Code != "" || !s.ExpiresAt.Equal(ttl) {
		t.Fatalf("402 did not revoke grant: %+v", s)
	}
	if _, err := overlay.Stat(); err == nil {
		t.Fatal("402 left user path open")
	}
	if _, err := system.Stat(); err != nil || !n.firewall {
		t.Fatal("402 removed fail-closed system tunnel/filter")
	}
	reportAPIError(c, &path, &signature, "PL", APIError{Status: 503, Code: "capacity_exhausted"})
	if c.get().State != "expired" || c.get().Code != "" {
		t.Fatal("capacity hid lease expiry")
	}
}

func TestCapacityRetryRecoversAndClearsReason(t *testing.T) {
	c := &controller{}
	c.set(Status{State: "preparing"})
	calls := 0
	err := awaitAPI(context.Background(), c, func() error {
		calls++
		if calls == 1 {
			return APIError{Status: 503, Code: "capacity_exhausted", RetryAfter: 1}
		}
		return nil
	})
	if err != nil || calls != 2 || c.get().Code != "" || c.get().RetryAfter != 0 {
		t.Fatalf("stale capacity reason: %+v %v", c.get(), err)
	}
}
