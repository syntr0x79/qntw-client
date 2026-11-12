package probe

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/syntr0x79/qntw-client/internal/mobileprobe/wire"
	"github.com/syntr0x79/qntw-client/mobile/engine"
)

// The server and runner are local fixtures: no VPN socket or external request
// is opened. Only the physical-network snapshot is read by the real client.
func reasonClient(t *testing.T, jobs []wire.Job) (Client, string) {
	t.Helper()
	if _, err := currentNetwork(); err != nil {
		t.Skipf("no physical-network snapshot: %v", err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Epoch string `json:"network_epoch"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			http.Error(w, "invalid fixture request", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(wire.Run{Version: wire.Version, NetworkEpoch: request.Epoch, Jobs: jobs, ControlURL: "https://control.invalid/"})
	}))
	t.Cleanup(server.Close)
	return Client{HTTP: server.Client(), testHost: server.Listener.Addr().String()}, server.URL + "/sub/abcdefghijklmnop"
}

func TestProbePublicReasonsRoundTrip(t *testing.T) {
	const privateError = "fetch https://secret.invalid/sub/private-token: peer private-key"
	cases := []struct {
		id, outcome, engineReason, publicReason string
		engineError                             bool
	}{
		{"tls", "fail", "tls_verification_failed", "tls_verification_failed", false},
		{"handshake", "fail", "handshake_timeout", "handshake_timeout", false},
		{"timeout", "fail", "timeout", "timeout", false},
		{"request", "fail", "request_failed", "request_failed", false},
		{"configuration", "unprobeable", "configuration_or_network", "configuration_or_network", false},
		{"cancelled", "cancelled", "cancelled", "cancelled", false},
		{"http_unauthorized", "fail", "http_unauthorized", "http_unauthorized", false},
		{"http_forbidden", "fail", "http_forbidden", "http_forbidden", false},
		{"http_not_found", "fail", "http_not_found", "http_not_found", false},
		{"http_rate_limited", "fail", "http_rate_limited", "http_rate_limited", false},
		{"http_4xx", "fail", "http_4xx", "http_4xx", false},
		{"http_5xx", "fail", "http_5xx", "http_5xx", false},
		{"http_status_error", "fail", "http_status_error", "http_status_error", false},
		{"connection_refused", "fail", "connection_refused", "connection_refused", false},
		{"connection_reset", "fail", "connection_reset", "connection_reset", false},
		{"unexpected_eof", "fail", "unexpected_eof", "unexpected_eof", false},
		{"network_unreachable", "fail", "network_unreachable", "network_unreachable", false},
		{"socket_permission", "fail", "socket_permission", "socket_permission", false},
		{"dns_failed", "fail", "dns_failed", "dns_failed", false},
		{"socket_binding_failed", "fail", "socket_binding_failed", "socket_binding_failed", false},
		{"unknown-reason", "fail", privateError, "request_failed", false},
		{"missing-reason", "unprobeable", "", "configuration_or_network", false},
		{"engine-error", "unprobeable", "", "configuration_or_network", true},
		{"success", "ok", privateError, "", false},
	}
	jobs := make([]wire.Job, len(cases))
	for i, c := range cases {
		jobs[i] = wire.Job{ID: c.id, Label: "Route " + c.id, Engine: "xray"}
	}
	client, link := reasonClient(t, jobs)
	client.runner = func(raw, _ string, _ int, _ engine.Network, _ string) (string, error) {
		var job wire.Job
		if err := json.Unmarshal([]byte(raw), &job); err != nil {
			return "", err
		}
		for _, c := range cases {
			if c.id != job.ID {
				continue
			}
			if c.engineError {
				return "", errors.New(privateError)
			}
			result, err := json.Marshal(wire.Result{JobID: job.ID, Outcome: c.outcome, Reason: c.engineReason, ElapsedMS: 12})
			return string(result), err
		}
		return "", errors.New("missing fixture")
	}
	results, err := client.Run(context.Background(), link)
	if err != nil || len(results) != len(cases) {
		t.Fatalf("results=%d err=%v", len(results), err)
	}
	for i, result := range results {
		c := cases[i]
		if result.JobID != c.id || result.Label != jobs[i].Label || result.Outcome != c.outcome || result.Reason != c.publicReason {
			t.Errorf("result %s: %+v", c.id, result)
		}
		if !c.engineError && result.ElapsedMS != 12 {
			t.Errorf("elapsed time lost for %s", c.id)
		}
		if c.outcome == "ok" && (result.Job == nil || result.ControlURL != "https://control.invalid/") {
			t.Error("successful route lost its connection fields")
		}
		data, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "private-token") || strings.Contains(string(data), "private-key") {
			t.Errorf("private engine diagnostic escaped for %s", c.id)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			t.Fatal(err)
		}
		_, hasReason := fields["reason"]
		if hasReason != (c.publicReason != "") {
			t.Errorf("reason JSON contract for %s: %s", c.id, data)
		}
	}
}

func TestProbeNetworkChangeHasDistinctReason(t *testing.T) {
	client, link := reasonClient(t, []wire.Job{{ID: "route", Engine: "xray"}})
	client.runner = func(_, _ string, _ int, n engine.Network, _ string) (string, error) {
		n.(*network).fingerprint = "fixture-network-change"
		return `{"job_id":"route","outcome":"ok","elapsed_ms":12}`, nil
	}
	results, err := client.Run(context.Background(), link)
	if err != nil || len(results) != 1 {
		t.Fatalf("results=%d err=%v", len(results), err)
	}
	result := results[0]
	if result.Outcome != "cancelled" || result.Reason != "network_changed" || result.Job != nil || result.ControlURL != "" {
		t.Fatalf("network change exposed a usable route or lost its reason: %+v", result)
	}
}
