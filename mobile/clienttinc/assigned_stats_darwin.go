//go:build darwin && !ios

package clienttinc

import (
	"golang.org/x/sys/unix"
	"net"
	"syscall"
)

func assignedSocketStats(conn net.Conn) map[string]uint64 {
	socket, ok := conn.(syscall.Conn)
	if !ok {
		return nil
	}
	raw, err := socket.SyscallConn()
	if err != nil {
		return nil
	}
	var result map[string]uint64
	raw.Control(func(fd uintptr) {
		i, err := unix.GetsockoptTCPConnectionInfo(int(fd), unix.IPPROTO_TCP, unix.TCP_CONNECTION_INFO)
		if err != nil {
			return
		}
		iface, _ := unix.GetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_BOUND_IF)
		result = map[string]uint64{"state": uint64(i.State), "bound_interface": uint64(iface), "mss": uint64(i.Maxseg), "send_window": uint64(i.Snd_wnd), "send_buffer": uint64(i.Snd_sbbytes), "receive_window": uint64(i.Rcv_wnd), "rto": uint64(i.Rto), "rtt": uint64(i.Rttcur), "tx_bytes": i.Txbytes, "rx_bytes": i.Rxbytes, "retransmit_bytes": i.Txretransmitbytes, "retransmit_packets": i.Txretransmitpackets, "send_scale": uint64(i.Snd_wscale), "receive_scale": uint64(i.Rcv_wscale)}
	})
	return result
}
