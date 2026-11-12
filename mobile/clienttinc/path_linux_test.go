//go:build linux && !android

package clienttinc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNativeTCPCompatibilityKeepsEveryIngressAndInnerUDP(t *testing.T) {
	c := configFixture(t)
	second := c.Ingresses[0]
	second.NodeID, second.Name, second.Endpoint = uuid.New(), "ingress2", "192.0.2.2:17445"
	second.PublicKey = "XRgcUuA74GEho1v5gNnOTrFrgx4ZJNj9jRyQ0ulZbLE"
	c.Ingresses = append(c.Ingresses, second)
	if err := ValidateConfig(c, time.Now()); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "hosts"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := renderNative(dir, c.Ingresses[0].PublicKey, c); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "tinc.conf"))
	if err != nil {
		t.Fatal(err)
	}
	settings := map[string][]string{}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			key, value = strings.TrimSpace(key), strings.TrimSpace(value)
			settings[key] = append(settings[key], value)
		}
	}
	for key, want := range map[string]string{"TCPOnly": "yes", "PingInterval": "15", "PingTimeout": "15", "ExperimentalProtocol": "yes", "StrictSubnets": "yes", "Forwarding": "off", "DeviceType": "fd"} {
		if got := settings[key]; len(got) != 1 || got[0] != want {
			t.Fatalf("native compatibility setting %s = %v", key, got)
		}
	}
	if got := settings["ConnectTo"]; len(got) != 2 || got[0] != "ingress1" || got[1] != "ingress2" {
		t.Fatalf("compatibility lost an ingress: %v", got)
	}
	var inner struct {
		Inbounds []struct {
			Protocol string `json:"protocol"`
			Settings struct {
				UDP bool `json:"udp"`
			} `json:"settings"`
		} `json:"inbounds"`
		Outbounds []struct {
			Protocol string `json:"protocol"`
			Mux      struct {
				Enabled         bool   `json:"enabled"`
				Concurrency     int    `json:"concurrency"`
				XUDPConcurrency int    `json:"xudpConcurrency"`
				UDP443          string `json:"xudpProxyUDP443"`
			} `json:"mux"`
			StreamSettings struct {
				Security    string `json:"security"`
				TLSSettings struct {
					ServerName        string `json:"serverName"`
					DisableSystemRoot bool   `json:"disableSystemRoot"`
				} `json:"tlsSettings"`
			} `json:"streamSettings"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal(xrayDocument(c, true), &inner); err != nil {
		t.Fatal(err)
	}
	if len(inner.Inbounds) != 2 || inner.Inbounds[0].Protocol != "socks" || !inner.Inbounds[0].Settings.UDP || inner.Inbounds[1].Protocol != "tun" {
		t.Fatal("outer TCP compatibility removed system TUN or user UDP")
	}
	if len(inner.Outbounds) < 1 || inner.Outbounds[0].Protocol != "vless" || inner.Outbounds[0].StreamSettings.Security != "tls" || inner.Outbounds[0].StreamSettings.TLSSettings.ServerName != c.Assignment.TLSName || !inner.Outbounds[0].StreamSettings.TLSSettings.DisableSystemRoot {
		t.Fatal("outer TCP compatibility weakened authenticated inner transport")
	}
	if mux := inner.Outbounds[0].Mux; !mux.Enabled || mux.Concurrency != 8 || mux.XUDPConcurrency != 0 || mux.UDP443 != "allow" {
		t.Fatal("client must pack ordinary TCP/UDP mux sessions without enabling persistent XUDP workers or dropping UDP443")
	}
}
