package elevation

import (
	"golang.org/x/sys/windows"
	"testing"
)

func TestInstalledACLRejectsWritableOrUnownedHelpers(t *testing.T) {
	cases := []struct {
		name, descriptor string
		private, want    bool
	}{
		{"installed", "O:BAG:BAD:P(A;;FA;;;BA)(A;;FA;;;SY)(A;;FRFX;;;BU)", false, true},
		{"private", "O:BAG:BAD:P(A;;FA;;;BA)(A;;FA;;;SY)", true, true},
		{"private-readable-by-users", "O:BAG:BAD:P(A;;FA;;;BA)(A;;FR;;;BU)", true, false},
		{"users-write", "O:BAG:BAD:P(A;;FA;;;BA)(A;;FW;;;BU)", false, false},
		{"users-change-owner", "O:BAG:BAD:P(A;;FA;;;BA)(A;;WO;;;BU)", false, false},
		{"unsafe-owner", "O:BUG:BUD:P(A;;FA;;;BA)(A;;FRFX;;;BU)", false, false},
		{"null-dacl", "O:BAG:BAD:NO_ACCESS_CONTROL", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sd, e := windows.SecurityDescriptorFromString(c.descriptor)
			if e != nil {
				t.Fatal(e)
			}
			if got := verifyDescriptor(sd, c.private) == nil; got != c.want {
				t.Fatalf("accepted=%v expected=%v", got, c.want)
			}
		})
	}
}

func TestAncestorACLAllowsVolumeCreationButRejectsReplacement(t *testing.T) {
	cases := []struct {
		name, access string
		want         bool
	}{
		{"read-execute", "FRFX", true},
		{"create-subdirectory", "0x00000004", true},
		{"add-file-only", "0x00000002", true},
		{"delete-child", "0x00000040", false},
		{"delete-self", "SD", false},
		{"write-dacl", "WD", false},
		{"write-owner", "WO", false},
		{"generic-write", "GW", false},
		{"full-control", "FA", false},
		{"write-attributes", "0x00000100", false},
		{"write-extended-attributes", "0x00000010", false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			sd, err := windows.SecurityDescriptorFromString("O:BAG:BAD:P(A;;FA;;;BA)(A;;FA;;;SY)(A;;" + test.access + ";;;BU)")
			if err != nil {
				t.Fatal(err)
			}
			if got := verifyAncestorDescriptor(sd) == nil; got != test.want {
				t.Fatalf("accepted=%v expected=%v", got, test.want)
			}
			if test.name == "create-subdirectory" && verifyDescriptor(sd, false) == nil {
				t.Fatal("installed folder must still reject create/write rights")
			}
		})
	}
}
