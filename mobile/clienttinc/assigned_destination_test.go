package clienttinc

import (
	"net/netip"
	"testing"

	xnet "github.com/xtls/xray-core/common/net"
)

func TestAssignedDestinationNeverFallsBackOutsideGrant(t *testing.T) {
	target := netip.MustParseAddrPort("10.76.0.10:18443")
	self := netip.MustParseAddr("10.77.4.1")
	valid := xnet.TCPDestination(xnet.ParseAddress(target.Addr().String()), xnet.Port(target.Port()))
	if err := validateAssignedDestination(target, self, nil, valid); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name        string
		source      xnet.Address
		destination xnet.Destination
	}{
		{name: "public destination", destination: xnet.TCPDestination(xnet.ParseAddress("1.1.1.1"), 18443)},
		{name: "other assigned exit", destination: xnet.TCPDestination(xnet.ParseAddress("10.76.0.11"), 18443)},
		{name: "other port", destination: xnet.TCPDestination(valid.Address, 443)},
		{name: "UDP transport", destination: xnet.UDPDestination(valid.Address, 18443)},
		{name: "hostname resolution forbidden", destination: xnet.TCPDestination(xnet.DomainAddress("10.76.0.10"), 18443)},
		{name: "IPv6 transport", destination: xnet.TCPDestination(xnet.ParseAddress("2001:db8::1"), 18443)},
		{name: "foreign source", source: xnet.ParseAddress("10.77.4.2"), destination: valid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if validateAssignedDestination(target, self, tc.source, tc.destination) == nil {
				t.Fatal("unauthorized connection accepted")
			}
		})
	}
}
