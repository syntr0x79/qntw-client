package clienttinc

import (
	"errors"
	"net/netip"

	xnet "github.com/xtls/xray-core/common/net"
)

// The inner client tunnel may dial exactly one authorized VLESS endpoint. A
// hostname, UDP transport, other port, or another core cannot become a bypass.
func validateAssignedDestination(target netip.AddrPort, self netip.Addr, source xnet.Address, dest xnet.Destination) error {
	if !target.IsValid() || !target.Addr().Is4() || !netip.MustParsePrefix("10.76.0.0/24").Contains(target.Addr()) || target.Port() != 18443 || !self.Is4() || !netip.MustParsePrefix("10.77.0.0/16").Contains(self) || dest.Network != xnet.Network_TCP || !dest.Address.Family().IsIP() {
		return errors.New("unauthorized assigned tunnel destination")
	}
	ip, ok := netip.AddrFromSlice(dest.Address.IP())
	if !ok || ip.Unmap() != target.Addr() || uint16(dest.Port) != target.Port() {
		return errors.New("connection does not match the authorized exit")
	}
	if source != nil && source != xnet.AnyIP {
		if !source.Family().IsIP() {
			return errors.New("unexpected assigned source address")
		}
		ip, ok := netip.AddrFromSlice(source.IP())
		if !ok || (ip.Unmap() != self && !ip.IsUnspecified()) {
			return errors.New("assigned source address is not owned")
		}
	}
	return nil
}
