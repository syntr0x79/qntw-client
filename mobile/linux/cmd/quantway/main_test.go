package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/syntr0x79/qntw-client/mobile/linux/account"
)

func TestJSONProtocolSeparatesEngineLogs(t *testing.T) {
	if os.Getenv("QUANTWAY_TEST_STDOUT") == "1" {
		if err := configureJSONOutput(); err != nil {
			os.Exit(2)
		}
		fmt.Fprintln(os.Stdout, "engine diagnostic")
		_ = json.NewEncoder(responseOut).Encode(result{Error: "example"})
		os.Exit(0)
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestJSONProtocolSeparatesEngineLogs")
	cmd.Env = append(os.Environ(), "QUANTWAY_TEST_STDOUT=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var decoded result
	if err = json.Unmarshal(out, &decoded); err != nil || decoded.Error != "example" {
		t.Fatalf("invalid JSON response: %q %v", out, err)
	}
	if !bytes.Contains(stderr.Bytes(), []byte("engine diagnostic")) {
		t.Fatal("diagnostic did not go to stderr")
	}
}

func TestCabinetFailurePreservesPublicAPIErrorMetadata(t *testing.T) {
	out := cabinetFailure(&account.HTTPError{Status: 403, Code: "org_managed", Message: "Этим аккаунтом управляет организация."})
	if out.Status != 403 || out.Code != "org_managed" || out.Error != "Этим аккаунтом управляет организация." {
		t.Fatalf("cabinet failure lost metadata: %+v", out)
	}
}

func TestRestoreFailureKeepsOfflineSeparateFromExpiredSession(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{&account.HTTPError{Status: 401}, 401, "unauthorized"},
		{&account.HTTPError{Status: 403}, 403, "forbidden"},
		{&account.HTTPError{Status: 503}, 503, "service_unavailable"},
		{errors.New("service unavailable"), 0, "request_failed"},
	} {
		out := cabinetFailure(tc.err)
		if out.Status != tc.status || out.Code != tc.code {
			t.Fatalf("restore classification mismatch: %+v", out)
		}
	}
}

func TestRestoreSessionHelperRejectsInvalidCookieWithStructuredPrivateOutput(t *testing.T) {
	if os.Getenv("QUANTWAY_TEST_RESTORE_FAILURE") == "1" {
		os.Args = []string{"quantway-helper", "--json", "--action", "restore-session"}
		flag.CommandLine = flag.NewFlagSet("quantway-helper", flag.ExitOnError)
		main()
		os.Exit(2)
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestRestoreSessionHelperRejectsInvalidCookieWithStructuredPrivateOutput")
	cmd.Env = append(os.Environ(), "QUANTWAY_TEST_RESTORE_FAILURE=1")
	cmd.Stdin = strings.NewReader("{\"cookie\":\"private-invalid-cookie-fixture\"}\n")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("invalid restore must exit with a structured failure: %v", err)
	}
	var out result
	if json.Unmarshal(raw, &out) != nil || out.Status != 401 || out.Code != "invalid_cookie" || out.Error == "" {
		t.Fatalf("restore failure envelope missing: %s", raw)
	}
	if bytes.Contains(raw, []byte("private-invalid-cookie-fixture")) || strings.Contains(stderr.String(), "private-invalid-cookie-fixture") {
		t.Fatal("restore credentials leaked into diagnostic output")
	}
}
