package vpn

import "encoding/json"

const tunnelName = "qtc0"
const tunnelAddress = "198.18.94.1"

// Only this fixed document creates privileged resources. Profile input replaces
// one validated client outbound, never inbound listeners, logs or app features.
func tunnelDocument() []byte {
	document := map[string]any{
		"log":       map[string]any{"loglevel": "none"},
		"inbounds":  []any{map[string]any{"tag": "windows-tun", "protocol": "tun", "settings": map[string]any{"name": tunnelName, "desc": "Quantway", "mtu": 1280, "gateway": []string{tunnelAddress + "/30"}, "autoSystemRoutingTable": []string{}}, "sniffing": map[string]any{"enabled": true, "destOverride": []string{"http", "tls", "quic"}, "routeOnly": true}}},
		"outbounds": []any{map[string]any{"protocol": "blackhole", "tag": "vpn"}, map[string]any{"protocol": "blackhole", "tag": "block"}},
		"routing":   map[string]any{"domainStrategy": "AsIs", "rules": []any{map[string]any{"type": "field", "outboundTag": "block", "ip": []string{"::/0"}}, map[string]any{"type": "field", "outboundTag": "vpn", "network": "tcp,udp"}}},
	}
	raw, _ := json.Marshal(document)
	return raw
}
