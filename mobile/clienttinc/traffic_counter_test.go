package clienttinc

import (
	"math"
	"testing"
)

func TestTrafficCountersAccumulateAcrossAdapterRecreation(t *testing.T) {
	var counter trafficCounter
	for _, sample := range []struct{ generation, rx, tx, wantRX, wantTX uint64 }{
		{1, 100, 200, 100, 200},
		{1, 150, 260, 150, 260},
		// The new adapter may already have larger counters than its predecessor.
		// An explicit owned generation must distinguish it, even with reused LUID.
		{2, 200, 300, 350, 560},
		{2, 210, 330, 360, 590},
		{2, 4, 5, 364, 595},
	} {
		rx, tx := counter.observe(sample.generation, sample.rx, sample.tx)
		if rx != sample.wantRX || tx != sample.wantTX {
			t.Fatalf("sample %+v produced %d/%d", sample, rx, tx)
		}
	}
}

func TestTrafficCounterOverflowRemainsMonotonic(t *testing.T) {
	counter := trafficCounter{generation: 1, lastRX: 10, lastTX: 10, totalRX: math.MaxUint64 - 2, totalTX: math.MaxUint64 - 1}
	rx, tx := counter.observe(1, 20, 20)
	if rx != math.MaxUint64 || tx != math.MaxUint64 {
		t.Fatalf("overflowed %d/%d", rx, tx)
	}
}

func TestCloneStatusOwnsEveryMutableObservation(t *testing.T) {
	connected := true
	before := Status{Ingresses: []string{"in1"}, Mesh: &MeshStatus{Ingresses: []MeshIngress{{Name: "in1", Connected: &connected}}, Traffic: &MeshTraffic{RXBytes: 12}}}
	after := CloneStatus(before)
	after.Ingresses[0] = "changed"
	after.Mesh.Ingresses[0].Name = "changed"
	*after.Mesh.Ingresses[0].Connected = false
	after.Mesh.Traffic.RXBytes = 99
	if before.Ingresses[0] != "in1" || before.Mesh.Ingresses[0].Name != "in1" || !*before.Mesh.Ingresses[0].Connected || before.Mesh.Traffic.RXBytes != 12 {
		t.Fatal("public state shares mutable memory")
	}
}
