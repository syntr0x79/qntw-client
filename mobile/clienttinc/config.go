// Package clienttinc implements the standalone Linux pilot client. It is
// intentionally independent from the desktop UI and its current VPN engine.
package clienttinc

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/syntr0x79/qntw-client/internal/agentproto"
	wire "github.com/syntr0x79/qntw-client/internal/clienttincwire"
)

type Credentials struct {
	Origin string `json:"origin"`
	Token  string `json:"token"`
	// Only loopback HTTP fixtures may opt out of HTTPS. Never use for deployment.
	AllowLoopbackHTTP bool `json:"allow_loopback_http,omitempty"`
}

func ReadCredentials(path string) (Credentials, error) {
	var c Credentials
	info, err := os.Lstat(path)
	if err != nil {
		return c, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return c, errors.New("credentials must be a regular private file (0600)")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if len(data) > 8192 || json.Unmarshal(data, &c) != nil {
		return c, errors.New("invalid credentials file")
	}
	return ValidateCredentials(c)
}

// ValidateCredentials validates an in-memory device credential. Privileged
// desktop helpers use it without creating a credential file on disk.
func ValidateCredentials(c Credentials) (Credentials, error) {
	u, err := url.Parse(c.Origin)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return c, errors.New("invalid API origin")
	}
	if u.Scheme != "https" {
		ip, e := netip.ParseAddr(u.Hostname())
		if u.Scheme != "http" || !c.AllowLoopbackHTTP || e != nil || !ip.IsLoopback() {
			return c, errors.New("API origin requires HTTPS")
		}
	}
	if len(c.Token) < 16 || len(c.Token) > 4096 || strings.ContainsAny(c.Token, " \r\n\t") {
		return c, errors.New("invalid device token")
	}
	c.Origin = strings.TrimSuffix(c.Origin, "/")
	return c, nil
}

var safeName = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)
var safeCountry = regexp.MustCompile(`^[A-Z]{2}$`)

func ValidateConfig(c wire.ClientConfig, now time.Time) error {
	bad := errors.New("invalid or expired client tinc configuration")
	if c.SchemaVersion != wire.SchemaVersion || c.DeviceID == uuid.Nil || c.Kind != "desktop" || !safeName.MatchString(c.Self.Name) || c.Revision == "" || len(c.Revision) > 256 || !c.ExpiresAt.After(now) || c.ExpiresAt.After(now.Add(10*time.Minute)) {
		return bad
	}
	self, e := netip.ParseAddr(c.Self.Address)
	if e != nil || !self.Is4() || !netip.MustParsePrefix("10.77.0.0/16").Contains(self) || len(c.Ingresses) == 0 || len(c.Ingresses) > 128 {
		return bad
	}
	if c.Assignment.EgressID == uuid.Nil || c.Assignment.VLESSUUID == uuid.Nil || c.Assignment.Version == 0 || !safeCountry.MatchString(c.Assignment.Country) || !c.Assignment.ExpiresAt.After(now) || c.Assignment.ExpiresAt.After(now.Add(10*time.Minute)) || c.Assignment.Port != 18443 || len(c.Assignment.TLSName) > 253 || len(c.Assignment.CertificatePEM) > 65536 {
		return bad
	}
	assigned, e := netip.ParseAddr(c.Assignment.Address)
	if e != nil || !assigned.Is4() || !netip.MustParsePrefix("10.76.0.0/24").Contains(assigned) {
		return bad
	}
	block, rest := pem.Decode([]byte(c.Assignment.CertificatePEM))
	if block == nil || block.Type != "CERTIFICATE" || len(strings.TrimSpace(string(rest))) != 0 {
		return bad
	}
	cert, e := x509.ParseCertificate(block.Bytes)
	if e != nil || cert.VerifyHostname(c.Assignment.TLSName) != nil || now.Before(cert.NotBefore) || !now.Before(cert.NotAfter) {
		return bad
	}
	ids, names, keys, endpoints := map[uuid.UUID]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, in := range c.Ingresses {
		endpoint, e := netip.ParseAddrPort(in.Endpoint)
		addr, aerr := netip.ParseAddr(in.Address)
		prefix, perr := netip.ParsePrefix(in.RoutePrefix)
		if e != nil || !endpoint.Addr().Is4() || endpoint.Addr().IsUnspecified() || endpoint.Addr().IsMulticast() || endpoint.Port() == 0 || aerr != nil || !addr.Is4() || !netip.MustParsePrefix("10.77.0.0/16").Contains(addr) || perr != nil || prefix != netip.MustParsePrefix("10.76.0.0/24") || !prefix.Contains(assigned) || in.NodeID == uuid.Nil || !safeName.MatchString(in.Name) || in.Name == c.Self.Name || !agentproto.ValidTincPubkey(in.PublicKey) || in.Weight < 1 || in.Weight > 10000 || ids[in.NodeID] || names[in.Name] || keys[in.PublicKey] || endpoints[in.Endpoint] {
			return bad
		}
		ids[in.NodeID], names[in.Name], keys[in.PublicKey], endpoints[in.Endpoint] = true, true, true, true
	}
	if len(c.DNS) == 0 || len(c.DNS) > 4 {
		return bad
	}
	for _, value := range c.DNS {
		ip, e := netip.ParseAddr(value)
		if e != nil || !ip.Is4() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			return bad
		}
	}
	return nil
}

// Fingerprint excludes only lease expiries. A renewed unchanged grant does not
// restart the data path or unnecessarily break active user connections.
func Fingerprint(c wire.ClientConfig) string {
	c.ExpiresAt = time.Time{}
	c.Assignment.ExpiresAt = time.Time{}
	c.Revision = ""
	b, _ := json.Marshal(c)
	return string(b)
}
