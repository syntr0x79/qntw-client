//go:build (linux && !android) || (darwin && !ios)

package clienttinc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateIdentityDirectoriesRestoreExistingIdentityWithoutFollowingSymlinks(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "native")
	if err := os.MkdirAll(filepath.Join(dir, "hosts"), 0755); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(dir, "ed25519_key.priv")
	if err := os.WriteFile(key, []byte("existing identity"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := privateIdentityDirectories(dir); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{dir, filepath.Join(dir, "hosts")} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0700 {
			t.Fatalf("identity directory privacy not restored: %v", err)
		}
	}
	if b, err := os.ReadFile(key); err != nil || string(b) != "existing identity" {
		t.Fatal("identity was changed while securing its directory")
	}
	if err := os.Remove(filepath.Join(dir, "hosts")); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Chmod(outside, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "hosts")); err != nil {
		t.Fatal(err)
	}
	if err := privateIdentityDirectories(dir); err == nil {
		t.Fatal("symlink identity directory was accepted")
	}
	if info, err := os.Stat(outside); err != nil || info.Mode().Perm() != 0755 {
		t.Fatal("symlink target permissions were changed")
	}
}
