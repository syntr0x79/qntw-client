//go:build linux && !android

package clienttinc

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	wire "github.com/syntr0x79/qntw-client/internal/clienttincwire"
)

func testMeshInput() *meshInput {
	return &meshInput{self: "device1", peers: []string{"i1", "i2"}, egress: MeshEgress{ID: "public-node-id", Country: "PL"}}
}

func TestMeshObservationUnknownIsNotDisconnected(t *testing.T) {
	input := testMeshInput()
	m := collectMesh(context.Background(), "/tinc", "/private/native", input, func(context.Context, string, ...string) ([]byte, error) {
		return nil, errors.New("raw private failure must not escape")
	}, nil)
	if m.ObservationState != "unavailable" || len(m.Ingresses) != 2 || m.Ingresses[0].Name != "i1" || m.Ingresses[0].Connected != nil || m.Traffic != nil {
		t.Fatalf("unknown sample: %+v", m)
	}
	b, _ := json.Marshal(m)
	if strings.Contains(string(b), "connected") || strings.Contains(string(b), "raw private") {
		t.Fatalf("false fact/raw error: %s", b)
	}
	query := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[len(args)-1] == "connections" {
			return []byte("i1 at 192.0.2.3 port 17445 options 0 socket 12 status 100\ni2 at 192.0.2.4 port 17445 options 0 socket 13 status 4\n"), nil
		}
		return []byte("device1 to i1 at 192.0.2.3 port 17445\ndevice1 to i2 at 192.0.2.4 port 17445\n"), nil
	}
	m = collectMesh(context.Background(), "/tinc", "/private/native", input, query, nil)
	if m.ObservationState != "ok" || m.Ingresses[0].Connected == nil || !*m.Ingresses[0].Connected || m.Ingresses[1].Connected == nil || *m.Ingresses[1].Connected {
		t.Fatalf("authenticated/pending distinction: %+v", m)
	}
}

func TestMeshQueryCancellationAndPublicProjection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	m := collectMesh(ctx, "/tinc", "/private/native", testMeshInput(), func(ctx context.Context, _ string, _ ...string) ([]byte, error) { <-ctx.Done(); return nil, ctx.Err() }, nil)
	if time.Since(started) > time.Second || m.ObservationState != "unavailable" {
		t.Fatal("query ignored cancellation")
	}
	c := wire.ClientConfig{Self: wire.ClientIdentity{Name: "self"}, Ingresses: []wire.ClientIngress{{Name: "known", PublicKey: "private projection sentinel"}}, Assignment: wire.Assignment{Country: "NL", CertificatePEM: "certificate sentinel", TLSName: "not-a-display-name"}}
	input := publicMeshInput(c)
	if input.self != "self" || input.egress.Country != "NL" || input.egress.Name != "" || len(input.peers) != 1 || input.peers[0] != "known" {
		t.Fatal("public projection changed")
	}
}

func TestMeshTrafficRequiresSameOwnedSystemInterface(t *testing.T) {
	source := &ownedTraffic{name: "qtc0", index: 17, epoch: "helper-session"}
	lookup := func(string) (*net.Interface, error) { return &net.Interface{Index: 17}, nil }
	read := func(path string) ([]byte, error) {
		if path == "/sys/class/net/qtc0/statistics/rx_bytes" {
			return []byte("18446744073709551615\n"), nil
		}
		if path == "/sys/class/net/qtc0/statistics/tx_bytes" {
			return []byte("99\n"), nil
		}
		t.Fatal("unexpected counter path", path)
		return nil, errors.New("invalid")
	}
	now := time.Now()
	m := readOwnedTraffic(source, now, lookup, read)
	if m == nil || m.RXBytes != ^uint64(0) || m.TXBytes != 99 || m.Source != "system_tun" || m.Epoch != source.epoch || !m.SampledAt.Equal(now) {
		t.Fatalf("traffic %+v", m)
	}
	if readOwnedTraffic(nil, now, lookup, read) != nil {
		t.Fatal("SOCKS-only fabricated system traffic")
	}
	var calls int
	replaced := func(string) (*net.Interface, error) {
		calls++
		index := 17
		if calls > 1 {
			index = 18
		}
		return &net.Interface{Index: index}, nil
	}
	if readOwnedTraffic(source, now, replaced, read) != nil {
		t.Fatal("replacement interface counter accepted")
	}
	foreign := func(string) (*net.Interface, error) { return &net.Interface{Index: 18}, nil }
	if readOwnedTraffic(source, now, foreign, func(string) ([]byte, error) { t.Fatal("read a foreign interface"); return nil, nil }) != nil {
		t.Fatal("foreign counter")
	}
	for _, text := range []string{"-1", "18446744073709551616", strings.Repeat("1", 33)} {
		if readOwnedTraffic(source, now, lookup, func(string) ([]byte, error) { return []byte(text), nil }) != nil {
			t.Fatal("invalid counter accepted")
		}
	}
}

