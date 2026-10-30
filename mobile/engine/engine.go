// Package engine exposes the existing Xray and AmneziaWG probers to gomobile.
// Concurrent probes share a pinned physical network epoch because Xray socket
// controllers are process-wide; different epochs must never overlap.
package engine

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/xtls/xray-core/core"
	_ "github.com/xtls/xray-core/main/distro/all"
	"github.com/xtls/xray-core/transport/internet"
	"google.golang.org/protobuf/proto"

	"github.com/syntr0x79/qntw-backend/internal/awgprobe"
	"github.com/syntr0x79/qntw-backend/internal/mobileprobe/wire"
	"github.com/syntr0x79/qntw-backend/internal/xrayfetch"
)

// Network is implemented by Android. PrepareSocket must fail if the selected
// physical network has disappeared. Resolve returns one literal IP from its DNS.
type Network interface {
	PrepareSocket(fd int) error
	Resolve(host string) (string, error)
}

var state struct {
	sync.Mutex
	probes        map[uint64]context.CancelFunc
	nextID        uint64
	epoch         string
	changed       chan struct{}
	generation    uint64
	network       Network
	tunnelNetwork Network
	tunnelCore    *core.Instance
	tunnelOwner   *socketOwner
}

func init() {
	// Xray's default controller logs binding failures and continues dialing.
	// Our dialer propagates them and gives probes a separate socket lifetime.
	internet.UseAlternativeSystemDialer(&mobileSystemDialer{})
}

func prepare(raw syscall.RawConn, n Network) error {
	var callErr error
	if err := raw.Control(func(fd uintptr) { callErr = n.PrepareSocket(int(fd)) }); err != nil {
		return err
	}
	return callErr
}

// Version identifies both the API and the pinned protocol engines.
func Version() string { return "mobile/3 xray/" + core.Version() + " awg/3.1.20260828" }
func Capabilities() string {
	return `{"version":1,"engines":["xray","amneziawg"],"parallelism":10,"tun":true}`
}

// Cancel interrupts all probes, including those waiting for an older epoch.
// The running VPN is independent of this cancellation group.
func Cancel() {
	state.Lock()
	defer state.Unlock()
	state.generation++
	for _, cancel := range state.probes {
		cancel()
	}
	signalProbesLocked()
}

func signalProbesLocked() {
	if state.changed != nil {
		close(state.changed)
	}
	state.changed = make(chan struct{})
}

// reserveProbe keeps the process-wide Xray controller on one physical epoch.
// Legacy callers (empty epoch) remain exclusive. Capacity/epoch waits do not
// become failed routes when Cancel interrupts them.
func reserveProbe(ctx context.Context, cancel context.CancelFunc, network Network, epoch string) (func(), error) {
	state.Lock()
	generation := state.generation
	for {
		if generation != state.generation || ctx.Err() != nil {
			state.Unlock()
			return nil, context.Canceled
		}
		if len(state.probes) == 0 || (epoch != "" && state.epoch == epoch && len(state.probes) < 10) {
			if state.probes == nil {
				state.probes = make(map[uint64]context.CancelFunc)
			}
			state.nextID++
			id := state.nextID
			if len(state.probes) == 0 {
				state.network, state.epoch = network, epoch
			}
			state.probes[id] = cancel
			state.Unlock()
			return func() {
				state.Lock()
				delete(state.probes, id)
				if len(state.probes) == 0 {
					state.network, state.epoch = nil, ""
				}
				signalProbesLocked()
				state.Unlock()
			}, nil
		}
		if state.changed == nil {
			state.changed = make(chan struct{})
		}
		changed := state.changed
		state.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		}
		state.Lock()
	}
}

// Probe executes one job and returns a wire.Result. Configuration errors are
// returned as Go errors; network outcomes contain only bounded reason codes.
func Probe(jobJSON string, controlURL string, timeoutMS int, network Network) (string, error) {
	return ProbeOnNetwork(jobJSON, controlURL, timeoutMS, network, "")
}

