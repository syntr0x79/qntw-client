//go:build !windows

package main

import (
	"golang.org/x/sys/unix"
	"os"
)

// Keep the machine-readable protocol separate from libraries that log to fd 1.
func configureJSONOutput() error {
	fd, err := unix.Dup(int(os.Stdout.Fd()))
	if err != nil {
		return err
	}
	unix.CloseOnExec(fd)
	responseOut = os.NewFile(uintptr(fd), "json-response")
	return unix.Dup2(int(os.Stderr.Fd()), int(os.Stdout.Fd()))
}
