//go:build linux && !android

package engine

import (
	"fmt"
	"golang.org/x/sys/unix"
)

func tunName(fd int) (string, error) {
	req, err := unix.NewIfreq("")
	if err != nil {
		return "", err
	}
	if err = unix.IoctlIfreq(fd, unix.TUNGETIFF, req); err != nil {
		return "", fmt.Errorf("identify TUN: %w", err)
	}
	return req.Name(), nil
}
