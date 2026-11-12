//go:build !windows

package engine

import "golang.org/x/sys/unix"

func duplicateTunnelDescriptor(fd int) (int, error) { return unix.Dup(fd) }
func closeTunnelDescriptor(fd int) error            { return unix.Close(fd) }
