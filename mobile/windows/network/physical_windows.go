//go:build windows

package network

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

// Physical is an immutable uplink snapshot. Discovery does not require admin
// rights and does not install filters, change routes, or modify DNS.
type Physical struct {
	name        string
	index       uint32
	luid        winipcfg.LUID
	dns         []netip.Addr
	fingerprint string
}

type physicalSnapshot struct {
	Name      string
	Index     uint32
	LUID      uint64
	Gateway   string
	Addresses []string
	DNS       []string
}

func Discover(ctx context.Context, name string) (*Physical, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	adapters, err := winipcfg.GetAdaptersAddresses(windows.AF_INET, winipcfg.GAAFlagIncludeGateways)
	if err != nil {
		return nil, errors.New("cannot read physical network adapters")
	}
	routes, err := winipcfg.GetIPForwardTable2(windows.AF_INET)
	if err != nil {
		return nil, errors.New("cannot read physical default route")
	}
	var chosen *winipcfg.IPAdapterAddresses
	var gateway netip.Addr
	best := ^uint64(0)
	for _, route := range routes {
		prefix := route.DestinationPrefix.Prefix()
		if !prefix.IsValid() || !prefix.Addr().Is4() || prefix.Bits() != 0 {
			continue
		}
		for _, adapter := range adapters {
			friendly := adapter.FriendlyName()
			if adapter.LUID != route.InterfaceLUID || adapter.OperStatus != winipcfg.IfOperStatusUp ||
				adapter.IfType == winipcfg.IfTypeSoftwareLoopback || ownedName(friendly) || (name != "" && friendly != name) {
				continue
			}
			metric := uint64(route.Metric) + uint64(adapter.Ipv4Metric)
			if metric < best {
				best, chosen, gateway = metric, adapter, route.NextHop.Addr()
			}
		}
	}
	if chosen == nil || chosen.IfIndex == 0 {
		return nil, errors.New("no usable physical IPv4 default route")
	}
	snapshot := physicalSnapshot{Name: chosen.FriendlyName(), Index: chosen.IfIndex, LUID: uint64(chosen.LUID), Gateway: gateway.String()}
	for address := chosen.FirstUnicastAddress; address != nil; address = address.Next {
		ip, ok := netip.AddrFromSlice(address.Address.IP())
		if ok && ip.Is4() && !ip.IsUnspecified() {
			snapshot.Addresses = append(snapshot.Addresses, ip.String())
		}
	}
	var dns []netip.Addr
	for address := chosen.FirstDNSServerAddress; address != nil; address = address.Next {
		ip, ok := netip.AddrFromSlice(address.Address.IP())
		if ok && ip.Is4() && !ip.IsUnspecified() && !ip.IsLoopback() {
			dns = append(dns, ip)
			snapshot.DNS = append(snapshot.DNS, ip.String())
		}
	}
	if len(snapshot.Addresses) == 0 || len(dns) == 0 {
		return nil, errors.New("physical network has no usable IPv4 address or DNS resolver")
	}
	slices.Sort(snapshot.Addresses)
	slices.Sort(snapshot.DNS)
	document, _ := json.Marshal(snapshot)
	hash := sha256.Sum256(document)
	return &Physical{name: snapshot.Name, index: snapshot.Index, luid: chosen.LUID, dns: dns, fingerprint: hex.EncodeToString(hash[:])}, nil
}

func ownedName(name string) bool {
	return strings.EqualFold(name, "qtc0") || strings.EqualFold(name, "qtm0")
}
func (p *Physical) Name() string  { return p.name }
func (p *Physical) Index() uint32 { return p.index }
func (p *Physical) LUID() uint64  { return uint64(p.luid) }

// Snapshot returns the current fingerprint so probe epochs can detect DHCP,
// uplink or resolver changes, including reuse of an existing interface index.
func (p *Physical) Snapshot() (string, error) {
	current, err := Discover(context.Background(), p.name)
	if err != nil {
		return "", err
	}
	return current.fingerprint, nil
}

func (p *Physical) valid() error {
	current, err := p.Snapshot()
	if err != nil || current != p.fingerprint {
		return errors.New("physical network changed; reconnect required")
	}
	return nil
}

func (p *Physical) PrepareSocket(fd int) error {
	if err := p.valid(); err != nil {
		return err
	}
	return pinIPv4Socket(windows.Handle(fd), p.index)
}

func (p *Physical) control(_, address string, raw syscall.RawConn) error {
	host, _, _ := net.SplitHostPort(address)
	if ip, err := netip.ParseAddr(host); err == nil && ip.IsLoopback() {
		return nil
	}
	var callErr error
	if err := raw.Control(func(fd uintptr) { callErr = p.PrepareSocket(int(fd)) }); err != nil {
		return err
	}
	return callErr
}

func (p *Physical) resolver() *net.Resolver {
	return &net.Resolver{PreferGo: true, StrictErrors: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		var last error
		for _, dns := range p.dns {
			dial := net.Dialer{Timeout: 4 * time.Second, Control: p.control}
			c, err := dial.DialContext(ctx, network+"4", net.JoinHostPort(dns.String(), "53"))
			if err == nil {
				return c, nil
			}
			last = err
		}
		return nil, last
	}}
}

func (p *Physical) Resolve(host string) (string, error) {
	if address, err := netip.ParseAddr(host); err == nil {
		if !address.Is4() {
			return "", errors.New("physical IPv6 transport is unavailable")
		}
		return address.String(), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	addresses, err := p.resolver().LookupNetIP(ctx, "ip4", host)
	if err != nil || len(addresses) == 0 {
		return "", errors.New("cannot resolve host on physical network")
	}
	return addresses[0].String(), nil
}

func (p *Physical) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if !strings.HasSuffix(network, "4") {
		if network != "tcp" && network != "udp" {
			return nil, errors.New("unsupported physical transport")
		}
		network += "4"
	}
	dial := net.Dialer{Timeout: 10 * time.Second, Control: p.control, Resolver: p.resolver()}
	return dial.DialContext(ctx, network, address)
}

func (p *Physical) Transport() *http.Transport {
	return &http.Transport{Proxy: nil, DialContext: p.DialContext, ForceAttemptHTTP2: true,
		TLSHandshakeTimeout: 10 * time.Second, IdleConnTimeout: 30 * time.Second,
		MaxIdleConns: 4, MaxIdleConnsPerHost: 2}
}

// NativeEnvironment is deliberately one numeric value, never a shell command.
func (p *Physical) NativeEnvironment() string {
	return "QUANTWAY_PHYSICAL_IFINDEX=" + strconv.FormatUint(uint64(p.index), 10)
}
