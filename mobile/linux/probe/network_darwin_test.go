//go:build darwin && !ios

package probe

import (
	"net"
	"testing"
)

func TestParseDarwinKernelRoute(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   darwinRoute
	}{
		{
			name: "unscoped Wi-Fi default",
			output: `   route to: 1.1.1.1
destination: default
       mask: default
    gateway: 192.168.1.1
  interface: en0
      flags: <UP,GATEWAY,DONE,STATIC,PRCLONING,GLOBAL>
 recvpipe sendpipe ssthresh rtt,msec rttvar hopcount mtu expire
       0        0        0        0      0        0 1500      0
`,
			want: darwinRoute{iface: "en0", gateway: "192.168.1.1"},
		},
		{
			name:   "bridge selected by kernel remains valid",
			output: "destination: default\n    gateway: 10.0.0.1\n  interface: bridge101\n",
			want:   darwinRoute{iface: "bridge101", gateway: "10.0.0.1"},
		},
		{
			name:   "direct route without gateway",
			output: "destination: 1.1.1.1\n  interface: ppp0\n",
			want:   darwinRoute{iface: "ppp0"},
		},
		{
			name:   "link gateway",
			output: "destination: 1.1.1.1\n    gateway: link#12\n  interface: bridge0\n",
			want:   darwinRoute{iface: "bridge0", gateway: "link#12"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseDarwinRoute(tt.output)
			if err != nil || got != tt.want {
				t.Fatalf("route=%+v err=%v want=%+v", got, err, tt.want)
			}
		})
	}
}

func TestParseDarwinKernelRouteRejectsAmbiguousState(t *testing.T) {
	for _, output := range []string{
		"",
		"route: route has not been found\n",
		"gateway: 192.168.1.1\n",
		"interface:\n",
		"interface: en0 bridge101\n",
		"interface: en0\ninterface: bridge101\n",
		"interface: en0\ngateway:\n",
		"interface: en0\ngateway: 192.168.1.1\ngateway: 10.0.0.1\n",
	} {
		if got, err := parseDarwinRoute(output); err == nil {
			t.Errorf("accepted ambiguous output %q: %+v", output, got)
		}
	}
}

func TestDarwinRouteFingerprintDetectsNetworkChanges(t *testing.T) {
	n := &network{name: "en0"}
	route := darwinRoute{iface: "en0", gateway: "192.168.1.1"}
	first := &net.IPNet{IP: net.ParseIP("192.168.1.20"), Mask: net.CIDRMask(24, 32)}
	second := &net.IPNet{IP: net.ParseIP("fe80::1"), Mask: net.CIDRMask(64, 128)}
	baseline, err := n.routeFingerprint(route, []net.Addr{first, second})
	if err != nil {
		t.Fatal(err)
	}
	reordered, err := n.routeFingerprint(route, []net.Addr{second, first})
	if err != nil || reordered != baseline {
		t.Fatalf("address order changed fingerprint: %q err=%v", reordered, err)
	}
	changedGateway, err := n.routeFingerprint(darwinRoute{iface: "en0", gateway: "192.168.1.2"}, []net.Addr{first, second})
	if err != nil || changedGateway == baseline {
		t.Fatalf("gateway change was not detected: %q err=%v", changedGateway, err)
	}
	changedAddresses, err := n.routeFingerprint(route, []net.Addr{first})
	if err != nil || changedAddresses == baseline {
		t.Fatalf("address change was not detected: %q err=%v", changedAddresses, err)
	}
	if _, err := n.routeFingerprint(darwinRoute{iface: "bridge101", gateway: route.gateway}, []net.Addr{first, second}); err == nil {
		t.Fatal("interface change was not rejected")
	}
}
