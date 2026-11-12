package clienttinc

import (
	"reflect"
	"testing"
)

func TestNativeEnvironmentContainsOnlyTrustedDirectoryAndOwnedRoutingValues(t *testing.T) {
	want := []string{`SystemRoot=C:\Windows`, `WINDIR=C:\Windows`, `PATH=C:\Windows\System32`, "QUANTWAY_PHYSICAL_IFINDEX=17"}
	if got := nativeEnvironment(`C:\Windows`, `C:\Windows\System32`, 17); !reflect.DeepEqual(got, want) {
		t.Fatalf("native environment differs from the strict allowlist: %q", got)
	}
	if got := nativeEnvironment(`C:\Windows`, `C:\Windows\System32`, 0); !reflect.DeepEqual(got, want[:3]) {
		t.Fatalf("bootstrap inherited route hint: %q", got)
	}
}
