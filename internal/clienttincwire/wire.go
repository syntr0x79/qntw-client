// Package clienttincwire defines the bounded opt-in client-tinc pilot protocol.
// Private device/server keys never cross this API. Addresses are numeric strings
// and must be validated by both producer and consumer before applying anything.
package clienttincwire

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

const SchemaVersion = 1
const MaxDevices = 16

// NodeRevision binds a desired document while permitting renewal of its outer
// node lease without restarting existing flows. Producers keep slice ordering
// deterministic. Reported server public keys are not part of an edge's inputs.
func NodeRevision(d NodeDocument) string {
	d.Revision = ""
	d.ExpiresAt = time.Time{}
	b, _ := json.Marshal(d)
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

type NodeSpec struct {
	NodeID       uuid.UUID `json:"node_id"`
	Name         string    `json:"name"`
	Endpoint     string    `json:"endpoint"` // Numeric public IP:core-port.
	CoreAddress  string    `json:"core_address"`
	Ingress      bool      `json:"ingress"`
	Egress       bool      `json:"egress"`
	Country      string    `json:"country,omitempty"`
	RouteID      uuid.UUID `json:"route_id,omitempty"`
	EdgePortBase uint16    `json:"edge_port_base"`
	InnerPort    uint16    `json:"inner_port"`
	TLSName      string    `json:"tls_name"`
}

type CorePeer struct {
	NodeID    uuid.UUID `json:"node_id"`
	Name      string    `json:"name"`
	PublicKey string    `json:"public_key"`
	Endpoint  string    `json:"endpoint"`
	Address   string    `json:"address"`
}

type Edge struct {
	DeviceID        uuid.UUID `json:"device_id"`
	Slot            int       `json:"slot"`
	ClientName      string    `json:"client_name"`
	ClientPublicKey string    `json:"client_public_key"`
	ClientAddress   string    `json:"client_address"`
	ServerAddress   string    `json:"server_address"`
	Port            uint16    `json:"port"`
	Weight          int       `json:"weight"`
	ExpiresAt       time.Time `json:"expires_at"`
}

type EgressGrant struct {
	DeviceID  uuid.UUID `json:"device_id"`
	Version   uint64    `json:"version"`
	VLESSUUID uuid.UUID `json:"vless_uuid"`
	ExpiresAt time.Time `json:"expires_at"`
}

type EgressConfig struct {
	ListenPort uint16        `json:"listen_port"`
	ServerName string        `json:"server_name"`
	Grants     []EgressGrant `json:"grants"`
}

type NodeDocument struct {
	SchemaVersion int           `json:"schema_version"`
	Revision      string        `json:"revision"`
	ExpiresAt     time.Time     `json:"expires_at"`
	Node          NodeSpec      `json:"node"`
	ServicePrefix string        `json:"service_prefix"`
	CorePeers     []CorePeer    `json:"core_peers"`
	Edges         []Edge        `json:"edges"`
	Egress        *EgressConfig `json:"egress,omitempty"`
}

type NodeReport struct {
	NodeID               uuid.UUID         `json:"node_id"`
	Revision             string            `json:"revision,omitempty"`
	State                string            `json:"state"` // bootstrap, applied, failed, expired.
	Error                string            `json:"error,omitempty"`
	CorePublicKey        string            `json:"core_public_key"`
	EdgePublicKeys       map[string]string `json:"edge_public_keys"` // device UUID -> key.
	DefaultEdgePublicKey string            `json:"default_edge_public_key,omitempty"`
	TLSCertPEM           string            `json:"tls_cert_pem"`    // Locally generated, self-signed pilot trust anchor.
	Usage                []UsageCounter    `json:"usage,omitempty"` // Authenticated egress counters only.
}

// UsageCounter is cumulative for one (device, assignment, Xray epoch, MSK day).
// Epoch changes only when the pilot Xray process/counters restart. A successful
// response acknowledges the whole report; retries retain these exact counters.
type UsageCounter struct {
	DeviceID uuid.UUID `json:"device_id"`
	Version  uint64    `json:"version"`
	Epoch    uuid.UUID `json:"epoch"`
	Day      string    `json:"day"`
	Up       int64     `json:"up"`
	Down     int64     `json:"down"`
}

const MaxUsageCounters = 256

type IdentityRequest struct {
	PublicKey string `json:"public_key"`
	Kind      string `json:"kind"` // phone, desktop, tv.
}

type CountryRequest struct {
	Country string `json:"country"`
}

type ClientIdentity struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

type ClientIngress struct {
	NodeID      uuid.UUID `json:"node_id"`
	Name        string    `json:"name"`
	PublicKey   string    `json:"public_key"`
	Endpoint    string    `json:"endpoint"` // Numeric public IP:per-device-port.
	Address     string    `json:"address"`
	RoutePrefix string    `json:"route_prefix"`
	Weight      int       `json:"weight"`
}

type Assignment struct {
	EgressID       uuid.UUID `json:"egress_id"`
	Country        string    `json:"country"`
	Version        uint64    `json:"version"`
	Address        string    `json:"address"`
	Port           uint16    `json:"port"`
	VLESSUUID      uuid.UUID `json:"vless_uuid"`
	TLSName        string    `json:"tls_name"`
	CertificatePEM string    `json:"certificate_pem"`
	ExpiresAt      time.Time `json:"expires_at"`
}

type ClientConfig struct {
	SchemaVersion int             `json:"schema_version"`
	Revision      string          `json:"revision"`
	ExpiresAt     time.Time       `json:"expires_at"`
	DeviceID      uuid.UUID       `json:"device_id"`
	Kind          string          `json:"kind"`
	Self          ClientIdentity  `json:"self"`
	Ingresses     []ClientIngress `json:"ingresses"`
	Assignment    Assignment      `json:"assignment"`
	DNS           []string        `json:"dns"`
}

// ClientCatalog exposes country choices before registering a device's tinc key.
// It contains no network endpoints, server identities or credentials. State is
// available, preparing or unavailable; readiness of a concrete assignment is
// still checked by the separate config endpoint.
type ClientCatalog struct {
	SchemaVersion    int              `json:"schema_version"`
	DeviceID         uuid.UUID        `json:"device_id"`
	RequestedCountry string           `json:"requested_country,omitempty"`
	State            string           `json:"state"`
	Countries        []CatalogCountry `json:"countries"`
}

type CatalogCountry struct {
	Code  string `json:"code"`
	State string `json:"state"`
}
