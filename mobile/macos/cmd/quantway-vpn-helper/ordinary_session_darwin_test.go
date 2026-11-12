//go:build darwin && !ios

package main

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/syntr0x79/qntw-client/internal/mobileprobe/wire"
	macnet "github.com/syntr0x79/qntw-client/mobile/macos/network"
)

type lifecycleNetwork struct{}

func (lifecycleNetwork) PrepareSocket(int) error { return nil }
func (lifecycleNetwork) Resolve(string) (string, error) {
	return "192.0.2.4", nil
}

type lifecycleSession struct {
	calls   []string
	fail    string
	cleanup error
	address string
	verify  func()
}

func (s *lifecycleSession) step(name string) error {
	s.calls = append(s.calls, name)
	if s.fail == name {
		return errors.New("test " + name + " failure")
	}
	return nil
}
func (s *lifecycleSession) AllowPhysicalIPs(context.Context, []string) error {
	return s.step("pin endpoint")
}
func (s *lifecycleSession) PinControlPlane(context.Context) error    { return s.step("pin health") }
func (s *lifecycleSession) ApplyDNS(context.Context, []string) error { return s.step("DNS") }
func (s *lifecycleSession) Activate(context.Context) error           { return s.step("activate") }
func (s *lifecycleSession) VerifySystemFrom(ctx context.Context, address string) error {
	s.address = address
	if s.verify != nil {
		s.verify()
	}
	if err := s.step("verify"); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return macnet.ConnectionError{Code: "system_tcp_failed", Message: "cancelled HTTP verification"}
	}
	return nil
}
func (s *lifecycleSession) Close() error { s.calls = append(s.calls, "close"); return s.cleanup }

func lifecycleJob() wire.Job {
	return wire.Job{Engine: "amneziawg", AWGConf: `[Interface]
PrivateKey = AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=
Address = 10.10.0.2/32
DNS = 1.1.1.1
[Peer]
PublicKey = AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=
Endpoint = 192.0.2.4:51820
AllowedIPs = 0.0.0.0/0
`}
}

func TestOrdinaryVPNRequiresVerifiedSystemPathAndAlwaysCleansUp(t *testing.T) {
	for _, fail := range []string{"", "pin endpoint", "pin health", "start", "DNS", "activate", "verify"} {
		t.Run("failure="+fail, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s := &lifecycleSession{fail: fail}
			connected := false
			err := runOrdinaryVPN(ctx, lifecycleJob(), lifecycleNetwork{}, s, "10.10.0.2", "1.1.1.1", func(startup context.Context, _ string) error {
				deadline, ok := startup.Deadline()
				if !ok || time.Until(deadline) > 45*time.Second {
					t.Fatal("unbounded startup")
				}
				return s.step("start")
			}, func() { s.calls = append(s.calls, "stop") }, func() { connected = true; s.calls = append(s.calls, "connected"); cancel() })
			if (err != nil) != (fail != "") || connected != (fail == "") {
				t.Fatalf("err=%v connected=%v calls=%v", err, connected, s.calls)
			}
			if !reflect.DeepEqual(s.calls[len(s.calls)-2:], []string{"stop", "close"}) {
				t.Fatalf("cleanup order: %v", s.calls)
			}
			if fail == "" {
				want := []string{"pin endpoint", "pin health", "start", "DNS", "activate", "verify", "connected", "stop", "close"}
				if !reflect.DeepEqual(s.calls, want) || s.address != "10.10.0.2" {
					t.Fatalf("calls=%v address=%s", s.calls, s.address)
				}
			}
		})
	}
}

func TestOrdinaryVPNCancelledVerificationNeverConnectsAndPreservesCleanupFailure(t *testing.T) {
	for _, cleanup := range []error{nil, macnet.ErrCleanupFailed} {
		ctx, cancel := context.WithCancel(context.Background())
		s := &lifecycleSession{cleanup: cleanup, verify: cancel}
		err := runOrdinaryVPN(ctx, lifecycleJob(), lifecycleNetwork{}, s, "10.10.0.2", "1.1.1.1", func(context.Context, string) error { return nil }, func() {}, func() { t.Error("cancelled verification connected") })
		cancel()
		if cleanup == nil && err != nil {
			t.Fatalf("user cancellation reported failure: %v", err)
		}
		if cleanup != nil && !errors.Is(err, macnet.ErrCleanupFailed) {
			t.Fatalf("cleanup failure hidden: %v", err)
		}
	}
}
