package desktopvpn

import (
	"bufio"
	"context"
	"github.com/syntr0x79/qntw-client/mobile/windows/elevation"
)

func Run(ctx context.Context, start elevation.Packet, reader *bufio.Reader, publish func(elevation.Packet)) error {
	return run(ctx, start, reader, publish, func(ctx context.Context, mode string) (privateChannel, error) { return elevation.Start(ctx, mode) })
}
