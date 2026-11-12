package clienttinc

import "time"

type Status struct {
	State             string      `json:"state"`
	Country           string      `json:"country,omitempty"`
	DesiredCountry    string      `json:"desired_country,omitempty"`
	EgressID          string      `json:"egress_id,omitempty"`
	AssignmentVersion uint64      `json:"assignment_version,omitempty"`
	ExpiresAt         time.Time   `json:"expires_at,omitempty"`
	Ingresses         []string    `json:"connected_ingresses,omitempty"`
	Message           string      `json:"message,omitempty"`
	Code              string      `json:"code,omitempty"`
	RetryAfter        int         `json:"retry_after,omitempty"`
	Mesh              *MeshStatus `json:"mesh,omitempty"`
}

// MeshStatus separates observed direct metadata connections from assignment
// intent. It deliberately contains no inferred server-core packet path.
type MeshStatus struct {
	ObservationState string        `json:"observation_state"`
	ObservedAt       time.Time     `json:"observed_at"`
	Ingresses        []MeshIngress `json:"ingresses,omitempty"`
	Egress           MeshEgress    `json:"egress"`
	Traffic          *MeshTraffic  `json:"traffic,omitempty"`
}

type MeshIngress struct {
	Name      string `json:"name"`
	Connected *bool  `json:"connected,omitempty"` // nil means observation unavailable.
}

type MeshEgress struct {
	ID      string `json:"id"`
	Country string `json:"country"`
	Name    string `json:"name,omitempty"` // Not inferred from TLS SNI or an address.
}

// MeshTraffic counts IP packet bytes at the owned system TUN for this helper
// session, including protocol headers. These are not delivery/billing counters
// or per-exit totals, and remain cumulative across country changes.
type MeshTraffic struct {
	Source    string    `json:"source"`
	RXBytes   uint64    `json:"rx_bytes"`
	TXBytes   uint64    `json:"tx_bytes"`
	Epoch     string    `json:"epoch"`
	SampledAt time.Time `json:"sampled_at"`
}

func cloneMesh(m *MeshStatus) *MeshStatus {
	if m == nil {
		return nil
	}
	copy := *m
	copy.Ingresses = append([]MeshIngress(nil), m.Ingresses...)
	for i := range copy.Ingresses {
		if v := copy.Ingresses[i].Connected; v != nil {
			value := *v
			copy.Ingresses[i].Connected = &value
		}
	}
	if m.Traffic != nil {
		value := *m.Traffic
		copy.Traffic = &value
	}
	return &copy
}

// CloneStatus copies every mutable public telemetry field across API/IPC owners.
func CloneStatus(s Status) Status {
	s.Ingresses = append([]string(nil), s.Ingresses...)
	s.Mesh = cloneMesh(s.Mesh)
	return s
}
func cloneStatus(s Status) Status { return CloneStatus(s) }
