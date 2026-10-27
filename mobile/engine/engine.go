// Package engine exposes the existing Xray and AmneziaWG probers to gomobile.
// Only one probe runs at once because Xray's socket controllers are process-wide.
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
	active        bool
	cancel        context.CancelFunc
	network       Network
	tunnelNetwork Network
}

func init() {
	if err := internet.RegisterDialerController(func(_, _ string, raw syscall.RawConn) error {
		state.Lock()
		n := state.network
		if n == nil {
			n = state.tunnelNetwork
		}
		state.Unlock()
		if n == nil {
			return errors.New("mobile: physical network unavailable")
		}
		return prepare(raw, n)
	}); err != nil {
		panic(err)
	}
}

func prepare(raw syscall.RawConn, n Network) error {
	var callErr error
	if err := raw.Control(func(fd uintptr) { callErr = n.PrepareSocket(int(fd)) }); err != nil {
		return err
	}
	return callErr
}

// Version identifies both the API and the pinned protocol engines.
func Version() string { return "mobile/2 xray/" + core.Version() + " awg/3.1.20260828" }
func Capabilities() string {
	return `{"version":1,"engines":["xray","amneziawg"],"parallelism":1,"tun":true}`
}

// Cancel interrupts the current probe. A cancellation is never a failed route.
func Cancel() {
	state.Lock()
	defer state.Unlock()
	if state.cancel != nil {
		state.cancel()
	}
}

// Probe executes one job and returns a wire.Result. Configuration errors are
// returned as Go errors; network outcomes contain only bounded reason codes.
func Probe(jobJSON string, controlURL string, timeoutMS int, network Network) (string, error) {
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
	state.Lock()
	if state.active {
		state.Unlock()
		return "", errors.New("probe already running")
	}
	state.active, state.network, state.cancel = true, network, cancel
	state.Unlock()
	defer func() { state.Lock(); state.active, state.network, state.cancel = false, nil, nil; state.Unlock() }()
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
