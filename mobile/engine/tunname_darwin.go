//go:build darwin && !ios

package engine

import "golang.org/x/sys/unix"

func tunName(fd int) (string, error) { return unix.GetsockoptString(fd, 2, 2) }
