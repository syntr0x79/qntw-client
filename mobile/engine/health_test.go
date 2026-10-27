package engine

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	xout "github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/transport"
)

type countedOutbound struct {
	tag   string
	calls atomic.Int64
}

func (h *countedOutbound) Tag() string                          { return h.tag }
func (h *countedOutbound) Start() error                         { return nil }
func (h *countedOutbound) Close() error                         { return nil }
func (h *countedOutbound) SenderSettings() *serial.TypedMessage { return nil }
func (h *countedOutbound) ProxySettings() *serial.TypedMessage  { return nil }
func (h *countedOutbound) Dispatch(_ context.Context, l *transport.Link) {
	h.calls.Add(1)
	common.Close(l.Writer)
	common.Interrupt(l.Reader)
}
func TestHealthCheckCannotUseDirectBypass(t *testing.T) {
	cfg, e := core.LoadConfig("json", strings.NewReader(`{"outbounds":[{"protocol":"blackhole","tag":"vpn"},{"protocol":"freedom","tag":"direct"}],"routing":{"rules":[{"type":"field","network":"tcp,udp","outboundTag":"direct"}]}}`))
	if e != nil {
		t.Fatal(e)
	}
	instance, e := core.New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	manager := instance.GetFeature(xout.ManagerType()).(xout.Manager)
	via := &countedOutbound{tag: "vpn"}
	direct := &countedOutbound{tag: "direct"}
	for _, handler := range []*countedOutbound{via, direct} {
		manager.RemoveHandler(context.Background(), handler.tag)
		if e = manager.AddHandler(context.Background(), handler); e != nil {
			t.Fatal(e)
		}
	}
	if e = instance.Start(); e != nil {
		t.Fatal(e)
	}
	vpn.Lock()
	vpn.closer = &xrayTunnel{core: instance, fd: -1}
	vpn.Unlock()
	defer StopVPN()
	healthy, e := CheckVPN("https://control.example/")
	if e != nil {
		t.Fatal(e)
	}
	if healthy {
		t.Fatal("dead outbound declared healthy")
	}
	if via.calls.Load() != 1 || direct.calls.Load() != 0 {
		t.Fatalf("health routed via vpn=%d direct=%d", via.calls.Load(), direct.calls.Load())
	}
}
