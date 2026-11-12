//go:build darwin && !ios

package network

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func fakeSession(t *testing.T, failRoute string) (*Session, *[]string) {
	t.Helper()
	var calls []string
	s := &Session{Physical: "en0", System: &Device{Name: "utun99"}}
	s.run = func(_ context.Context, input, name string, args ...string) ([]byte, error) {
		line := name + " " + strings.Join(args, " ")
		calls = append(calls, line)
		switch {
		case strings.HasSuffix(line, "-sr"):
			return []byte(`anchor "com.apple/*" all`), nil
		case strings.HasSuffix(line, "-E"):
			return []byte("Token : 42"), nil
		case failRoute != "" && strings.Contains(line, "add -net "+failRoute):
			return nil, errors.New("route conflict")
		default:
			return nil, nil
		}
	}
	return s, &calls
}
func TestPendingTunnelDoesNotChangeInternetOrDNS(t *testing.T) {
	s, calls := fakeSession(t, "")
	ctx := context.Background()
	if e := s.ApplyDNS(ctx, []string{"1.1.1.1"}); e != nil {
		t.Fatal(e)
	}
	if len(*calls) != 0 || s.active || len(s.routes) != 0 || s.dnsKey != "" {
		t.Fatal("preparing a tunnel changed system networking before forwarding was verified")
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	if len(*calls) != 0 {
		t.Fatal("cancelled preparation changed global networking")
	}
}

func TestActiveSystemPathFailureIsAnErrorRatherThanIndefiniteReadinessWaiting(t *testing.T) {
	s, calls := fakeSession(t, "")
	if checked, err := s.VerifyIfActive(context.Background()); checked || err != nil || len(*calls) != 0 {
		t.Fatal("pending session started a system check")
	}
	s.active = true
	checked, err := s.VerifyIfActive(context.Background())
	var connectionError ConnectionError
	if !checked || !errors.As(err, &connectionError) || connectionError.Code != "system_route_failed" {
		t.Fatalf("active tunnel failure was hidden: checked=%v err=%v", checked, err)
	}
}
func TestActivationAppliesGuardAndRoutesBeforeDNSAndOwnsCleanup(t *testing.T) {
	s, calls := fakeSession(t, "")
	ctx := context.Background()
	s.ApplyDNS(ctx, []string{"1.1.1.1"})
	if e := s.Activate(ctx); e != nil {
		t.Fatal(e)
	}
	if !s.active || len(s.routes) != 4 || s.dnsKey == "" {
		t.Fatal("activation incomplete")
	}
	all := strings.Join(*calls, "\n")
	if strings.Index(all, "-a com.apple/quantway -f -") > strings.Index(all, "add -net 0.0.0.0/1") {
		t.Fatal("unguarded routing window")
	}
	if !strings.HasSuffix(all, "/usr/sbin/scutil ") {
		t.Fatal("DNS was switched before routes")
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	all = strings.Join(*calls, "\n")
	if !strings.Contains(all, "delete -net 0.0.0.0/1") || !strings.Contains(all, "-a com.apple/quantway -F rules") || !strings.Contains(all, "-X 42") {
		t.Fatal("owned state was not restored")
	}
}
func TestActivationFailureRollsBackOnlyRoutesThisSessionInstalled(t *testing.T) {
	s, calls := fakeSession(t, "128.0.0.0/1")
	if e := s.Activate(context.Background()); e == nil {
		t.Fatal("route conflict was hidden")
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	all := strings.Join(*calls, "\n")
	if !strings.Contains(all, "delete -net 0.0.0.0/1") || strings.Contains(all, "delete -net 128.0.0.0/1") || strings.Contains(all, "/usr/sbin/scutil") {
		t.Fatal("cleanup touched unowned state")
	}
}

// Tinc's physical TCP and assigned TLS flows already exist when PF activates.
// Implicit PF flags S/SA match only SYN and strand both established flows.
func TestFirewallAcceptsEstablishedTunnelPacketsAtActivation(t *testing.T) {
	s, _ := fakeSession(t, "")
	var rules string
	original := s.run
	s.run = func(ctx context.Context, input, name string, args ...string) ([]byte, error) {
		if name == "/sbin/pfctl" && input != "" {
			rules = input
		}
		return original(ctx, input, name, args...)
	}
	if e := s.Activate(context.Background()); e != nil {
		t.Fatal(e)
	}
	for _, line := range strings.Split(rules, "\n") {
		if strings.HasPrefix(line, "pass ") && !strings.Contains(line, "proto udp") && !strings.Contains(line, "flags any") {
			t.Fatalf("established TCP packets would be dropped by %s", line)
		}
		if strings.HasPrefix(line, "pass ") && !strings.Contains(line, "no state") {
			t.Fatalf("midstream state can lose TCP window scaling or bypass an interface rule: %s", line)
		}
	}
	if !strings.Contains(rules, "block drop out quick all") {
		t.Fatal("the guard was removed instead of fixing established transport")
	}
}

func TestPhysicalTransportRouteIsScopedAndOwnedOnce(t *testing.T) {
	s, calls := fakeSession(t, "")
	s.Gateway = "192.168.1.1"
	if e := s.AllowPhysicalIPs(context.Background(), []string{"192.0.2.10", "192.0.2.10"}); e != nil {
		t.Fatal(e)
	}
	all := strings.Join(*calls, "\n")
	if strings.Count(all, "add -host 192.0.2.10") != 1 || !strings.Contains(all, "192.168.1.1 -ifscope en0") {
		t.Fatal("transport bypass must be scoped to the physical interface")
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	all = strings.Join(*calls, "\n")
	if !strings.Contains(all, "delete -host 192.0.2.10 -ifscope en0") {
		t.Fatal("owned transport exception was not removed")
	}
}
func TestExistingStaticPhysicalRouteIsNeverRemoved(t *testing.T) {
	s, calls := fakeSession(t, "")
	s.Gateway = "192.168.1.1"
	original := s.run
	s.run = func(ctx context.Context, input, name string, args ...string) ([]byte, error) {
		original(ctx, input, name, args...)
		line := strings.Join(args, " ")
		if strings.Contains(line, "add -host") {
			return nil, errors.New("File exists")
		}
		if strings.Contains(line, "get 192.0.2.10") {
			return []byte("destination: 192.0.2.10\ngateway: 192.168.1.1\ninterface: en0\nflags: <UP,HOST,STATIC,IFSCOPE>\n"), nil
		}
		return nil, nil
	}
	if e := s.AllowPhysicalIPs(context.Background(), []string{"192.0.2.10"}); e != nil {
		t.Fatal(e)
	}
	s.Close()
	if strings.Contains(strings.Join(*calls, "\n"), "delete -host") {
		t.Fatal("another owner's static route was removed")
	}
}

func TestGlobalDefaultDNSDoesNotRequireInterfaceAnnotation(t *testing.T) {
	block := "DNS configuration\nresolver #1\n nameserver[0] : 1.1.1.1\n flags : Supplemental, Request A records\n order : 1\n"
	if !defaultResolverUses(block, "1.1.1.1") {
		t.Fatal("valid unscoped default DNS was rejected")
	}
	if defaultResolverUses(strings.ReplaceAll(block, "1.1.1.1", "1.1.1.10"), "1.1.1.1") {
		t.Fatal("prefix match accepted a different resolver")
	}
}

func TestCleanupFailureRemainsDistinctAndKeepsGuard(t *testing.T) {
	s, calls := fakeSession(t, "")
	if err := s.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	original := s.run
	s.run = func(ctx context.Context, input, name string, args ...string) ([]byte, error) {
		if name == "/sbin/route" && len(args) > 1 && args[1] == "delete" {
			return nil, errors.New("test cleanup failure")
		}
		return original(ctx, input, name, args...)
	}
	for i := 0; i < 2; i++ {
		if !errors.Is(s.Close(), ErrCleanupFailed) {
			t.Fatal("cleanup failure lost its public classification")
		}
	}
	if strings.Contains(strings.Join(*calls, "\n"), "-F rules") {
		t.Fatal("failed cleanup cleared the network guard")
	}
}
