//go:build linux

package probe

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/syntr0x79/qntw-client/mobile/engine"
	"golang.org/x/sys/unix"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestNetworkBindsSocketAndRejectsChangedSnapshot(t *testing.T) {
	n, err := currentNetwork()
	if err != nil {
		t.Skipf("no route: %v", err)
	}
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	if err = n.PrepareSocket(fd); err != nil {
		t.Fatalf("bind to current network: %v", err)
	}
	n.fingerprint = "changed"
	if err = n.PrepareSocket(fd); err == nil {
		t.Fatal("accepted a changed network")
	}
}
func TestRunUsesAuthenticatedEndpoint(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/mobile/v1/probe/runs" || r.Header.Get("Authorization") != "Bearer abcdefghijklmnop" {
			t.Error("wrong probe request")
		}
		var body struct {
			Epoch string `json:"network_epoch"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.Epoch == "" {
			t.Error("missing epoch")
		}
		fmt.Fprintf(w, `{"version":1,"network_epoch":%q,"control_url":"https://example.com/","jobs":[]}`, body.Epoch)
	}))
	defer server.Close()
	results, err := (Client{HTTP: server.Client(), testHost: server.Listener.Addr().String()}).Run(context.Background(), server.URL+"/sub/abcdefghijklmnop")
	if err != nil || len(results) != 0 {
		t.Fatalf("probe failed: %v", err)
	}
}

func TestRunReturnsEveryInvalidRouteWithoutCrashing(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Epoch string `json:"network_epoch"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		jobs := make([]map[string]any, 56)
		for i := range jobs {
			jobs[i] = map[string]any{"job_id": fmt.Sprintf("job-%d", i), "label": fmt.Sprintf("route-%d", i), "engine": "xray", "outbound": "/w=="}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"version": 1, "network_epoch": body.Epoch, "control_url": "https://example.com/", "jobs": jobs})
	}))
	defer server.Close()
	results, err := (Client{HTTP: server.Client(), testHost: server.Listener.Addr().String()}).Run(context.Background(), server.URL+"/sub/abcdefghijklmnop")
	if err != nil || len(results) != 56 {
		t.Fatalf("unexpected results count %d: %v", len(results), err)
	}
	for i, r := range results {
		if r.JobID != fmt.Sprintf("job-%d", i) || r.Outcome != "unprobeable" {
			t.Fatalf("invalid result %d: %+v", i, r)
		}
	}
}

func TestProbeConcurrencyLimit(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Epoch string `json:"network_epoch"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		jobs := make([]map[string]any, 56)
		for i := range jobs {
			jobs[i] = map[string]any{"job_id": fmt.Sprintf("job-%d", i), "engine": "xray"}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"version": 1, "network_epoch": request.Epoch, "control_url": "https://example.com/", "jobs": jobs})
	}))
	defer server.Close()
	var active, peak atomic.Int32
	fake := func(raw, url string, ms int, n engine.Network, epoch string) (string, error) {
		v := active.Add(1)
		for {
			old := peak.Load()
			if v <= old || peak.CompareAndSwap(old, v) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
		active.Add(-1)
		var job struct {
			ID string `json:"job_id"`
		}
		_ = json.Unmarshal([]byte(raw), &job)
		return fmt.Sprintf(`{"job_id":%q,"outcome":"ok","elapsed_ms":10}`, job.ID), nil
	}
	var progress []int
	results, err := (Client{HTTP: server.Client(), testHost: server.Listener.Addr().String(), runner: fake}).RunWithProgress(context.Background(), server.URL+"/sub/abcdefghijklmnop", func(done, total int) {
		if total != 56 {
			t.Errorf("unexpected total %d", total)
		}
		progress = append(progress, done)
	})
	if len(progress) != 57 {
		t.Fatalf("expected initial and 56 completion updates, got %d", len(progress))
	}
	for i, count := range progress {
		if count != i {
			t.Fatalf("out of order progress at %d: %d", i, count)
		}
	}
	if err != nil || len(results) != 56 || peak.Load() > 10 || peak.Load() < 2 {
		t.Fatalf("results=%d peak=%d err=%v", len(results), peak.Load(), err)
	}
	for _, result := range results {
		if result.Outcome != "ok" {
			t.Fatalf("unexpected outcome: %s", result.Outcome)
		}
	}
}

func TestRunRetriesServerCooldown(t *testing.T) {
	attempts := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		var body struct {
			Epoch string `json:"network_epoch"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.Epoch == "" {
			t.Error("retry lost request body")
		}
		fmt.Fprintf(w, `{"version":1,"network_epoch":%q,"control_url":"https://example.com/","jobs":[]}`, body.Epoch)
	}))
	defer server.Close()
	_, err := (Client{HTTP: server.Client(), testHost: server.Listener.Addr().String()}).Run(context.Background(), server.URL+"/sub/abcdefghijklmnop")
	if err != nil || attempts != 2 {
		t.Fatalf("retry failed: attempts=%d err=%v", attempts, err)
	}
}
