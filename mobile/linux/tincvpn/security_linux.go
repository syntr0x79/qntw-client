//go:build linux && !android

package tincvpn

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/google/uuid"
	"github.com/syntr0x79/qntw-client/mobile/clienttinc"
)

const (
	HelperPath    = "/usr/libexec/quantway/quantway-tinc-helper"
	StateRoot     = "/var/lib/quantway-tinc/users"
	NativeDaemon  = "/usr/libexec/quantway/tincd"
	NativeControl = "/usr/libexec/quantway/tinc"
	apiOrigin     = "https://qntw.link"
)

// Check every component instead of resolving symlinks into a trusted target.
// An administrator installs these immutable executable paths.
func trustedExecutable(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("invalid helper path")
	}
	for current := path; ; current = filepath.Dir(current) {
		fi, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				return failure("helper_missing")
			}
			return failure("helper_unsafe")
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != 0 || fi.Mode()&os.ModeSymlink != 0 || fi.Mode().Perm()&0022 != 0 {
			return failure("helper_unsafe")
		}
		if current == path && (!fi.Mode().IsRegular() || fi.Mode().Perm()&0111 == 0) {
			return failure("helper_unsafe")
		}
		if current != path && !fi.IsDir() {
			return failure("helper_unsafe")
		}
		if current == "/" {
			return nil
		}
	}
}

func desktopCredentials(origin, token, country string) (clienttinc.Credentials, error) {
	c, err := clienttinc.ValidateCredentials(clienttinc.Credentials{Origin: origin, Token: token})
	if err != nil || c.Origin != apiOrigin || !countryValid(country) {
		return clienttinc.Credentials{}, errors.New("invalid desktop tinc request")
	}
	return c, nil
}

// stateDirectory accepts only an authenticated backend device identity. The
// calling user's device link may rotate without rotating the tinc private key.
func stateDirectory(root string, uid uint32, device uuid.UUID) (string, error) {
	if device == uuid.Nil || !filepath.IsAbs(root) {
		return "", errors.New("invalid device identity")
	}
	path := filepath.Join(root, strconv.FormatUint(uint64(uid), 10), device.String())
	var dirs []string
	for p := path; p != "/"; p = filepath.Dir(p) {
		dirs = append(dirs, p)
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		p := dirs[i]
		if err := os.Mkdir(p, 0700); err != nil && !os.IsExist(err) {
			return "", errors.New("private device state unavailable")
		}
		fi, err := os.Lstat(p)
		if err != nil {
			return "", errors.New("private device state unavailable")
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != 0 || !fi.IsDir() || fi.Mode().Perm()&0022 != 0 {
			return "", errors.New("unsafe device state directory")
		}
		// Existing system ancestors may be 0755; the helper's state is private.
		if (p == root || len(p) > len(root) && p[:len(root)+1] == root+"/") && fi.Mode().Perm()&0077 != 0 {
			return "", errors.New("device state directory must be private")
		}
	}
	return path, nil
}
