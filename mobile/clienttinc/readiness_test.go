package clienttinc

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	wire "github.com/syntr0x79/qntw-client/internal/clienttincwire"
)

func TestPathReadinessRequiresIngressAndAuthorizedExit(t *testing.T) {
	for _, tc := range []struct {
		name           string
		ingresses      []string
		requireHealth  bool
		healthError    error
		wantHealthCall bool
		wantReady      bool
		wantMessage    string
	}{
		{name: "no ingress never checks exit", requireHealth: true, wantMessage: "Waiting for an ingress"},
		{name: "authenticated ingress is not exit proof", ingresses: []string{"in1"}, requireHealth: true, healthError: errors.New("exit not accepting grant"), wantHealthCall: true, wantMessage: "Waiting for the authorized exit"},
		{name: "healthy authorized exit", ingresses: []string{"in1", "in2"}, requireHealth: true, wantHealthCall: true, wantReady: true},
		{name: "optional health preserves ingress-only contract", ingresses: []string{"in1"}, wantReady: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expiresAt := time.Now().Add(time.Minute)
			healthCalled := false
			var health func(context.Context) error
			if tc.requireHealth {
				health = func(ctx context.Context) error {
					healthCalled = true
					if deadline, ok := ctx.Deadline(); !ok || !deadline.Equal(expiresAt) {
						t.Fatalf("health lost authorized deadline: %v %v", deadline, ok)
					}
					return tc.healthError
				}
			}
			got, valid := probePathReadiness(context.Background(), expiresAt, func(context.Context) []string { return tc.ingresses }, health)
			if !valid || got.ready != tc.wantReady || got.message != tc.wantMessage || healthCalled != tc.wantHealthCall || !reflect.DeepEqual(got.ingresses, tc.ingresses) {
				t.Fatalf("unexpected readiness: observation=%+v valid=%v healthCalled=%v", got, valid, healthCalled)
			}
		})
	}
}

func TestPathReadinessCanAdvanceWithoutRenewingAuthorization(t *testing.T) {
	expiresAt := time.Now().Add(time.Minute)
	ingressCalls, healthCalls := 0, 0
	ingress := func(context.Context) []string {
		ingressCalls++
		if ingressCalls == 1 {
			return nil
		}
		return []string{"in1"}
	}
	health := func(context.Context) error { healthCalls++; return nil }
	first, valid := probePathReadiness(context.Background(), expiresAt, ingress, health)
	if !valid || first.ready || healthCalls != 0 {
		t.Fatal("initial absent ingress was treated as a working authorized path")
	}
	second, valid := probePathReadiness(context.Background(), expiresAt, ingress, health)
	if !valid || !second.ready || healthCalls != 1 || ingressCalls != 2 {
		t.Fatal("local handshake completion did not advance readiness on the existing grant")
	}
}

func TestPathReadinessRejectsExpiredAndCanceledBeforeAnyIO(t *testing.T) {
	for _, tc := range []struct {
		name    string
		expires time.Time
		cancel  bool
	}{
		{name: "expired", expires: time.Now().Add(-time.Second)},
		{name: "stopped", expires: time.Now().Add(time.Minute), cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancel {
				cancel()
			}
			called := false
			observation, valid := probePathReadiness(ctx, tc.expires, func(context.Context) []string {
				called = true
				return []string{"in1"}
			}, func(context.Context) error { called = true; return nil })
			if valid || observation.ready || called {
				t.Fatal("expired or canceled permission initiated readiness I/O")
			}
		})
	}
}

func TestPathReadinessExpiryDuringIngressSkipsHealth(t *testing.T) {
	healthCalled := false
	observation, valid := probePathReadiness(context.Background(), time.Now().Add(50*time.Millisecond), func(ctx context.Context) []string {
		<-ctx.Done()
		return []string{"in1"}
	}, func(context.Context) error { healthCalled = true; return nil })
	if valid || observation.ready || healthCalled {
		t.Fatal("ingress observation completing after expiry reached the exit check")
	}
}

func TestPathReadinessRejectsHealthSuccessAfterExpiry(t *testing.T) {
	healthCalled := false
	_, valid := probePathReadiness(context.Background(), time.Now().Add(50*time.Millisecond), func(context.Context) []string { return []string{"in1"} }, func(ctx context.Context) error {
		healthCalled = true
		<-ctx.Done()
		// A request may have received a successful response concurrently with
		// expiry. Its success must still not become a publishable observation.
		return nil
	})
	if valid || !healthCalled {
		t.Fatal("health success after the grant deadline remained publishable")
	}
}

