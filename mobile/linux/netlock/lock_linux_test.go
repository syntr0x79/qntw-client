//go:build linux

package netlock

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGlobalRootLockIsExclusiveReusableAndRejectsForeignFiles(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("run in isolated root test container")
	}
	dir, err := os.MkdirTemp("/run", "quantway-netlock-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "lock")
	a, err := acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := acquire(path); err == nil {
		second.Close()
		t.Fatal("second helper acquired the same network lock")
	}
	a.Close()
	b, err := acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	b.Close()
	os.Remove(path)
	os.Symlink("/etc/passwd", path)
	if f, err := acquire(path); err == nil {
		f.Close()
		t.Fatal("symlink lock accepted")
	}
	os.Remove(path)
	os.WriteFile(path, []byte(""), 0666)
	os.Chmod(path, 0666)
	if f, err := acquire(path); err == nil {
		f.Close()
		t.Fatal("writable lock accepted")
	}
	os.Chmod(path, 0600)
	os.Chown(path, 1000, 1000)
	if f, err := acquire(path); err == nil {
		f.Close()
		t.Fatal("user-owned lock accepted")
	}
	os.Chown(path, 0, 0)
	os.Remove(path)
	os.Chmod(dir, 0777)
	if f, err := acquire(path); err == nil {
		f.Close()
		t.Fatal("writable lock parent accepted")
	}
}
