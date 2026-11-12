// Package wire defines the versioned mobile probe contract without database dependencies.
package wire

import "time"

const Version = 1

// Job carries only credentials belonging to the authenticated device.
type Job struct {
	Protocol     string `json:"protocol,omitempty"`
	Transport    string `json:"transport,omitempty"`
	Security     string `json:"security,omitempty"`
	ID           string `json:"job_id"`
	RouteID      string `json:"route_id"`
	InboundID    string `json:"inbound_id"`
	Label        string `json:"label"`
	EntryLabel   string `json:"entry_label,omitempty"`
	Engine       string `json:"engine"`
	Outbound     []byte `json:"outbound,omitempty"`
	AWGConf      string `json:"awg_conf,omitempty"`
	Fingerprint  string `json:"fingerprint"`
	Provisioning bool   `json:"provisioning,omitempty"`
}

type Run struct {
	NetworkType  string    `json:"network_type"`
	Version      int       `json:"version"`
	ID           string    `json:"run_id"`
	ExpiresAt    time.Time `json:"expires_at"`
	ControlURL   string    `json:"control_url"`
	NetworkEpoch string    `json:"network_epoch"`
	Jobs         []Job     `json:"jobs"`
}

type Result struct {
	JobID     string `json:"job_id"`
	Outcome   string `json:"outcome"`
	Reason    string `json:"reason_code,omitempty"`
	ElapsedMS int64  `json:"elapsed_ms"`
}

type Report struct {
	ID            string   `json:"report_id"`
	RunID         string   `json:"run_id"`
	NetworkEpoch  string   `json:"network_epoch"`
	AppVersion    string   `json:"app_version"`
	EngineVersion string   `json:"engine_version"`
	Results       []Result `json:"results"`
}