func TestPathReadinessStopCancelsBlockedHealth(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	result := make(chan bool, 1)
	go func() {
		_, valid := probePathReadiness(ctx, time.Now().Add(time.Minute), func(context.Context) []string { return []string{"in1"} }, func(ctx context.Context) error {
			close(entered)
			<-ctx.Done()
			// Even a callback that returns nil after cancellation is stale.
			return nil
		})
		result <- valid
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("health check did not begin")
	}
	cancel()
	select {
	case valid := <-result:
		if valid {
			t.Fatal("stopped health check remained publishable")
		}
	case <-time.After(time.Second):
		t.Fatal("Stop did not interrupt blocked health")
	}
}

func TestPathReadinessAllowsHealthyExitSlowerThanTwoSeconds(t *testing.T) {
	// This is intentionally longer than two seconds: imposing the native
	// telemetry-query budget on real HTTPS would reject otherwise healthy VPNs.
	const responseDelay = 2050 * time.Millisecond
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	observation, valid := probePathReadiness(ctx, time.Now().Add(time.Minute), func(context.Context) []string { return []string{"in1"} }, func(ctx context.Context) error {
		timer := time.NewTimer(responseDelay)
		defer timer.Stop()
		select {
		case <-timer.C:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	if !valid || !observation.ready || time.Since(started) < responseDelay {
		t.Fatalf("healthy slow exit was rejected: observation=%+v valid=%v elapsed=%v", observation, valid, time.Since(started))
	}
}

func readinessConfig() wire.ClientConfig {
	expiresAt := time.Now().Add(time.Minute)
	return wire.ClientConfig{ExpiresAt: expiresAt, Assignment: wire.Assignment{Country: "PL", EgressID: uuid.New(), Version: 3, ExpiresAt: expiresAt}}
}

func TestReadinessStatusKeepsCapacityReasonAndCurrentGrant(t *testing.T) {
	config := readinessConfig()
	previous := Status{State: "connecting", Country: "PL", DesiredCountry: "SE", EgressID: config.Assignment.EgressID.String(), ExpiresAt: config.ExpiresAt, Code: "capacity_exhausted", RetryAfter: 7, Message: "All connections are occupied"}
	refused := APIError{Status: 503, Code: "capacity_exhausted", RetryAfter: 7}
	got := readinessStatus(previous, config, "SE", readinessObservation{ready: true, ingresses: []string{"in1"}}, refused)
	if got.State != "connected" || got.Code != previous.Code || got.RetryAfter != previous.RetryAfter || got.Message != previous.Message {
		t.Fatalf("local health hid server refusal: %+v", got)
	}
	if got.Country != "PL" || got.DesiredCountry != "SE" || got.EgressID != previous.EgressID || !got.ExpiresAt.Equal(config.ExpiresAt) {
		t.Fatalf("local health changed the authorized grant to requested country: %+v", got)
	}
	if previous.State != "connecting" || previous.DesiredCountry != "SE" || !previous.ExpiresAt.Equal(config.ExpiresAt) {
		t.Fatal("readiness modified its input status")
	}
}

func TestReadinessStatusPositiveConfigClearsRefusalWithoutReadinessFlicker(t *testing.T) {
	config := readinessConfig()
	previous := Status{State: "connected", Country: "PL", Code: "capacity_exhausted", RetryAfter: 7, Message: "All connections are occupied"}
	got := readinessStatus(previous, config, "PL", readinessObservation{ready: true, ingresses: []string{"in1"}}, nil)
	if got.State != "connected" || got.Code != "" || got.RetryAfter != 0 || got.Message != "" || !got.ExpiresAt.Equal(config.ExpiresAt) {
		t.Fatalf("positive renewed configuration retained stale refusal or flickered: %+v", got)
	}
	got = readinessStatus(got, config, "PL", readinessObservation{ingresses: []string{"in1"}, message: "Waiting for the authorized exit"}, nil)
	if got.State != "connecting" || got.Message != "Waiting for the authorized exit" || !got.ExpiresAt.Equal(config.ExpiresAt) {
		t.Fatalf("failed exit health was presented as fully connected: %+v", got)
	}
}

func TestReadinessStatusOwnsIngressSlice(t *testing.T) {
	config := readinessConfig()
	previous := Status{Ingresses: []string{"old"}}
	observation := readinessObservation{ready: true, ingresses: []string{"in1", "in2"}}
	got := readinessStatus(previous, config, "PL", observation, nil)
	observation.ingresses[0] = "probe-mutated"
	if got.Ingresses[0] != "in1" {
		t.Fatal("probe retained mutable ownership of published ingress names")
	}
	got.Ingresses[1] = "status-mutated"
	if observation.ingresses[1] != "in2" || previous.Ingresses[0] != "old" {
		t.Fatal("readiness status aliases observation or previous status")
	}
}
