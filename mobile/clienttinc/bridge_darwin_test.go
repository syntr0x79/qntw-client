//go:build darwin && !ios

package clienttinc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func bridgeTestPair(t *testing.T) ([2]int, [2]*os.File) {
	t.Helper()
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_DGRAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	var files [2]*os.File
	for i, fd := range pair {
		unix.SetNonblock(fd, true)
		files[i] = os.NewFile(uintptr(fd), "isolated-packet")
		t.Cleanup(func() { files[i].Close() })
	}
	if err := unix.SetsockoptInt(pair[1], unix.SOL_SOCKET, unix.SO_RCVBUF, 4096); err != nil {
		t.Fatal(err)
	}
	return pair, files
}

func fillBridgeQueue(t *testing.T, fd int) {
	t.Helper()
	for i := 0; i < 4096; i++ {
		_, err := unix.Write(fd, bytes.Repeat([]byte{0x45}, 1280))
		if errors.Is(err, unix.ENOBUFS) || errors.Is(err, unix.EAGAIN) {
			t.Logf("reproduced actual datagram queue congestion: %v", err)
			return
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal("test did not fill the actual datagram queue")
}

func TestDarwinBridgeRetriesRealCongestionWithoutDamagingPacket(t *testing.T) {
	pair, files := bridgeTestPair(t)
	fillBridgeQueue(t, pair[0])
	marker := bytes.Repeat([]byte{0x46}, 1280)
	received := make(chan error, 1)
	go func() {
		time.Sleep(10 * time.Millisecond)
		files[1].SetReadDeadline(time.Now().Add(time.Second))
		b := make([]byte, 2048)
		for {
			n, err := files[1].Read(b)
			if err != nil {
				received <- err
				return
			}
			if bytes.Equal(b[:n], marker) {
				received <- nil
				return
			}
		}
	}()
	if err := writeBridgePacket(files[0], marker); err != nil {
		t.Fatal(err)
	}
	if err := <-received; err != nil {
		t.Fatal(err)
	}
}

func TestDarwinBridgeSurvivesAPacketDroppedAfterPersistentCongestion(t *testing.T) {
	_, tun := bridgeTestPair(t)
	rawFD, raw := bridgeTestPair(t)
	fillBridgeQueue(t, rawFD[0])
	events := make(chan string, 8)
	go bridgeUTUNObserved(tun[0], raw[0], func(direction string, _ error) {
		select {
		case events <- direction:
		default:
		}
	})
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, unix.AF_INET)
	first := append(append([]byte(nil), header...), bytes.Repeat([]byte{0x45}, 1280)...)
	if _, err := tun[1].Write(first); err != nil {
		t.Fatal(err)
	}
	select {
	case direction := <-events:
		if direction != "congestion_packet_fd" {
			t.Fatalf("unexpected bridge failure: %s", direction)
		}
	case <-time.After(time.Second):
		t.Fatal("packet congestion was not bounded")
	}
	b := make([]byte, 2048)
	for {
		_, err := unix.Read(rawFD[1], b)
		if errors.Is(err, unix.EAGAIN) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	marker := bytes.Repeat([]byte{0x46}, 1280)
	if _, err := tun[1].Write(append(header, marker...)); err != nil {
		t.Fatal(err)
	}
	raw[1].SetReadDeadline(time.Now().Add(time.Second))
	n, err := raw[1].Read(b)
	if err != nil || !bytes.Equal(b[:n], marker) {
		t.Fatalf("bridge stopped after a single congested packet: %v", err)
	}
}
