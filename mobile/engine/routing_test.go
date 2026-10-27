package engine

import (
	"encoding/json"
	"github.com/xtls/xray-core/core"
	"strings"
	"testing"
)

func TestRoutingOrderAndNoExternalAssets(t *testing.T) {
	p := mobileRouting{Enabled: true, TunnelDomains: []string{"domain:googlevideo.com"}, DirectDomains: []string{"domain:example.ru"}, DirectCIDRs: []string{"10.0.0.0/8"}, Exceptions: []string{"local.example"}, ForcedVPN: []string{"10.66.0.1"}}
	text := tunnelDocument(p)
	var v struct {
		Routing struct {
			Rules []map[string]any `json:"rules"`
		} `json:"routing"`
	}
	if e := json.Unmarshal([]byte(text), &v); e != nil {
		t.Fatal(e)
	}
	want := []string{"direct", "vpn", "vpn", "direct", "direct", "block", "vpn"}
	for i, tag := range want {
		if v.Routing.Rules[i]["outboundTag"] != tag {
			t.Fatalf("rule %d priority", i)
		}
	}
	if _, e := core.LoadConfig("json", strings.NewReader(text)); e != nil {
		t.Fatal(e)
	}
	if e := ConfigureRouting(`{"enabled":true,"exceptions":["https://invalid/"]}`); e == nil {
		t.Fatal("invalid exception accepted")
	}
	ConfigureRouting(`{}`)
}
func TestImportSubscriptionStrictAndDeduplicated(t *testing.T) {
	text := "vless://11111111-2222-4333-8444-555555555555@127.0.0.1:443?encryption=none&security=none&type=tcp#Local"
	result, e := ImportSubscription(text + "\n" + text + "\ntrojan://unsupported")
	if e != nil {
		t.Fatal(e)
	}
	var v struct {
		Profiles []map[string]any `json:"profiles"`
		Skipped  int              `json:"skipped"`
	}
	if e = json.Unmarshal([]byte(result), &v); e != nil {
		t.Fatal(e)
	}
	if len(v.Profiles) != 1 || v.Skipped != 1 || v.Profiles[0]["protocol"] != "vless" {
		t.Fatal("import metadata/deduplication")
	}
	if _, e = ImportSubscription("hy2://password@vpn.example:443?insecure=1"); e == nil {
		t.Fatal("insecure TLS subscription accepted")
	}
}
