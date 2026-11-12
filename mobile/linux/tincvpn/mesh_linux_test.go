//go:build linux && !android

package tincvpn

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/syntr0x79/qntw-client/mobile/clienttinc"
)

func TestMeshCapabilityKeepsLegacyStrictStatusDecoderWorking(t *testing.T) {
	for _, want := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy_parent", true: "mesh_parent"}[want], func(t *testing.T) {
			a, b := pair(t)
			o := fixtureOptions(t)
			o.run = func(ctx context.Context, options clienttinc.Options) error {
				yes := true
				options.OnStatus(Status{State: "connected", Country: "PL", Mesh: &clienttinc.MeshStatus{ObservationState: "ok", Ingresses: []clienttinc.MeshIngress{{Name: "known", Connected: &yes}}}})
				<-ctx.Done()
				return nil
			}
			done := make(chan error, 1)
			go func() { done <- serve(context.Background(), b, uint32(os.Getuid()), o) }()
			w := writer{conn: a}
			if err := w.send(packet{Type: "start", Origin: apiOrigin, Token: testToken, Country: "PL", WantMesh: want}); err != nil {
				t.Fatal(err)
			}
			if p, err := receive(a); err != nil || p.Type != "accepted" {
				t.Fatalf("acceptance: %+v %v", p, err)
			}
			_ = a.SetReadDeadline(time.Now().Add(time.Second))
			buf := make([]byte, maxPacket)
			n, err := a.Read(buf)
			if err != nil {
				t.Fatal(err)
			}
			if want {
				var p packet
				if json.Unmarshal(buf[:n], &p) != nil || p.Status.Mesh == nil || p.Status.Mesh.ObservationState != "ok" {
					t.Fatal("negotiated telemetry missing")
				}
			} else {
				// This is the previous strict nested status shape, without Mesh.
				var legacy struct {
					Type   string `json:"type"`
					Status *struct {
						State          string    `json:"state"`
						Country        string    `json:"country,omitempty"`
						DesiredCountry string    `json:"desired_country,omitempty"`
						EgressID       string    `json:"egress_id,omitempty"`
						ExpiresAt      time.Time `json:"expires_at,omitempty"`
						Ingresses      []string  `json:"connected_ingresses,omitempty"`
						Message        string    `json:"message,omitempty"`
						Code           string    `json:"code,omitempty"`
						RetryAfter     int       `json:"retry_after,omitempty"`
					} `json:"status"`
				}
				decoder := json.NewDecoder(bytes.NewReader(buf[:n]))
				decoder.DisallowUnknownFields()
				if err := decoder.Decode(&legacy); err != nil || legacy.Status == nil || legacy.Status.State != "connected" {
					t.Fatalf("legacy parent rejected new root: %v", err)
				}
			}
			if err := w.send(packet{Type: "stop", ID: 1}); err != nil {
				t.Fatal(err)
			}
			if p, err := receive(a); err != nil || p.Type != "stopped" {
				t.Fatalf("cleanup: %+v %v", p, err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMeshCapabilityCannotAppearOnControlCommandsOrErrors(t *testing.T) {
	if startupPacketFailure(packet{Type: "error", Code: "helper_missing", WantMesh: true}) != nil {
		t.Fatal("response-only capability accepted as startup failure")
	}
	a, b := pair(t)
	o := fixtureOptions(t)
	called := make(chan struct{}, 1)
	o.country = func(context.Context, string, string) error { called <- struct{}{}; return nil }
	done := make(chan error, 1)
	go func() { done <- serve(context.Background(), b, uint32(os.Getuid()), o) }()
	w := writer{conn: a}
	if err := w.send(packet{Type: "start", Origin: apiOrigin, Token: testToken, Country: "PL", WantMesh: true}); err != nil {
		t.Fatal(err)
	}
	if p, err := receive(a); err != nil || p.Type != "accepted" {
		t.Fatal("not accepted", err)
	}
	if p, err := receive(a); err != nil || p.Type != "status" {
		t.Fatal("missing status", err)
	}
	if err := w.send(packet{Type: "country", ID: 1, Country: "NL", WantMesh: true}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("invalid capability command did not stop the helper")
	}
	select {
	case <-called:
		t.Fatal("invalid command reached country control")
	default:
	}
}

func TestPostAcceptanceTerminalReasonDrainsBeforeProcessExit(t *testing.T) {
	for range 20 {
		o := fixtureOptions(t)
		o.run = func(context.Context, clienttinc.Options) error {
			return clienttinc.RuntimeError{Code: "dns_unavailable"}
		}
		s, err := Start(context.Background(), Config{Origin: apiOrigin, Token: testToken, Country: "PL", launch: launchFixture(t, o)})
		if err != nil {
			t.Fatal(err)
		}
		select {
		case <-s.Done():
		case <-time.After(2 * time.Second):
			t.Fatal("terminal read did not finish")
		}
		if FailureCode(s.Close()) != "dns_unavailable" {
			t.Fatal("queued terminal reason was discarded")
		}
	}
}

func TestMeshStatusCloneAcrossPrivateIPC(t *testing.T) {
	yes := true
	s := Status{State: "connected", Mesh: &clienttinc.MeshStatus{ObservationState: "ok", Ingresses: []clienttinc.MeshIngress{{Name: "i1", Connected: &yes}}, Traffic: &clienttinc.MeshTraffic{TXBytes: 12}}}
	copy := copyStatus(s)
	copy.Mesh.Ingresses[0].Name = "changed"
	*copy.Mesh.Ingresses[0].Connected = false
	copy.Mesh.Traffic.TXBytes = 99
	if s.Mesh.Ingresses[0].Name != "i1" || !*s.Mesh.Ingresses[0].Connected || s.Mesh.Traffic.TXBytes != 12 {
		t.Fatal("IPC status clone aliases mutable telemetry")
	}
	a, b := pair(t)
	go func() {
		if err := (&writer{conn: a}).send(packet{Type: "status", Status: &s}); err != nil {
			t.Error(err)
		}
	}()
	_ = b.SetReadDeadline(time.Now().Add(time.Second))
	p, err := receive(b)
	if err != nil || p.Status == nil || p.Status.Mesh == nil || p.Status.Mesh.Traffic.TXBytes != 12 || !*p.Status.Mesh.Ingresses[0].Connected {
		t.Fatalf("mesh packet: %+v %v", p, err)
	}
}

func TestOptionalMeshCannotOverflowPrivatePacketBudget(t *testing.T) {
	a, b := pair(t)
	s := Status{State: "connecting", Mesh: &clienttinc.MeshStatus{ObservationState: "ok", ObservedAt: time.Now(), Egress: clienttinc.MeshEgress{Country: "PL"}}}
	for range 128 {
		yes := true
		s.Mesh.Ingresses = append(s.Mesh.Ingresses, clienttinc.MeshIngress{Name: strings.Repeat("a", 64), Connected: &yes})
	}
	result := make(chan error, 1)
	go func() { result <- (&writer{conn: a}).send(packet{Type: "status", Status: &s}) }()
	_ = b.SetReadDeadline(time.Now().Add(time.Second))
	p, err := receive(b)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal("optional telemetry stopped private IPC", err)
	}
	if p.Status.State != "connecting" || p.Status.Mesh.ObservationState != "unavailable" || len(p.Status.Mesh.Ingresses) != 0 || p.Status.Mesh.Egress.Country != "PL" {
		t.Fatal("oversize fallback invented readiness")
	}
}
