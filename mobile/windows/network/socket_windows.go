//go:build windows

package network

import (
	"errors"
	"math/bits"
	"unsafe"

	"golang.org/x/sys/windows"
)

// SO_PROTOCOL_INFOW works on sockets in any state, including net.Dialer.Control
// before bind/connect. getsockname is unsuitable here: Winsock returns WSAEINVAL
// for an unbound socket. The numeric option is from the Windows SDK Winsock2.h.
// https://learn.microsoft.com/windows/win32/api/winsock2/nf-winsock2-getsockopt
const soProtocolInfoW = 0x2005

func socketFamily(socket windows.Handle) (int32, error) {
	var protocol windows.WSAProtocolInfo
	size := int32(unsafe.Sizeof(protocol))
	if err := windows.Getsockopt(socket, windows.SOL_SOCKET, soProtocolInfoW, (*byte)(unsafe.Pointer(&protocol)), &size); err != nil {
		return 0, err
	}
	if size != int32(unsafe.Sizeof(protocol)) {
		return 0, errors.New("unexpected Winsock protocol information size")
	}
	return protocol.AddressFamily, nil
}

func pinIPv4Socket(socket windows.Handle, index uint32) error {
	if index == 0 {
		return errors.New("network interface index is unavailable")
	}
	family, err := socketFamily(socket)
	if err != nil {
		return err
	}
	if family != windows.AF_INET {
		return errors.New("only IPv4 transport sockets are supported")
	}
	// IP_UNICAST_IF expects the DWORD interface index in network byte order.
	const ipUnicastIF = 31
	return windows.SetsockoptInt(socket, windows.IPPROTO_IP, ipUnicastIF, int(bits.ReverseBytes32(index)))
}
