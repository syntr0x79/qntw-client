package network

import (
	"context"
	"errors"
	"testing"

	"golang.org/x/sys/windows"
)

func startWinsock(t *testing.T) {
	t.Helper()
	var data windows.WSAData
	if err := windows.WSAStartup(0x202, &data); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = windows.WSACleanup() })
}

func rawSocket(t *testing.T, family, kind int) windows.Handle {
	t.Helper()
	socket, err := windows.Socket(family, kind, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = windows.Closesocket(socket) })
	return socket
}

// This regression test intentionally never calls bind/connect: the control
// callback must be able to inspect and pin these exact newly created sockets.
func TestWindowsUnboundSocketFamilyDoesNotRequireLocalAddress(t *testing.T) {
	startWinsock(t)
	for _, tc := range []struct {
		name         string
		family, kind int
	}{
		{"IPv4 TCP", windows.AF_INET, windows.SOCK_STREAM},
		{"IPv4 UDP", windows.AF_INET, windows.SOCK_DGRAM},
		{"IPv6 TCP rejected", windows.AF_INET6, windows.SOCK_STREAM},
		{"IPv6 UDP rejected", windows.AF_INET6, windows.SOCK_DGRAM},
	} {
		t.Run(tc.name, func(t *testing.T) {
			socket := rawSocket(t, tc.family, tc.kind)
			family, err := socketFamily(socket)
			if err != nil || family != int32(tc.family) {
				t.Fatalf("unbound protocol query: family=%d err=%v", family, err)
			}
			if tc.family == windows.AF_INET6 && pinIPv4Socket(socket, 1) == nil {
				t.Fatal("IPv6 socket accepted by IPv4 interface policy")
			}
		})
	}
}

func TestWindowsPrepareSocketPinsFreshUnboundTCPAndUDP(t *testing.T) {
	startWinsock(t)
	physical, err := Discover(context.Background(), "")
	if err != nil {
		t.Skipf("physical IPv4 uplink unavailable: %v", err)
	}
	for _, kind := range []int{windows.SOCK_STREAM, windows.SOCK_DGRAM} {
		socket := rawSocket(t, windows.AF_INET, kind)
		if err := physical.PrepareSocket(int(socket)); err != nil {
			t.Fatalf("PrepareSocket before bind/connect (kind %d): %v", kind, err)
		}
		const ipUnicastIF = 31
		selected, err := windows.GetsockoptInt(socket, windows.IPPROTO_IP, ipUnicastIF)
		// Windows' IP_UNICAST_IF contract is asymmetric: SET takes network
		// byte order, GET returns host byte order. Do not swap the readback.
		// https://learn.microsoft.com/windows/win32/winsock/ipproto-ip-socket-options
		if err != nil || uint32(selected) != physical.Index() {
			t.Fatalf("interface not pinned: value=%08x expected=%d err=%v", selected, physical.Index(), err)
		}
	}
	for _, kind := range []int{windows.SOCK_STREAM, windows.SOCK_DGRAM} {
		socket := rawSocket(t, windows.AF_INET6, kind)
		if err := physical.PrepareSocket(int(socket)); err == nil {
			t.Fatal("physical IPv6 socket accepted")
		}
	}
}

func TestWindowsClosedSocketProtocolErrorCannotBecomeIPv4(t *testing.T) {
	startWinsock(t)
	socket, err := windows.Socket(windows.AF_INET, windows.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = windows.Closesocket(socket)
	if err := pinIPv4Socket(socket, 1); !errors.Is(err, windows.WSAENOTSOCK) {
		t.Fatalf("closed socket error hidden or treated as IPv4: %v", err)
	}
}
