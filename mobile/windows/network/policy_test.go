package network

import (
	"errors"
	"net/netip"
	"reflect"
	"testing"
)

func TestRoutesRollbackAndRetainFailedUndo(t *testing.T) {
	addFailure := errors.New("second split route conflicts")
	undoFailure := errors.New("adapter temporarily unavailable")
	for _, failUndo := range []bool{false, true} {
		t.Run(map[bool]string{false: "clean rollback", true: "retained for cleanup"}[failUndo], func(t *testing.T) {
			var attempted, removed []netip.Prefix
			retained, err := installRoutes(func(prefix netip.Prefix) error {
				attempted = append(attempted, prefix)
				if len(attempted) == 2 {
					return addFailure
				}
				return nil
			}, func(prefix netip.Prefix) error {
				removed = append(removed, prefix)
				if failUndo {
					return undoFailure
				}
				return nil
			})
			if !errors.Is(err, addFailure) || errors.Is(err, undoFailure) != failUndo {
				t.Fatalf("wrong failure chain: %v", err)
			}
			if !reflect.DeepEqual(attempted, SystemPrefixes()) || !reflect.DeepEqual(removed, SystemPrefixes()[:1]) {
				t.Fatalf("bad transaction attempts %v / %v", attempted, removed)
			}
			if failUndo && !reflect.DeepEqual(retained, removed) {
				t.Fatalf("lost route ownership: %v", retained)
			}
			if !failUndo && len(retained) != 0 {
				t.Fatalf("already removed routes retained: %v", retained)
			}
		})
	}
}

func TestRoutesKeepOriginalDefaultAndNeverRemoveOnSuccess(t *testing.T) {
	var added []netip.Prefix
	retained, err := installRoutes(func(prefix netip.Prefix) error {
		if prefix.Bits() == 0 || !prefix.Addr().Is4() {
			t.Fatalf("unexpected physical default mutation: %v", prefix)
		}
		added = append(added, prefix)
		return nil
	}, func(prefix netip.Prefix) error { t.Fatalf("removed successful route %v", prefix); return nil })
	if err != nil || !reflect.DeepEqual(retained, added) || len(added) != 2 {
		t.Fatalf("bad route ownership %v: %v", retained, err)
	}
}

func TestFirstRouteConflictNeverDeletesAnExistingRoute(t *testing.T) {
	conflict := errors.New("route belongs to another owner")
	attempts := 0
	retained, err := installRoutes(func(netip.Prefix) error {
		attempts++
		return conflict
	}, func(prefix netip.Prefix) error {
		t.Fatalf("attempted to remove a route not installed by this session: %v", prefix)
		return nil
	})
	if !errors.Is(err, conflict) || attempts != 1 || len(retained) != 0 {
		t.Fatalf("bad first-conflict handling: %v %v %d", retained, err, attempts)
	}
}

func TestMeshPeerPolicyRejectsUnauthorizedEndpointShapes(t *testing.T) {
	valid := netip.MustParseAddrPort("203.0.113.20:17448")
	if err := ValidatePeers([]netip.AddrPort{valid}); err != nil {
		t.Fatal(err)
	}
	for _, peer := range []netip.AddrPort{netip.MustParseAddrPort("127.0.0.1:17448"), netip.MustParseAddrPort("0.0.0.0:17448"), netip.MustParseAddrPort("224.1.1.1:17448"), netip.MustParseAddrPort("[2001:db8::1]:17448"), netip.MustParseAddrPort("203.0.113.20:0"), {}} {
		if ValidatePeers([]netip.AddrPort{peer}) == nil {
			t.Errorf("accepted unusable ingress %v", peer)
		}
	}
	if ValidatePeers([]netip.AddrPort{valid, valid}) == nil {
		t.Fatal("accepted duplicate native endpoint")
	}
	if ValidatePeers(make([]netip.AddrPort, 129)) == nil {
		t.Fatal("accepted unbounded native policy")
	}
	if ValidatePeers(nil) != nil {
		t.Fatal("cannot revoke all native permits")
	}
}

func TestDNSPolicyRejectsLocalAndIPv6Resolvers(t *testing.T) {
	if err := ValidateDNS([]netip.Addr{netip.MustParseAddr("9.9.9.9")}); err != nil {
		t.Fatal(err)
	}
	for _, ip := range []netip.Addr{netip.MustParseAddr("127.0.0.53"), netip.MustParseAddr("0.0.0.0"), netip.MustParseAddr("169.254.1.1"), netip.MustParseAddr("ff02::1"), netip.MustParseAddr("::1"), {}} {
		if ValidateDNS([]netip.Addr{ip}) == nil {
			t.Errorf("accepted invalid DNS %v", ip)
		}
	}
	if ValidateDNS(nil) == nil {
		t.Fatal("accepted missing DNS")
	}
}
