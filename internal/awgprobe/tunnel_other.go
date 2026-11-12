//go:build !android && !linux && (!darwin || ios)

package awgprobe

import (
	"context"
	"errors"
	"io"
)

func StartTunnel(ctx context.Context, fd int, conf string, options Options) (io.Closer, error) {
	return nil, errors.New("system TUN is unsupported on this platform")
}
