package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/syntr0x79/qntw-client/mobile/clienttinc"
)

func TestTincAcceptsOnlyDeviceLinksAtTheQuantwayOrigin(t *testing.T) {
	const token = "private-device-token-fixture"
	valid := "https://qntw.link/sub/" + token
	for _, link := range []string{valid, valid + "/", valid + "?client=linux"} {
		credentials, err := tincCredentials(link)
		if err != nil || credentials.Origin != "https://qntw.link" || credentials.Token != token || credentials.AllowLoopbackHTTP {
			t.Fatal("valid device link was not mapped to the fixed origin")
		}
	}
	for _, link := range []string{
		"http://qntw.link/sub/" + token, "https://example.invalid/sub/" + token,
		"https://qntw.link.evil.invalid/sub/" + token, "https://qntw.link:444/sub/" + token,
		"https://user@qntw.link/sub/" + token, "https://qntw.link/sub/short",
		"https://qntw.link/sub/" + token + "#fragment", "https://qntw.link/sub/%41" + token,
		"https://qntw.link/api/web/v1/account/me", "file:///sub/" + token,
	} {
		_, err := tincCredentials(link)
		if err == nil || strings.Contains(err.Error(), token) || strings.Contains(err.Error(), link) {
			t.Fatal("unsafe link accepted or credential leaked")
		}
	}
}

func TestInitialRequestPreservesPipelinedConnectionCommands(t *testing.T) {
	for _, body := range []string{`{"country":"PL"}`, `{"link":"` + strings.Repeat("x", 9000) + `"}`} {
		reader := bufio.NewReaderSize(strings.NewReader(body+"\nstop\n"), 32)
		line, err := readRequestLine(reader, 16384)
		if err != nil || line != body+"\n" {
			t.Fatal("request could not be read")
		}
		stop, err := reader.ReadString('\n')
		if err != nil || stop != "stop\n" {
			t.Fatal("connection stop command was lost")
		}
	}
	if _, err := readRequestLine(bufio.NewReader(strings.NewReader(strings.Repeat("x", 65))), 64); err == nil {
		t.Fatal("oversized initial request accepted")
	}
}

func TestTincErrorsPreservePublicStatusWithoutLeakingSecrets(t *testing.T) {
	for _, code := range []int{401, 402, 403, 503} {
		out := tincFailure(clienttinc.APIError{Status: code})
		if out.Status != code || out.Code == "" || out.Error == "" {
			t.Fatal("missing public error information")
		}
	}
	denied := tincFailure(clienttinc.APIError{Status: 403})
	if denied.Code != "access_denied" || strings.Contains(denied.Error, "пока") {
		t.Fatal("ordinary access refusal is still described as a pilot")
	}
	out := tincFailure(errors.New("private-token-fixture with unknown internal request"))
	encoded, _ := json.Marshal(out)
	if strings.Contains(string(encoded), "private-token-fixture") || out.Error == "" {
		t.Fatal("internal error leaked or public error missing")
	}
}

func TestTincRuntimeStatesFollowTheDesktopStreamProtocol(t *testing.T) {
	for _, item := range []struct{ native, public string }{
		{"preparing", "preparing"}, {"connecting", "waiting"}, {"waiting", "waiting"},
		{"connected", "connected"}, {"reconnecting", "reconnecting"},
		{"expired", "expired"}, {"revoked", "expired"}, {"error", "failed"}, {"failed", "failed"},
	} {
		state, include := publicTincState(item.native)
		if !include || state != item.public {
			t.Fatalf("runtime state %s did not match the desktop contract", item.native)
		}
	}
	if _, include := publicTincState("stopped"); include {
		t.Fatal("cleanup status must be conveyed by process exit after cleanup")
	}
}
