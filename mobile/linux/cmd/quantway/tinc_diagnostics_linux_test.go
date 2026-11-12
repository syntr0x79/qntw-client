//go:build linux

package main

import (
	"errors"
	"github.com/syntr0x79/qntw-client/mobile/linux/tincvpn"
	"strings"
	"testing"
)

func TestPublicTincFailurePreservesKnownCodeWithoutInternalMessage(t *testing.T) {
	for _, code := range []string{"helper_missing", "helper_unsafe", "pkexec_missing", "authorization_cancelled", "authorization_failed", "dns_unavailable", "identity_conflict", "run_as_user"} {
		event := publicTincFailure(tincvpn.Failure{Code: code})
		if event["status"] != "failed" || event["code"] != code || event["error"] == "" {
			t.Fatal("diagnostic collapsed")
		}
	}
	event := publicTincFailure(errors.New("private-token-secret https://qntw.link/sub/secret"))
	if strings.Contains(event["error"], "private-token") || event["code"] != "network_setup_failed" {
		t.Fatal("raw internal error leaked")
	}
}
