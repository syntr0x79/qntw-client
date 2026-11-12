//go:build linux

package vpn

import (
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestResolvedConfiguresOnlyTunnelLink(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resolv.conf")
	if err := os.WriteFile(path, []byte("nameserver 127.0.0.53\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	r := Resolved{resolvConf: path, run: func(_ context.Context, args ...string) error {
		calls = append(calls, append([]string(nil), args...))
		return nil
	}}
	if err := r.Apply(context.Background(), "qw0", netip.MustParseAddr("1.1.1.1")); err != nil {
		t.Fatal(err)
	}
	if err := r.Revert(context.Background(), "qw0"); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"dns", "qw0", "1.1.1.1"}, {"domain", "qw0", "~."}, {"default-route", "qw0", "yes"}, {"revert", "qw0"}}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("unexpected DNS commands: %v", calls)
	}
}
func TestResolvedRejectsUnsupportedDNS(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resolv.conf")
	if err := os.WriteFile(path, []byte("nameserver 127.0.0.53\nnameserver 9.9.9.9\n"), 0600); err != nil {
		t.Fatal(err)
	}
	called := false
	r := Resolved{resolvConf: path, run: func(context.Context, ...string) error { called = true; return nil }}
	if err := r.Apply(context.Background(), "qw0", netip.MustParseAddr("1.1.1.1")); err == nil || called {
		t.Fatal("unsupported DNS accepted")
	}
}
