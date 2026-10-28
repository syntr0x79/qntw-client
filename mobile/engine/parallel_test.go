package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/syntr0x79/qntw-backend/internal/mobileprobe/wire"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/proxy/vless/outbound"
	"google.golang.org/protobuf/proto"
)

type parallelDNS struct {
	entered chan struct{}
	release chan struct{}
}

func (n *parallelDNS) PrepareSocket(int) error { return nil }
func (n *parallelDNS) Resolve(string) (string, error) {
	n.entered <- struct{}{}
	<-n.release
	return "192.0.2.1", nil
}
func parallelJob(t *testing.T, id string) string {
	t.Helper()
	out := &core.OutboundHandlerConfig{ProxySettings: serial.ToTypedMessage(&outbound.Config{Vnext: &protocol.ServerEndpoint{Address: xnet.NewIPOrDomain(xnet.DomainAddress("mask.example")), Port: 443}})}
	payload, err := proto.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(wire.Job{ID: id, Engine: "xray", Outbound: payload})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
func TestTenParallelNativeProbesCancelTogether(t *testing.T) {
	n := &parallelDNS{make(chan struct{}, 10), make(chan struct{})}
	defer close(n.release)
	done := make(chan string, 10)
	for i := 0; i < 10; i++ {
		job := parallelJob(t, fmt.Sprint(i))
		go func() {
			data, err := ProbeOnNetwork(job, "https://control.invalid/", 30000, n, "wifi-epoch")
			if err != nil {
				done <- "error"
			} else {
				done <- data
			}
		}()
	}
	for i := 0; i < 10; i++ {
		select {
		case <-n.entered:
		case <-time.After(3 * time.Second):
			Cancel()
			t.Fatal("ten native probes did not enter DNS concurrently")
		}
	}
	// A different physical epoch may not replace the process-wide controller.
	state.Lock()
	if len(state.probes) != 10 || state.network != n {
		state.Unlock()
		t.Fatal("incorrect native admission")
	}
	state.Unlock()
	Cancel()
	for i := 0; i < 10; i++ {
		select {
		case data := <-done:
			var result wire.Result
			if err := json.Unmarshal([]byte(data), &result); err != nil || result.Outcome != "cancelled" {
				t.Fatalf("result=%s", data)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("cancel did not drain all probes")
		}
	}
	// The next epoch can start after all cancelled calls have drained.
	bad, _ := json.Marshal(wire.Job{ID: "next", Engine: "xray", Outbound: []byte{0xff}})
	data, err := ProbeOnNetwork(string(bad), "https://control.invalid/", 100, n, "cell-epoch")
	if err != nil {
		t.Fatal(err)
	}
	var result wire.Result
	json.Unmarshal([]byte(data), &result)
	if result.Outcome != "unprobeable" {
		t.Fatalf("fresh epoch cancelled: %s", data)
	}
}

func TestNativeCapacityAndEpochIsolation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first, second := &testNetwork{}, &testNetwork{}
	releases := make([]func(), 0, 10)
	defer func() {
		for _, release := range releases {
			release()
		}
	}()
	for i := 0; i < 10; i++ {
		release, err := reserveProbe(ctx, cancel, first, "wifi")
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	same := make(chan func(), 1)
	go func() {
		release, err := reserveProbe(ctx, cancel, first, "wifi")
		if err == nil {
			same <- release
		}
	}()
	select {
	case release := <-same:
		release()
		t.Fatal("eleventh probe exceeded capacity")
	case <-time.After(50 * time.Millisecond):
	}
	releases[0]()
	select {
	case release := <-same:
		release()
	case <-time.After(time.Second):
		t.Fatal("freed slot was not reused")
	}
	next := make(chan func(), 1)
	go func() {
		release, err := reserveProbe(ctx, cancel, second, "cell")
		if err == nil {
			next <- release
		}
	}()
	select {
	case release := <-next:
		release()
		t.Fatal("different epochs overlapped")
	case <-time.After(50 * time.Millisecond):
	}
	state.Lock()
	network := state.network
	state.Unlock()
	if network != first {
		t.Fatal("old controller replaced before drain")
	}
	for _, release := range releases[1:] {
		release()
	}
	select {
	case release := <-next:
		state.Lock()
		network = state.network
		state.Unlock()
		release()
		if network != second {
			t.Fatal("new epoch did not bind its adapter")
		}
	case <-time.After(time.Second):
		t.Fatal("new epoch did not start after drain")
	}
}
