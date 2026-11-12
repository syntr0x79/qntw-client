package vpn

import (
	"context"
	"github.com/syntr0x79/qntw-client/internal/awgprobe"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/common/signal"
	"github.com/xtls/xray-core/common/task"
	"github.com/xtls/xray-core/transport"
	"time"
)

type awgOutbound struct{ stack *awgprobe.Stack }

func (h *awgOutbound) Tag() string                          { return "vpn" }
func (h *awgOutbound) Start() error                         { return nil }
func (h *awgOutbound) Close() error                         { return h.stack.Close() }
func (h *awgOutbound) SenderSettings() *serial.TypedMessage { return nil }
func (h *awgOutbound) ProxySettings() *serial.TypedMessage  { return nil }
func (h *awgOutbound) Dispatch(ctx context.Context, link *transport.Link) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	idle := signal.CancelAfterInactivity(ctx, cancel, 5*time.Minute)
	defer idle.SetTimeout(0)
	defer common.Close(link.Writer)
	defer common.Interrupt(link.Reader)
	out := session.OutboundsFromContext(ctx)
	if len(out) == 0 {
		return
	}
	destination := out[len(out)-1].Target
	connection, err := h.stack.DialContext(ctx, destination.Network.SystemString(), destination.NetAddr())
	if err != nil {
		return
	}
	defer connection.Close()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			connection.Close()
		case <-done:
		}
	}()
	var reader buf.Reader = buf.NewReader(connection)
	if destination.Network.SystemString() == "udp" {
		reader = buf.NewPacketReader(connection)
		idle.SetTimeout(30 * time.Second)
	}
	_ = task.Run(ctx, func() error {
		err := buf.Copy(link.Reader, buf.NewWriter(connection), buf.UpdateActivity(idle))
		if closeWrite, ok := connection.(interface{ CloseWrite() error }); ok {
			_ = closeWrite.CloseWrite()
		}
		return err
	}, func() error { return buf.Copy(reader, link.Writer, buf.UpdateActivity(idle)) })
}
