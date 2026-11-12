package network

import (
	"errors"
	"fmt"
	"net/netip"
)

func ValidatePeers(peers []netip.AddrPort) error {
	if len(peers) > 128 {
		return errors.New("too many mesh ingress endpoints")
	}
	seen := make(map[netip.AddrPort]bool, len(peers))
	for _, peer := range peers {
		ip := peer.Addr()
		if !peer.IsValid() || !ip.Is4() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLoopback() || peer.Port() == 0 || seen[peer] {
			return errors.New("invalid or duplicate mesh ingress endpoint")
		}
		seen[peer] = true
	}
	return nil
}

func ValidateDNS(addresses []netip.Addr) error {
	if len(addresses) == 0 || len(addresses) > 4 {
		return errors.New("one to four DNS servers required")
	}
	for _, ip := range addresses {
		if !ip.Is4() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			return errors.New("invalid DNS server")
		}
	}
	return nil
}

// Original default routes remain intact. IPv6 cannot escape on a physical
// adapter because the WFP policy blocks it while the IPv4 VPN is active.
func SystemPrefixes() []netip.Prefix {
	return []netip.Prefix{netip.MustParsePrefix("0.0.0.0/1"), netip.MustParsePrefix("128.0.0.0/1")}
}

// installRoutes retains ownership of any route whose rollback failed. The
// caller must keep those entries for explicit disconnect cleanup/retry.
func installRoutes(add, remove func(netip.Prefix) error) ([]netip.Prefix, error) {
	var added []netip.Prefix
	for _, prefix := range SystemPrefixes() {
		if err := add(prefix); err != nil {
			result := fmt.Errorf("install tunnel route: %w", err)
			var retained []netip.Prefix
			for i := len(added) - 1; i >= 0; i-- {
				if rollback := remove(added[i]); rollback != nil {
					result = errors.Join(result, fmt.Errorf("remove tunnel route: %w", rollback))
					retained = append(retained, added[i])
				}
			}
			return retained, result
		}
		added = append(added, prefix)
	}
	return added, nil
}
