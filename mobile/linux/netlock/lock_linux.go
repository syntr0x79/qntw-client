//go:build linux

// Package netlock serializes the two privileged desktop VPN helpers. The lock
// belongs to root, spans users and modes, and lives until network cleanup ends.
package netlock

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

const Path = "/run/quantway-desktop-vpn.lock"

func Acquire() (*os.File, error) { return acquire(Path) }

func acquire(path string) (*os.File, error) {
	if os.Geteuid() != 0 || !filepath.IsAbs(path) {
		return nil, errors.New("administrator privileges are required")
	}
	for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
		fi, err := os.Lstat(dir)
		if err != nil {
			return nil, errors.New("unsafe VPN lock directory")
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != 0 || !fi.IsDir() || fi.Mode().Perm()&0022 != 0 {
			return nil, errors.New("unsafe VPN lock directory")
		}
		if dir == "/" {
			break
		}
	}
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
	if err != nil {
		return nil, errors.New("VPN lock unavailable")
	}
	f := os.NewFile(uintptr(fd), path)
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Uid != 0 || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&0077 != 0 || st.Nlink != 1 {
		f.Close()
		return nil, errors.New("unsafe VPN lock")
	}
	if unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB) != nil {
		f.Close()
		return nil, errors.New("another desktop VPN is active")
	}
	return f, nil
}
