//go:build (linux && !android) || (darwin && !ios)

package clienttinc

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Newer tinc init sets existing configuration directories to 0755. Restore
// application ownership and privacy before publishing the identity or starting
// its socket guard, including when native initialization failed partway.
func privateIdentityDirectories(dir string) error {
	for _, path := range []string{dir, filepath.Join(dir, "hosts")} {
		fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return err
		}
		var stat unix.Stat_t
		err = unix.Fstat(fd, &stat)
		if err == nil && stat.Uid != uint32(os.Geteuid()) {
			err = errors.New("tinc identity directory must be owned by this process")
		}
		if err == nil {
			err = unix.Fchmod(fd, 0700)
		}
		unix.Close(fd)
		if err != nil {
			return err
		}
	}
	return nil
}
