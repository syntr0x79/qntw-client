package clienttinc

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"github.com/syntr0x79/qntw-client/internal/agentproto"
	wire "github.com/syntr0x79/qntw-client/internal/clienttincwire"
)

func identity(ctx context.Context, ctl, dir string) (string, error) {
	if e := os.MkdirAll(filepath.Join(dir, "hosts"), 0700); e != nil {
		return "", e
	}
	path := filepath.Join(dir, "public-key")
	if b, e := os.ReadFile(path); e == nil {
		key := strings.TrimSpace(string(b))
		if !agentproto.ValidTincPubkey(key) {
			return "", errors.New("invalid saved tinc public key")
		}
		return key, nil
	}
	if _, e := os.Stat(filepath.Join(dir, "ed25519_key.priv")); e == nil {
		return "", errors.New("tinc identity exists without its saved public key; refusing to rotate")
	}
	if _, e := command(ctx, "", ctl, "-c", dir, "init", "bootstrap"); e != nil {
		return "", errors.New("native tinc identity generation failed")
	}
	b, e := os.ReadFile(filepath.Join(dir, "hosts", "bootstrap"))
	if e != nil {
		return "", e
	}
	for _, line := range strings.Split(string(b), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok && strings.EqualFold(strings.TrimSpace(key), "Ed25519PublicKey") {
			public := strings.TrimSpace(value)
			if !agentproto.ValidTincPubkey(public) {
				break
			}
			if e = os.WriteFile(path, []byte(public+"\n"), 0600); e != nil {
				return "", e
			}
			return public, nil
		}
	}
	return "", errors.New("native tinc did not produce a valid public key")
}

func renderNative(dir, public string, c wire.ClientConfig) error {
	for _, name := range []string{"tinc-up", "tinc-down", "host-up", "host-down", "subnet-up", "subnet-down", "tinc-up.bat", "tinc-down.bat", "host-up.bat", "host-down.bat", "subnet-up.bat", "subnet-down.bat", "tinc-up.cmd", "tinc-down.cmd"} {
		if e := os.Remove(filepath.Join(dir, name)); e != nil && !errors.Is(e, os.ErrNotExist) {
			return e
		}
	}
	entries, e := os.ReadDir(filepath.Join(dir, "hosts"))
	if e != nil {
		return e
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return errors.New("unexpected nested host directory")
		}
		if e = os.Remove(filepath.Join(dir, "hosts", entry.Name())); e != nil {
			return e
		}
	}
	var conf strings.Builder
	// Keep the Linux prototype on authenticated TCP meta links until mixed
	// UDP/TCP data-session recovery is validated across NAT and VM pauses.
	// Inner VLESS still carries user TCP and UDP; this only selects tinc's
	// outer transport. A short VM scheduling delay must not expire a handshake.
	// tinc clamps PingTimeout to PingInterval, so both must be at least 15s.
	fmt.Fprintf(&conf, "Name = %s\nDeviceType = wintun\nInterface = qtm0\nMode = router\nAddressFamily = ipv4\nPort = 0\nTCPOnly = yes\nAutoConnect = no\nLocalDiscovery = no\nBroadcast = no\nStrictSubnets = yes\nForwarding = off\nExperimentalProtocol = yes\nPingInterval = 15\nPingTimeout = 15\nMaxTimeout = 10\n", c.Self.Name)
	self := fmt.Sprintf("Ed25519PublicKey = %s\nSubnet = %s/32\nIndirectData = yes\n", public, c.Self.Address)
	if e = os.WriteFile(filepath.Join(dir, "hosts", c.Self.Name), []byte(self), 0600); e != nil {
		return e
	}
	for _, in := range c.Ingresses {
		endpoint := netip.MustParseAddrPort(in.Endpoint)
		host := fmt.Sprintf("Ed25519PublicKey = %s\nAddress = %s\nPort = %d\nSubnet = %s/32\nSubnet = %s#%d\nIndirectData = yes\n", in.PublicKey, endpoint.Addr(), endpoint.Port(), in.Address, in.RoutePrefix, in.Weight)
		if e = os.WriteFile(filepath.Join(dir, "hosts", in.Name), []byte(host), 0600); e != nil {
			return e
		}
		fmt.Fprintf(&conf, "ConnectTo = %s\n", in.Name)
	}
	return os.WriteFile(filepath.Join(dir, "tinc.conf"), []byte(conf.String()), 0600)
}
