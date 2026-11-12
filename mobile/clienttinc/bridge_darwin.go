//go:build darwin && !ios

package clienttinc

import (
	"encoding/binary"
	"errors"
	"io"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

var errBridgePacketDropped = errors.New("packet queue remained full; packet dropped")

// Darwin AF_UNIX datagrams can return ENOBUFS instead of EAGAIN under a
// short burst. That drops one IP packet; it must not end the session's bridge.
// Retry briefly for backpressure, then let TCP retransmit instead of blocking
// tinc's event loop indefinitely. Each successful write is one whole packet.
func writeBridgePacket(w io.Writer, packet []byte) error {
	deadline := time.Now().Add(50 * time.Millisecond)
	delay := time.Millisecond
	for {
		n, err := w.Write(packet)
		if err == nil {
			if n != len(packet) {
				return io.ErrShortWrite
			}
			return nil
		}
		if !errors.Is(err, unix.ENOBUFS) && !errors.Is(err, unix.EAGAIN) && !errors.Is(err, unix.EINTR) {
			return err
		}
		if n != 0 {
			return io.ErrShortWrite
		}
		if remaining := time.Until(deadline); remaining <= 0 {
			return errBridgePacketDropped
		} else if delay > remaining {
			delay = remaining
		}
		time.Sleep(delay)
		if delay < 8*time.Millisecond {
			delay *= 2
		}
	}
}

func bridgeUTUN(tun, raw *os.File) { bridgeUTUNObserved(tun, raw, nil) }

func bridgeUTUNObserved(tun, raw *os.File, observer func(string, error)) {
	failed := func(direction string, err error) {
		if observer != nil {
			observer(direction, err)
		}
	}
	go func() {
		b := make([]byte, 65540)
		for {
			n, err := tun.Read(b)
			if err != nil {
				failed("read_utun", err)
				return
			}
			if n <= 4 {
				continue
			}
			if err := writeBridgePacket(raw, b[4:n]); err != nil {
				if errors.Is(err, errBridgePacketDropped) {
					failed("congestion_packet_fd", err)
					continue
				}
				failed("write_packet_fd", err)
				return
			}
		}
	}()
	b := make([]byte, 65540)
	for {
		n, err := raw.Read(b[4:])
		if err != nil {
			failed("read_packet_fd", err)
			return
		}
		if n < 1 {
			continue
		}
		family := uint32(unix.AF_INET)
		if b[4]>>4 == 6 {
			family = unix.AF_INET6
		}
		binary.BigEndian.PutUint32(b[:4], family)
		if err := writeBridgePacket(tun, b[:n+4]); err != nil {
			if errors.Is(err, errBridgePacketDropped) {
				failed("congestion_utun", err)
				continue
			}
			failed("write_utun", err)
			return
		}
	}
}
