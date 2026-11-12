//go:build linux

package tun

import (
	"errors"
	"fmt"
	"net"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// PolicyRoutes puts the tunnel in its own routing table so a more-specific
// route in the main table cannot silently send application traffic outside it.
// Only sockets explicitly bound to the original interface use the main table.
type PolicyRoutes struct {
	routes []netlink.Route
	rules  []netlink.Rule
}

const policyTable = 52952
const bypassPriority = 10000
const tunnelPriority = 10001

// InstallPolicy requires an otherwise unused table and rule priorities. It
// refuses existing higher-priority policy rules instead of guessing their effect.
func (d *Device) InstallPolicy(physical string) (_ *PolicyRoutes, err error) {
	if d == nil || d.File == nil || physical == "" || physical == d.Name {
		return nil, errors.New("invalid policy configuration")
	}
	iface, err := net.InterfaceByName(physical)
	if err != nil || iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
		return nil, errors.New("physical network unavailable")
	}
	link, err := netlink.LinkByName(d.Name)
	if err != nil {
		return nil, err
	}
	p := &PolicyRoutes{}
	defer func() {
		if err != nil {
			err = errors.Join(err, p.Remove())
		}
	}()
	for _, family := range []int{netlink.FAMILY_V4, netlink.FAMILY_V6} {
		rules, e := netlink.RuleList(family)
		if e != nil {
			return nil, e
		}
		for _, r := range rules {
			if (r.Priority == 0 && (r.Table != unix.RT_TABLE_LOCAL || r.Src != nil || r.Dst != nil || r.IifName != "" || r.OifName != "" || r.Mark != 0)) || (r.Priority > 0 && r.Priority <= tunnelPriority) {
				return nil, fmt.Errorf("conflicting network policy at priority %d", r.Priority)
			}
		}
		routes, e := netlink.RouteListFiltered(family, &netlink.Route{Table: policyTable}, netlink.RT_FILTER_TABLE)
		if e != nil {
			return nil, e
		}
		if len(routes) != 0 {
			return nil, errors.New("VPN routing table already in use")
		}
		cidr := "0.0.0.0/0"
		if family == netlink.FAMILY_V6 {
			cidr = "::/0"
		}
		_, dst, _ := net.ParseCIDR(cidr)
		route := netlink.Route{LinkIndex: link.Attrs().Index, Dst: dst, Table: policyTable, Scope: netlink.SCOPE_LINK}
		if e = netlink.RouteAdd(&route); e != nil {
			return nil, fmt.Errorf("install policy route: %w", e)
		}
		p.routes = append(p.routes, route)
		bypass := netlink.NewRule()
		bypass.Family = family
		bypass.Table = unix.RT_TABLE_MAIN
		bypass.Priority = bypassPriority
		bypass.OifName = physical
		if e = netlink.RuleAdd(bypass); e != nil {
			return nil, fmt.Errorf("install network bypass: %w", e)
		}
		p.rules = append(p.rules, *bypass)
		rule := netlink.NewRule()
		rule.Family = family
		rule.Table = policyTable
		rule.Priority = tunnelPriority
		if e = netlink.RuleAdd(rule); e != nil {
			return nil, fmt.Errorf("install tunnel rule: %w", e)
		}
		p.rules = append(p.rules, *rule)
	}
	return p, nil
}

// Remove only removes rules and routes installed by this instance.
func (p *PolicyRoutes) Remove() error {
	if p == nil {
		return nil
	}
	var result error
	for i := len(p.rules) - 1; i >= 0; i-- {
		if e := netlink.RuleDel(&p.rules[i]); e != nil && !errors.Is(e, unix.ENOENT) && !errors.Is(e, unix.ESRCH) {
			result = errors.Join(result, e)
		}
	}
	p.rules = nil
	for i := len(p.routes) - 1; i >= 0; i-- {
		if e := netlink.RouteDel(&p.routes[i]); e != nil && !errors.Is(e, unix.ENOENT) && !errors.Is(e, unix.ESRCH) {
			result = errors.Join(result, e)
		}
	}
	p.routes = nil
	return result
}