func TestMeshStatusDeepClone(t *testing.T) {
	yes := true
	s := Status{Ingresses: []string{"i1"}, Mesh: &MeshStatus{Ingresses: []MeshIngress{{Name: "i1", Connected: &yes}}, Traffic: &MeshTraffic{RXBytes: 10}}}
	copy := CloneStatus(s)
	copy.Ingresses[0] = "changed"
	copy.Mesh.Ingresses[0].Name = "changed"
	*copy.Mesh.Ingresses[0].Connected = false
	copy.Mesh.Traffic.RXBytes = 20
	if s.Ingresses[0] != "i1" || s.Mesh.Ingresses[0].Name != "i1" || !*s.Mesh.Ingresses[0].Connected || s.Mesh.Traffic.RXBytes != 10 {
		t.Fatal("public snapshot aliases live state")
	}
}

func TestMeshSamplerKeepsHealthStateAndDropsStaleGeneration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updates := make(chan Status, 32)
	c := &controller{onStatus: func(s Status) { updates <- s }}
	ttl := time.Now().Add(time.Minute)
	c.set(Status{State: "connecting", Country: "PL", EgressID: "public-node-id", ExpiresAt: ttl, Message: "Waiting for the authorized exit"})
	entered := make(chan *meshInput, 2)
	release := make(chan struct{})
	var collected atomic.Int32
	stop := c.runMeshSampler(ctx, 15*time.Millisecond, func(ctx context.Context, input *meshInput, _ *ownedTraffic) *MeshStatus {
		if collected.Add(1) == 1 {
			entered <- input
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
		return &MeshStatus{ObservationState: "ok", ObservedAt: time.Now(), Egress: input.egress}
	}, nil)
	defer stop()
	c.setMeshInput(testMeshInput())
	var old *meshInput
	select {
	case old = <-entered:
	case <-time.After(time.Second):
		t.Fatal("sampler did not start")
	}
	newInput := testMeshInput()
	newInput.egress.Country = "NL"
	c.set(Status{State: "connecting", Country: "NL", EgressID: "public-node-id", ExpiresAt: ttl})
	c.setMeshInput(newInput)
	close(release)
	deadline := time.After(time.Second)
	for {
		select {
		case s := <-updates:
			if s.State != "connecting" || !s.ExpiresAt.Equal(ttl) {
				t.Fatal("sampling changed health/permission state")
			}
			if s.Mesh == nil {
				continue
			}
			if s.Mesh.Egress.Country != "NL" {
				t.Fatal("stale sample replaced current assignment")
			}
			goto observed
		case <-deadline:
			t.Fatal("no local update without API/health polling")
		}
	}
observed:
	c.setMeshInput(nil)
	c.set(Status{State: "expired", ExpiresAt: ttl})
	c.publishMesh(old, &MeshStatus{ObservationState: "ok", Egress: old.egress})
	if s := c.get(); s.State != "expired" || s.Mesh != nil {
		t.Fatal("old observer revived an expired path")
	}
}

func TestMeshObservationCannotMixNewAssignmentWithOldLifecycleIdentity(t *testing.T) {
	c := &controller{}
	c.set(Status{State: "connected", Country: "PL", EgressID: "old"})
	input := testMeshInput()
	input.egress = MeshEgress{Country: "NL", ID: "new"}
	c.setMeshInput(input)
	snapshot := c.meshSnapshot()
	c.publishMesh(snapshot, &MeshStatus{ObservationState: "ok", Egress: snapshot.egress})
	if s := c.get(); s.Mesh != nil || s.Country != "PL" || s.State != "connected" {
		t.Fatal("new telemetry published against old connection state")
	}
	c.set(Status{State: "connecting", Country: "NL", EgressID: "new"})
	c.publishMesh(snapshot, &MeshStatus{ObservationState: "ok", Egress: snapshot.egress})
	if s := c.get(); s.Mesh == nil || s.Mesh.Egress.Country != "NL" || s.State != "connecting" {
		t.Fatal("observation changed health readiness")
	}
}

func TestMeshSamplerStopCancelsNativeQuery(t *testing.T) {
	entered := make(chan struct{})
	c := &controller{}
	stop := c.runMeshSampler(context.Background(), time.Hour, func(ctx context.Context, input *meshInput, _ *ownedTraffic) *MeshStatus {
		close(entered)
		<-ctx.Done()
		return &MeshStatus{ObservationState: "unavailable"}
	}, nil)
	c.setMeshInput(testMeshInput())
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("collector did not start")
	}
	done := make(chan struct{})
	go func() { stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("local observer kept runtime alive")
	}
}
