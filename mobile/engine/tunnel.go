package engine

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/syntr0x79/qntw-backend/internal/awgprobe"
	"github.com/syntr0x79/qntw-backend/internal/mobileprobe/wire"
	"github.com/xtls/xray-core/common/platform"
	"github.com/xtls/xray-core/core"
	xout "github.com/xtls/xray-core/features/outbound"
	"golang.org/x/sys/unix"
	"google.golang.org/protobuf/proto"
)

var vpn struct {
	sync.Mutex
	closer io.Closer
	fd     int
}

type xrayTunnel struct {
	core    *core.Instance
	fd      int
	sockets *socketOwner
}

func (t *xrayTunnel) Close() error {
	t.sockets.close()
	err := t.core.Close()
	unix.Close(t.fd)
	return err
}

// StartVPN duplicates the Android-owned descriptor; StopVPN closes only its copy.
// Profiles use the same versioned outbound/config payload as the probe jobs.
func StartVPN(fd int, jobJSON string, network Network) error {
	vpn.Lock()
	defer vpn.Unlock()
	if vpn.closer != nil {
		return errors.New("VPN already running")
	}
	if fd < 0 || network == nil {
		return errors.New("TUN and physical network required")
	}
	if len(jobJSON) > 256<<10 {
		return errors.New("profile too large")
	}
	var job wire.Job
	if err := json.Unmarshal([]byte(jobJSON), &job); err != nil {
		return errors.New("invalid profile")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	copyFD, err := unix.Dup(fd)
	if err != nil {
		return err
	}
	state.Lock()
	state.tunnelNetwork = network
	state.Unlock()
	success := false
	defer func() {
		if !success {
			unix.Close(copyFD)
			state.Lock()
			state.tunnelNetwork = nil
			state.Unlock()
		}
	}()
	owner := newSocketOwner(context.Background(), network)
	defer func() {
		if !success {
			owner.close()
		}
	}()
	policy := currentRouting()
	if job.Engine == "amneziawg" {
		for _, line := range strings.Split(job.AWGConf, "\n") {
			key, value, ok := strings.Cut(line, "=")
			if ok && strings.EqualFold(strings.TrimSpace(key), "DNS") {
				for _, s := range strings.Split(value, ",") {
					if ip, e := netip.ParseAddr(strings.TrimSpace(s)); e == nil {
						policy.ForcedVPN = append(policy.ForcedVPN, ip.String())
					}
				}
			}
		}
	}
	var stack *awgprobe.Stack
	if job.Engine == "amneziawg" && (policy.Enabled || len(policy.Exceptions) > 0) {
		stack, err = awgprobe.OpenStack(ctx, job.AWGConf, awgprobe.Options{Resolve: func(ctx context.Context, host string) (netip.Addr, error) {
			ip, e := resolve(ctx, network, host)
			if e != nil {
				return netip.Addr{}, e
			}
			return netip.ParseAddr(ip)
		}, Listen: func(kind string, addr *net.UDPAddr) (*net.UDPConn, error) {
			lc := net.ListenConfig{Control: func(_, _ string, raw syscall.RawConn) error { return prepare(raw, network) }}
			c, e := lc.ListenPacket(ctx, kind, addr.String())
			if e != nil {
				return nil, e
			}
			return c.(*net.UDPConn), nil
		}})
		if err != nil {
			return err
		}
		defer func() {
			if !success {
				stack.Close()
			}
		}()
	}
	kind := job.Engine
	if stack != nil {
		kind = "xray"
	}
	switch kind {
	case "xray":
		var outbound core.OutboundHandlerConfig
		if stack == nil {
			if err = proto.Unmarshal(job.Outbound, &outbound); err != nil {
				return errors.New("invalid Xray outbound")
			}
			if err = resolveOutboundContext(ctx, &outbound, network); err != nil {
				return err
			}
		}
		outbound.Tag = "vpn"
		os.Setenv(platform.TunFdKey, strconv.Itoa(copyFD))
		config, err := core.LoadConfig("json", strings.NewReader(tunnelDocument(policy)))
		if err != nil {
			return err
		}
		if stack == nil {
			config.Outbound[0] = &outbound
		}
		instance, err := core.NewWithContext(context.WithValue(context.Background(), socketOwnerKey{}, owner), config)
		if err != nil {
			return err
		}
		if stack != nil {
			manager := instance.GetFeature(xout.ManagerType()).(xout.Manager)
			manager.RemoveHandler(ctx, "vpn")
			if err = manager.AddHandler(ctx, &awgOutbound{stack: stack}); err != nil {
				instance.Close()
				return err
			}
		}
		state.Lock()
		state.tunnelCore, state.tunnelOwner = instance, owner
		state.Unlock()
		defer func() {
			if !success {
				owner.close()
				state.Lock()
				if state.tunnelCore == instance {
					state.tunnelCore, state.tunnelOwner = nil, nil
				}
				state.Unlock()
			}
		}()
		if err = instance.Start(); err != nil {
			instance.Close()
			return err
		}
		vpn.closer = &xrayTunnel{core: instance, fd: copyFD, sockets: owner}
	case "amneziawg":
		options := awgprobe.Options{Resolve: func(ctx context.Context, host string) (netip.Addr, error) {
			ip, err := resolve(ctx, network, host)
			if err != nil {
				return netip.Addr{}, err
			}
			return netip.ParseAddr(ip)
		}, Listen: func(kind string, addr *net.UDPAddr) (*net.UDPConn, error) {
			lc := net.ListenConfig{Control: func(_, _ string, raw syscall.RawConn) error { return prepare(raw, network) }}
			c, err := lc.ListenPacket(ctx, kind, addr.String())
			if err != nil {
				return nil, err
			}
			return c.(*net.UDPConn), nil
		}}
		closer, err := awgprobe.StartTunnel(ctx, copyFD, job.AWGConf, options)
		if err != nil {
			return err
		}
		unix.Close(copyFD)
		vpn.closer = closer
	default:
		return errors.New("unsupported VPN engine")
	}
	success = true
	return nil
}
func StopVPN() {
	vpn.Lock()
	defer vpn.Unlock()
	if vpn.closer != nil {
		vpn.closer.Close()
		vpn.closer = nil
	}
	state.Lock()
	state.tunnelNetwork = nil
	state.tunnelCore, state.tunnelOwner = nil, nil
	state.Unlock()
}
