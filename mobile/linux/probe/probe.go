// Package probe runs the shared Android probe engine on Linux.
package probe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/syntr0x79/qntw-client/mobile/linux/desktopmeta"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/syntr0x79/qntw-client/internal/mobileprobe/wire"
	"github.com/syntr0x79/qntw-client/mobile/engine"
	"github.com/syntr0x79/qntw-client/mobile/linux/subscription"
)

var tokenPath = regexp.MustCompile(`^/sub/([A-Za-z0-9_-]{16,256})/?$`)

type Result struct {
	JobID      string    `json:"job_id"`
	Label      string    `json:"label"`
	Outcome    string    `json:"outcome"`
	Reason     string    `json:"reason,omitempty"`
	ElapsedMS  int64     `json:"elapsed_ms"`
	Job        *wire.Job `json:"job,omitempty"`
	ControlURL string    `json:"control_url,omitempty"`
}
type Client struct {
	HTTP     *http.Client
	testHost string
	runner   func(string, string, int, engine.Network, string) (string, error)
}

// publicReason keeps diagnostics useful without forwarding engine errors,
// which may contain the control URL or device credentials.
func publicReason(outcome, reason string) string {
	if outcome == "ok" {
		return ""
	}
	switch reason {
	case "cancelled", "network_changed", "configuration_or_network", "request_failed", "tls_verification_failed", "handshake_timeout", "timeout", "http_unauthorized", "http_forbidden", "http_not_found", "http_rate_limited", "http_4xx", "http_5xx", "http_status_error", "connection_refused", "connection_reset", "unexpected_eof", "network_unreachable", "socket_permission", "dns_failed", "socket_binding_failed":
		return reason
	}
	switch outcome {
	case "cancelled":
		return "cancelled"
	case "unprobeable":
		return "configuration_or_network"
	default:
		return "request_failed"
	}
}

func (c Client) Run(ctx context.Context, text string) ([]Result, error) {
	return c.RunWithProgress(ctx, text, nil)
}

