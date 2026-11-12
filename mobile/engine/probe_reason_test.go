package engine

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"testing"

	"github.com/syntr0x79/qntw-client/internal/awgprobe"
	xnet "github.com/xtls/xray-core/common/net"
)

type diagnosticHTTPStatus int

func (s diagnosticHTTPStatus) Error() string       { return "private URL must not be exposed" }
func (s diagnosticHTTPStatus) HTTPStatusCode() int { return int(s) }

func TestProbeErrorReasonsAreBounded(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"http 401", diagnosticHTTPStatus(401), "http_unauthorized"},
		{"http 403", diagnosticHTTPStatus(403), "http_forbidden"},
		{"http 404", diagnosticHTTPStatus(404), "http_not_found"},
		{"http 429", diagnosticHTTPStatus(429), "http_rate_limited"},
		{"http 408", diagnosticHTTPStatus(408), "http_4xx"},
		{"http 503", diagnosticHTTPStatus(503), "http_5xx"},
		{"http 302", diagnosticHTTPStatus(302), "http_status_error"},
		{"refused", &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}, "connection_refused"},
		{"reset", &net.OpError{Op: "read", Err: syscall.ECONNRESET}, "connection_reset"},
		{"broken pipe", syscall.EPIPE, "connection_reset"},
		{"closed pipe", io.ErrClosedPipe, "connection_reset"},
		{"unreachable", syscall.ENETUNREACH, "network_unreachable"},
		{"host unreachable", syscall.EHOSTUNREACH, "network_unreachable"},
		{"network down", syscall.ENETDOWN, "network_unreachable"},
		{"permission", syscall.EPERM, "socket_permission"},
		{"access", syscall.EACCES, "socket_permission"},
		{"eof", io.EOF, "unexpected_eof"},
		{"truncated", io.ErrUnexpectedEOF, "unexpected_eof"},
		{"dns", &net.DNSError{Err: "private hostname", Name: "secret.invalid"}, "dns_failed"},
		{"certificate", &tls.CertificateVerificationError{Err: errors.New("private certificate")}, "tls_verification_failed"},
		{"handshake", awgprobe.ErrHandshake, "handshake_timeout"},
		{"deadline", context.DeadlineExceeded, "timeout"},
		{"cancelled", context.Canceled, "cancelled"},
		{"unknown", errors.New("https://secret.invalid/sub/device-token"), "request_failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := probeErrorReason(fmt.Errorf("request to private endpoint: %w", tt.err)); got != tt.want {
				t.Fatalf("reason=%q want %q", got, tt.want)
			}
		})
	}
}

func TestProbePhysicalCauseSurvivesDispatcherEOF(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	network := &testNetwork{err: syscall.EPERM}
	owner := newSocketOwner(context.Background(), network)
	defer owner.close()
	ctx := context.WithValue(context.Background(), socketOwnerKey{}, owner)
	target := xnet.TCPDestination(xnet.LocalHostIP, xnet.Port(listener.Addr().(*net.TCPAddr).Port))
	conn, err := (&mobileSystemDialer{}).Dial(ctx, nil, target, nil)
	if conn != nil || !errors.Is(err, syscall.EPERM) {
		t.Fatalf("dial behavior changed: conn=%v err=%v", conn, err)
	}
	if got := probeRequestReason(ctx, io.EOF, owner); got != "socket_permission" {
		t.Fatalf("dispatcher EOF hid physical failure: %q", got)
	}
	if got := probeRequestReason(ctx, io.ErrClosedPipe, owner); got != "socket_permission" {
		t.Fatalf("dispatcher closed pipe hid physical failure: %q", got)
	}
	// An actual control HTTP response remains more precise than an earlier
	// failed socket attempt. Probe ownership must not overwrite that evidence.
	if got := probeRequestReason(ctx, diagnosticHTTPStatus(429), owner); got != "http_rate_limited" {
		t.Fatalf("HTTP status overwritten by socket diagnostic: %q", got)
	}
	expired, cancel := context.WithCancel(context.Background())
	cancel()
	if got := probeErrorReason(expired.Err()); got != "cancelled" {
		t.Fatalf("cancellation changed: %q", got)
	}
}

type failingProbeResolver struct{ testNetwork }

func (*failingProbeResolver) Resolve(string) (string, error) {
	return "", errors.New("DNS failure for private endpoint")
}

func TestProbeDNSFallbackDoesNotExposeEndpoint(t *testing.T) {
	owner := newSocketOwner(context.Background(), &failingProbeResolver{})
	defer owner.close()
	ctx := context.WithValue(context.Background(), socketOwnerKey{}, owner)
	target := xnet.TCPDestination(xnet.DomainAddress("secret.invalid"), 443)
	conn, err := (&mobileSystemDialer{}).Dial(ctx, nil, target, nil)
	if conn != nil || err == nil {
		t.Fatalf("unexpected dial result: conn=%v err=%v", conn, err)
	}
	if got := probeRequestReason(ctx, io.EOF, owner); got != "dns_failed" {
		t.Fatalf("unsafe or missing DNS diagnostic: %q", got)
	}
}
