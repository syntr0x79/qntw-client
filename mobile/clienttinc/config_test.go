package clienttinc

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/google/uuid"
	wire "github.com/syntr0x79/qntw-client/internal/clienttincwire"
)

func configFixture(t *testing.T) wire.ClientConfig {
	t.Helper()
	now := time.Now()
	pub, priv, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "pilot.test"}, DNSNames: []string{"pilot.test"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, e := x509.CreateCertificate(rand.Reader, cert, cert, pub, priv)
	if e != nil {
		t.Fatal(e)
	}
	return wire.ClientConfig{SchemaVersion: 1, Revision: "rev1", ExpiresAt: now.Add(5 * time.Minute), DeviceID: uuid.New(), Kind: "desktop", Self: wire.ClientIdentity{Name: "device1", Address: "10.77.1.1"}, DNS: []string{"1.1.1.1"}, Ingresses: []wire.ClientIngress{{NodeID: uuid.New(), Name: "ingress1", PublicKey: "/YsLMYpYiJIl5dU9irpkQebvuP+0YvrAW8bOiYwsmyH", Endpoint: "192.0.2.1:17445", Address: "10.77.1.11", RoutePrefix: "10.76.0.0/24", Weight: 10}}, Assignment: wire.Assignment{EgressID: uuid.New(), Country: "NL", Version: 1, Address: "10.76.0.4", Port: 18443, VLESSUUID: uuid.New(), TLSName: "pilot.test", CertificatePEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), ExpiresAt: now.Add(4 * time.Minute)}}
}

func TestRejectUnsafeOrExpiredServerConfiguration(t *testing.T) {
	mutations := map[string]func(*wire.ClientConfig){
		"expired":                  func(c *wire.ClientConfig) { c.ExpiresAt = time.Now().Add(-time.Second) },
		"unbounded lease":          func(c *wire.ClientConfig) { c.ExpiresAt = time.Now().Add(time.Hour) },
		"config injection":         func(c *wire.ClientConfig) { c.Ingresses[0].Name = "ingress\nProxy=exec" },
		"hostname endpoint":        func(c *wire.ClientConfig) { c.Ingresses[0].Endpoint = "outside.test:17445" },
		"arbitrary route":          func(c *wire.ClientConfig) { c.Ingresses[0].RoutePrefix = "0.0.0.0/0" },
		"foreign assigned address": func(c *wire.ClientConfig) { c.Assignment.Address = "192.0.2.1" },
		"wrong TLS hostname":       func(c *wire.ClientConfig) { c.Assignment.TLSName = "untrusted.test" },
		"invalid credential":       func(c *wire.ClientConfig) { c.Assignment.VLESSUUID = uuid.Nil },
		"duplicate ingress":        func(c *wire.ClientConfig) { c.Ingresses = append(c.Ingresses, c.Ingresses[0]) },
	}
	if e := ValidateConfig(configFixture(t), time.Now()); e != nil {
		t.Fatal(e)
	}
	for name, change := range mutations {
		t.Run(name, func(t *testing.T) {
			c := configFixture(t)
			change(&c)
			if ValidateConfig(c, time.Now()) == nil {
				t.Fatal("unsafe configuration accepted")
			}
		})
	}
}

func TestLeaseRenewalKeepsPathButAssignmentChangeRebuilds(t *testing.T) {
	c := configFixture(t)
	before := Fingerprint(c)
	c.ExpiresAt = c.ExpiresAt.Add(time.Second)
	c.Assignment.ExpiresAt = c.Assignment.ExpiresAt.Add(time.Second)
	c.Revision = "new-server-revision"
	if Fingerprint(c) != before {
		t.Fatal("renewal unexpectedly changes data path")
	}
	c.Assignment.Version++
	if Fingerprint(c) == before {
		t.Fatal("new assignment does not change data path")
	}
}