// RunWithProgress reports actual completed jobs, serialized across workers.
func (c Client) RunWithProgress(ctx context.Context, text string, progress func(int, int)) ([]Result, error) {
	u, err := url.Parse(strings.TrimSpace(text))
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return nil, errors.New("invalid subscription")
	}
	n, err := currentNetwork()
	if err != nil {
		return nil, err
	}
	epoch := uuid.NewString()
	hc := http.Client{Timeout: 25 * time.Second}
	if c.HTTP != nil {
		hc = *c.HTTP
	}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	var req *http.Request
	var token []string
	if subscription.IsQuantwayHost(u.Hostname()) || (c.testHost != "" && u.Host == c.testHost) {
		token = tokenPath.FindStringSubmatch(u.EscapedPath())
	}
	if token != nil {
		body, _ := json.Marshal(map[string]any{"network_epoch": epoch, "network_type": "other", "engines": []string{"xray", "amneziawg"}})
		endpoint := (&url.URL{Scheme: "https", Host: u.Host, Path: "/api/mobile/v1/probe/runs"}).String()
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err == nil {
			req.Header.Set("Authorization", "Bearer "+token[1])
			req.Header.Set("Content-Type", "application/json")
		}
	} else {
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	}
	if err != nil {
		return nil, errors.New("invalid subscription")
	}
	req.Header.Set("User-Agent", desktopmeta.UserAgent())
	var response *http.Response
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 && req.GetBody != nil {
			req.Body, err = req.GetBody()
			if err != nil {
				return nil, errors.New("cannot retry probe")
			}
		}
		response, err = hc.Do(req)
		if err != nil {
			return nil, errors.New("subscription unavailable")
		}
		if response.StatusCode != http.StatusTooManyRequests || token == nil || attempt == 1 {
			break
		}
		seconds, e := strconv.Atoi(response.Header.Get("Retry-After"))
		if e != nil || seconds < 1 {
			seconds = 5
		}
		if seconds > 60 {
			seconds = 60
		}
		response.Body.Close()
		timer := time.NewTimer(time.Duration(seconds) * time.Second)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("service returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	if err != nil || len(data) > 4<<20 {
		return nil, errors.New("invalid response")
	}
	var run wire.Run
	if token == nil {
		if len(data) > 1<<20 {
			return nil, errors.New("invalid response")
		}
		s, e := engine.ImportSubscription(string(data))
		if e != nil {
			return nil, errors.New("unsupported subscription")
		}
		var imported struct {
			Version    int        `json:"version"`
			Profiles   []wire.Job `json:"profiles"`
			ControlURL string     `json:"control_url"`
		}
		if json.Unmarshal([]byte(s), &imported) != nil {
			return nil, errors.New("invalid response")
		}
		run = wire.Run{Version: imported.Version, Jobs: imported.Profiles, ControlURL: imported.ControlURL, NetworkEpoch: epoch}
	} else if json.Unmarshal(data, &run) != nil {
		return nil, errors.New("invalid response")
	}
	if run.Version != wire.Version || run.NetworkEpoch != epoch || len(run.Jobs) > 512 {
		return nil, errors.New("invalid probe run")
	}
	if _, e := url.ParseRequestURI(run.ControlURL); e != nil || !strings.HasPrefix(run.ControlURL, "https://") {
		return nil, errors.New("invalid control URL")
	}
	for _, job := range run.Jobs {
		if job.ID == "" {
			return nil, errors.New("invalid job")
		}
	}
	execute := c.runner
	if execute == nil {
		execute = engine.ProbeOnNetwork
	}
	results := make([]Result, len(run.Jobs))
	sem := make(chan struct{}, 10)
	var wg sync.WaitGroup
	var progressMu sync.Mutex
	completed := 0
	if progress != nil {
		progress(0, len(run.Jobs))
	}
	for i, job := range run.Jobs {
		wg.Add(1)
		go func(i int, j wire.Job) {
			defer wg.Done()
			defer func() {
				if progress != nil {
					progressMu.Lock()
					completed++
					progress(completed, len(run.Jobs))
					progressMu.Unlock()
				}
			}()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				results[i] = Result{JobID: j.ID, Label: j.Label, Outcome: "cancelled", Reason: "cancelled"}
				return
			}
			defer func() { <-sem }()
			raw, e := json.Marshal(j)
			if e != nil {
				results[i] = Result{JobID: j.ID, Label: j.Label, Outcome: "unprobeable", Reason: "configuration_or_network"}
				return
			}
			r := wire.Result{JobID: j.ID, Outcome: "unprobeable"}
			attempts := 1
			if j.Engine == "amneziawg" && j.Provisioning {
				attempts = 3
			}
			deadline := time.Now().Add(8 * time.Second)
			for attempt := 0; attempt < attempts; attempt++ {
				remaining := time.Until(deadline)
				if remaining <= 0 || ctx.Err() != nil {
					break
				}
				budget := remaining / time.Duration(attempts-attempt)
				output, e := execute(string(raw), run.ControlURL, int(budget.Milliseconds()), n, epoch)
				if e == nil {
					_ = json.Unmarshal([]byte(output), &r)
				}
				if r.Outcome != "fail" {
					break
				}
			}
			current, e := n.snapshot()
			if e != nil || current != n.fingerprint {
				r.Outcome = "cancelled"
				r.Reason = "network_changed"
			}
			results[i] = Result{JobID: j.ID, Label: j.Label, Outcome: r.Outcome, Reason: publicReason(r.Outcome, r.Reason), ElapsedMS: r.ElapsedMS}
			if r.Outcome == "ok" {
				results[i].Job = &j
				results[i].ControlURL = run.ControlURL
			}
		}(i, job)
	}
	wg.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return results, nil
}
