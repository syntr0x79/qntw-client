package engine

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/syntr0x79/qntw-client/internal/awgprobe"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/common/signal"
	"github.com/xtls/xray-core/common/task"
	"github.com/xtls/xray-core/transport"
	"strings"
	"sync"
	"time"
)

type mobileRouting struct {
	ForcedVPN     []string `json:"-"`
	Enabled       bool     `json:"enabled"`
	Revision      string   `json:"revision"`
	TunnelDomains []string `json:"tunnel_domains"`
	DirectDomains []string `json:"direct_domains"`
	DirectCIDRs   []string `json:"direct_cidrs"`
	Exceptions    []string `json:"exceptions"`
}

var routingState struct {
	sync.Mutex
	policy mobileRouting
}

// ConfigureRouting applies to the next system tunnel; probes remain unsplit.
func ConfigureRouting(document string) error {
	if len(document) > 1<<20 {
		return errors.New("routing policy too large")
	}
	var p mobileRouting
	if e := json.Unmarshal([]byte(document), &p); e != nil {
		return errors.New("invalid routing policy")
	}
	if len(p.Exceptions) > 128 {
		return errors.New("too many exceptions")
	}
	for _, s := range p.Exceptions {
		if len(s) > 253 || strings.ContainsAny(s, " /:\n\r\t") || !strings.Contains(s, ".") {
			return errors.New("invalid domain exception")
		}
	}
	routingState.Lock()
	routingState.policy = p
	routingState.Unlock()
	return nil
}
func currentRouting() mobileRouting {
	routingState.Lock()
	defer routingState.Unlock()
	return routingState.policy
}
func tunnelDocument(p mobileRouting, tunName ...string) string {
	name := "quantway"
	if len(tunName) > 0 && tunName[0] != "" {
		name = tunName[0]
	}
	rules := []any{}
	rule := func(tag, key string, v []string) {
		if len(v) > 0 {
			rules = append(rules, map[string]any{"type": "field", "outboundTag": tag, key: v})
		}
	}
	exceptions := []string{}
	for _, s := range p.Exceptions {
		exceptions = append(exceptions, "domain:"+s)
	}
	rule("direct", "domain", exceptions)
	rule("vpn", "ip", p.ForcedVPN)
	if p.Enabled {
		rule("vpn", "domain", p.TunnelDomains)
		rule("direct", "domain", p.DirectDomains)
		rule("direct", "ip", p.DirectCIDRs)
	}
	rule("block", "ip", []string{"::/0"})
	rules = append(rules, map[string]any{"type": "field", "outboundTag": "vpn", "network": "tcp,udp"})
	doc := map[string]any{"inbounds": []any{map[string]any{"tag": "android-tun", "protocol": "tun", "settings": map[string]any{"name": name, "mtu": 1280}, "sniffing": map[string]any{"enabled": true, "destOverride": []string{"http", "tls", "quic"}, "routeOnly": true}}}, "outbounds": []any{map[string]any{"protocol": "blackhole", "tag": "vpn"}, map[string]any{"protocol": "freedom", "tag": "direct"}, map[string]any{"protocol": "blackhole", "tag": "block"}}, "routing": map[string]any{"domainStrategy": "AsIs", "rules": rules}}
	b, _ := json.Marshal(doc)
	return string(b)
}

type awgOutbound struct{ stack *awgprobe.Stack }

func (h *awgOutbound) Tag() string                          { return "vpn" }
func (h *awgOutbound) Start() error                         { return nil }
func (h *awgOutbound) Close() error                         { return h.stack.Close() }
func (h *awgOutbound) SenderSettings() *serial.TypedMessage { return nil }
func (h *awgOutbound) ProxySettings() *serial.TypedMessage  { return nil }
func (h *awgOutbound) Dispatch(ctx context.Context, link *transport.Link) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	idle := signal.CancelAfterInactivity(ctx, cancel, 5*time.Minute)
	defer idle.SetTimeout(0)
	defer common.Close(link.Writer)
	defer common.Interrupt(link.Reader)
	outs := session.OutboundsFromContext(ctx)
	if len(outs) == 0 {
		return
	}
	dest := outs[len(outs)-1].Target
	conn, e := h.stack.DialContext(ctx, dest.Network.SystemString(), dest.NetAddr())
	if e != nil {
		return
	}
	defer conn.Close()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-done:
		}
	}()
	var reader buf.Reader = buf.NewReader(conn)
	if dest.Network.SystemString() == "udp" {
		reader = buf.NewPacketReader(conn)
		idle.SetTimeout(30 * time.Second)
	}
	task.Run(ctx, func() error {
		e := buf.Copy(link.Reader, buf.NewWriter(conn), buf.UpdateActivity(idle))
		if c, ok := conn.(interface{ CloseWrite() error }); ok {
			c.CloseWrite()
		}
		return e
	}, func() error { return buf.Copy(reader, link.Writer, buf.UpdateActivity(idle)) })
}
