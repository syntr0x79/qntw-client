package clienttinc

import (
	"reflect"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsNativeChildEnvironmentIgnoresCallerExecutionAndRoutingSettings(t *testing.T) {
	for _, key := range []string{"SystemRoot", "windir", "PATH", "COMSPEC", "PATHEXT", "NETNAME", "LISTEN_FDS", "LISTEN_PID", "TINC_UMBILICAL", "EDITOR", "VISUAL", "__COMPAT_LAYER", "OPENSSL_CONF", "OPENSSL_MODULES", "quantway_physical_ifindex"} {
		t.Setenv(key, `C:\untrusted-caller\must-not-run`)
	}
	root, err := windows.GetSystemWindowsDirectory()
	if err != nil {
		t.Fatal(err)
	}
	system, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	got, err := trustedNativeEnvironment(17)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"SystemRoot=" + root, "WINDIR=" + root, "PATH=" + system, "QUANTWAY_PHYSICAL_IFINDEX=17"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("untrusted child environment survived: got=%q want=%q", got, want)
	}
}