// ProbeOnNetwork admits up to ten concurrent jobs on the same Android epoch.
// The epoch must identify the immutable physical-network snapshot, not a run.
func ProbeOnNetwork(jobJSON string, controlURL string, timeoutMS int, network Network, epoch string) (string, error) {
	if network == nil {
		return "", errors.New("network required")
	}
	if len(jobJSON) > 256<<10 {
		return "", errors.New("job too large")
	}
	var job wire.Job
	if err := json.Unmarshal([]byte(jobJSON), &job); err != nil {
		return "", errors.New("invalid job")
	}
	if job.ID == "" || (job.Engine != "xray" && job.Engine != "amneziawg") {
		return "", errors.New("unsupported job")
	}
	u, err := url.Parse(controlURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return "", errors.New("HTTPS control URL required")
	}
	if timeoutMS < 100 || timeoutMS > 30000 {
		return "", errors.New("timeout must be 100..30000 ms")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMS)*time.Millisecond)
	defer cancel()
	release, admissionErr := reserveProbe(ctx, cancel, network, epoch)
	if admissionErr != nil {
		result := wire.Result{JobID: job.ID, Outcome: "cancelled", Reason: "cancelled"}
		data, _ := json.Marshal(result)
		return string(data), nil
	}
	defer release()
	owner := newSocketOwner(ctx, network)
	ctx = context.WithValue(ctx, socketOwnerKey{}, owner)
	ctx = xrayfetch.WithInstanceObserver(ctx, func(instance *core.Instance) func() {
		rememberCore(instance)
		probeOwners.Lock()
		probeOwners.instances[instance] = owner
		probeOwners.Unlock()
		return func() { probeOwners.Lock(); delete(probeOwners.instances, instance); probeOwners.Unlock() }
	})
	stopSockets := context.AfterFunc(ctx, owner.close)
	defer stopSockets()
	defer owner.close()
	started := time.Now()
	var unprobeable bool
	switch job.Engine {
	case "xray":
		var out core.OutboundHandlerConfig
		if err = proto.Unmarshal(job.Outbound, &out); err != nil {
			unprobeable = true
			break
		}
		if err = resolveOutboundContext(ctx, &out, network); err != nil {
			unprobeable = true
			break
		}
		_, err = xrayfetch.FetchThrough(ctx, &out, controlURL, time.Duration(timeoutMS)*time.Millisecond)
		unprobeable = errors.Is(err, xrayfetch.ErrInstance)
	case "amneziawg":
		if err = awgprobe.ValidateConfig(job.AWGConf); err != nil {
			unprobeable = true

			break
		}
		err = awgprobe.Probe(ctx, job.AWGConf, awgprobe.Options{
			ControlURL: controlURL, Timeout: time.Duration(timeoutMS) * time.Millisecond,
			Resolve: func(ctx context.Context, host string) (netip.Addr, error) {
				if ctx.Err() != nil {
					return netip.Addr{}, ctx.Err()
				}
				s, e := resolve(ctx, network, host)
				if e != nil {
					return netip.Addr{}, e
				}
				return netip.ParseAddr(s)
			},
			Listen: func(kind string, addr *net.UDPAddr) (*net.UDPConn, error) {
				lc := net.ListenConfig{Control: func(_, _ string, raw syscall.RawConn) error { return prepare(raw, network) }}
				c, e := lc.ListenPacket(ctx, kind, addr.String())
				if e != nil {
					return nil, e
				}
				udp, ok := c.(*net.UDPConn)
				if !ok {
					c.Close()
					return nil, errors.New("not UDP")
				}
				return udp, nil
			},
		})
	}
	result := wire.Result{JobID: job.ID, Outcome: "ok", ElapsedMS: time.Since(started).Milliseconds()}
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		result.Outcome, result.Reason = "cancelled", "cancelled"
	case unprobeable:
		result.Outcome, result.Reason = "unprobeable", "configuration_or_network"
	case err != nil:
		result.Outcome, result.Reason = "fail", "request_failed"
		var certificateError *tls.CertificateVerificationError
		if errors.As(err, &certificateError) {
			result.Reason = "tls_verification_failed"
		}
		if errors.Is(err, awgprobe.ErrHandshake) {
			result.Reason = "handshake_timeout"
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			result.Reason = "timeout"
		}
	}
	data, _ := json.Marshal(result)
	return string(data), nil
}

// Resolve only the outer server through the physical network. The control
// destination is resolved by the protocol tunnel, as in the server prober.
func resolveOutbound(out *core.OutboundHandlerConfig, n Network) error {
	return resolveOutboundContext(context.Background(), out, n)
}

func resolve(ctx context.Context, n Network, host string) (string, error) {
	type answer struct {
		ip  string
		err error
	}
	done := make(chan answer, 1)
	go func() { ip, err := n.Resolve(host); done <- answer{ip, err} }()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case a := <-done:
		return a.ip, a.err
	}
}

func resolveOutboundContext(ctx context.Context, out *core.OutboundHandlerConfig, n Network) error {
	if out.ProxySettings == nil {
		return errors.New("missing protocol settings")
	}
	msg, err := out.ProxySettings.GetInstance()
	if err != nil {
		return err
	}
	ep, err := xrayfetch.ServerEndpoint(msg)
	if err != nil {
		return err
	}
	if ep == nil || ep.Address == nil {
		return errors.New("missing server")
	}
	host := ep.Address.AsAddress().String()
	parsed, parseErr := netip.ParseAddr(host)
	if parseErr != nil {
		ip, err := resolve(ctx, n, host)
		if err != nil {
			return err
		}
		parsed, err = netip.ParseAddr(strings.TrimSpace(ip))
		if err != nil {
			return fmt.Errorf("invalid resolved address: %w", err)
		}
	}
	pinned, err := xrayfetch.BindOutbound(out, "", parsed)
	if err != nil {
		return err
	}
	proto.Reset(out)
	proto.Merge(out, pinned)
	return nil
}
