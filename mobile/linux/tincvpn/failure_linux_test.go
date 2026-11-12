//go:build linux && !android

package tincvpn

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/syntr0x79/qntw-client/mobile/clienttinc"
)

func TestDiagnosticsAllowOnlyPublicReasons(t *testing.T) {
	for _, item := range []struct {
		err  error
		want string
	}{
		{failure("helper_missing"), "helper_missing"},
		{failure("helper_unsafe"), "helper_unsafe"},
		{clienttinc.APIError{Status: 409}, "identity_conflict"},
		{clienttinc.RuntimeError{Code: "dns_unavailable"}, "dns_unavailable"},
		{clienttinc.RuntimeError{Code: "private_token_secret"}, "network_setup_failed"},
		{errors.New("private_token_secret https://qntw.link/sub/secret"), "network_setup_failed"},
	} {
		code := FailureCode(item.err)
		if code != item.want || !KnownFailure(code) || strings.Contains(FailureMessage(code), "private_token_secret") {
			t.Fatalf("unsafe diagnostic code %q", code)
		}
	}
}

func TestPreAuthorizationFailuresSurvivePrivateChannel(t *testing.T) {
	for _, code := range []string{"helper_missing", "helper_unsafe", "native_missing", "vpn_lock_failed", "device_state_unavailable"} {
		t.Run(code, func(t *testing.T) {
			o := fixtureOptions(t)
			o.verify = func() error { return failure(code) }
			s, err := Start(context.Background(), Config{Origin: apiOrigin, Token: testToken, Country: "PL", launch: launchFixture(t, o)})
			if s != nil || err == nil || FailureCode(err) != code || strings.Contains(err.Error(), testToken) {
				t.Fatalf("lost private startup reason: %v", err)
			}
		})
	}
}

func TestAPIIdentityConflictAndRuntimeDNSReasonSurviveIPC(t *testing.T) {
	for _, stage := range []string{"catalog", "runtime"} {
		t.Run(stage, func(t *testing.T) {
			o := fixtureOptions(t)
			if stage == "catalog" {
				o.device = func(context.Context, clienttinc.Credentials) (uuid.UUID, error) {
					return uuid.Nil, clienttinc.APIError{Status: 409}
				}
				_, err := Start(context.Background(), Config{Origin: apiOrigin, Token: testToken, Country: "PL", launch: launchFixture(t, o)})
				if err == nil || FailureCode(err) != "identity_conflict" {
					t.Fatalf("API reason lost: %v", err)
				}
				return
			}
			o.run = func(context.Context, clienttinc.Options) error {
				return clienttinc.RuntimeError{Code: "dns_unavailable"}
			}
			updates := make(chan Status, 10)
			s, err := Start(context.Background(), Config{Origin: apiOrigin, Token: testToken, Country: "PL", OnStatus: func(s Status) { updates <- s }, launch: launchFixture(t, o)})
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-s.Done():
			case <-time.After(time.Second):
				t.Fatal("runtime failure did not terminate")
			}
			if FailureCode(s.Close()) != "dns_unavailable" {
				t.Fatal("setup failure was misreported as cleanup failure")
			}
		})
	}
}

func TestPkexecExitCodesHaveSafeDistinctReasons(t *testing.T) {
	for _, item := range []struct{ exit, code string }{{"126", "authorization_cancelled"}, {"127", "authorization_failed"}, {"1", "helper_initialization_failed"}} {
		cmd := exec.Command("/bin/sh", "-c", "exit "+item.exit)
		err := cmd.Run()
		done := make(chan error, 1)
		done <- err
		if FailureCode(launchFailure(done)) != item.code {
			t.Fatal("wrong authorization diagnostic")
		}
	}
}

func TestMissingAndUnsafeHelpersRemainRejected(t *testing.T) {
	dir := rootDir(t)
	path := dir + "/helper"
	if FailureCode(trustedExecutable(path)) != "helper_missing" {
		t.Fatal("missing component not diagnosed")
	}
	if err := os.WriteFile(path, []byte("fixture"), 0777); err != nil {
		t.Fatal(err)
	}
	os.Chmod(path, 0777)
	if FailureCode(trustedExecutable(path)) != "helper_unsafe" {
		t.Fatal("unsafe helper accepted or misdiagnosed")
	}
}
