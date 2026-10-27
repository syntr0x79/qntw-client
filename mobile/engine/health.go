package engine

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/core"
)

// CheckVPN validates the VPN outbound even when the control host matches a
// user bypass rule. Direct AWG TUN has no bypass rules, so OS routing suffices.
func CheckVPN(controlURL string) (bool, error) {
	u, e := url.Parse(controlURL)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return false, errors.New("HTTPS control URL required")
	}
	vpn.Lock()
	t, _ := vpn.closer.(*xrayTunnel)
	running := vpn.closer != nil
	vpn.Unlock()
	if !running {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	transport := &http.Transport{DisableKeepAlives: true}
	if t != nil {
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, e := net.SplitHostPort(address)
			if e != nil {
				return nil, e
			}
			p, e := strconv.Atoi(port)
			if e != nil {
				return nil, e
			}
			ctx = session.SetForcedOutboundTagToContext(ctx, "vpn")
			return core.Dial(ctx, t.core, xnet.TCPDestination(xnet.ParseAddress(host), xnet.Port(p)))
		}
	}
	defer transport.CloseIdleConnections()
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, controlURL, nil)
	if e != nil {
		return false, e
	}
	response, e := (&http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if e != nil {
		return false, nil
	}
	defer response.Body.Close()
	io.Copy(io.Discard, io.LimitReader(response.Body, 16<<10))
	return response.StatusCode >= 200 && response.StatusCode < 300, nil
}
