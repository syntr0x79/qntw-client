package engine

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"syscall"

	"github.com/syntr0x79/qntw-client/internal/awgprobe"
)

// probeErrorReason returns only a bounded public code. Error strings can contain
// device credentials or endpoint URLs and must never become probe diagnostics.
func probeErrorReason(err error) string {
	if err == nil {
		return ""
	}
	var status interface{ HTTPStatusCode() int }
	if errors.As(err, &status) {
		switch code := status.HTTPStatusCode(); {
		case code == 401:
			return "http_unauthorized"
		case code == 403:
			return "http_forbidden"
		case code == 404:
			return "http_not_found"
		case code == 429:
			return "http_rate_limited"
		case code >= 400 && code < 500:
			return "http_4xx"
		case code >= 500 && code < 600:
			return "http_5xx"
		default:
			return "http_status_error"
		}
	}
	var certificateError *tls.CertificateVerificationError
	if errors.As(err, &certificateError) {
		return "tls_verification_failed"
	}
	if errors.Is(err, awgprobe.ErrHandshake) {
		return "handshake_timeout"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	var dnsError *net.DNSError
	if errors.As(err, &dnsError) {
		return "dns_failed"
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return "timeout"
	}
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection_refused"
	case errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.EPIPE), errors.Is(err, io.ErrClosedPipe):
		return "connection_reset"
	case errors.Is(err, syscall.ENETUNREACH), errors.Is(err, syscall.EHOSTUNREACH), errors.Is(err, syscall.ENETDOWN):
		return "network_unreachable"
	case errors.Is(err, syscall.EPERM), errors.Is(err, syscall.EACCES):
		return "socket_permission"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "unexpected_eof"
	default:
		return "request_failed"
	}
}

func probeRequestReason(ctx context.Context, err error, owner *socketOwner) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "timeout"
	}
	reason := probeErrorReason(err)
	// Xray's dispatcher can replace the physical dial error with EOF on its
	// pipe. Retain the first bounded socket diagnostic for this instance only;
	// a more precise HTTP, TLS or timeout error always takes precedence.
	if reason == "request_failed" || reason == "unexpected_eof" || errors.Is(err, io.ErrClosedPipe) {
		if physical := owner.physicalFailure(); physical != "" {
			return physical
		}
	}
	return reason
}
