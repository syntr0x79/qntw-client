//go:build linux

package privsep

import (
	"errors"
	"net"
	"net/netip"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// RequestTunnel obtains a TUN descriptor over the private socketpair.
func RequestTunnel(c *net.UnixConn, address netip.Prefix, dns netip.Addr) (*os.File, string, error) {
	if c == nil || !address.IsValid() || !dns.IsValid() {
		return nil, "", errors.New("invalid request")
	}
	_ = c.SetReadDeadline(time.Now().Add(120 * time.Second))
	if err := send(c, message{Type: "create", Address: address.String(), DNS: dns.String()}, -1); err != nil {
		return nil, "", err
	}
	m, oob, err := receive(c)
	if err != nil {
		return nil, "", err
	}
	messages, err := unix.ParseSocketControlMessage(oob)
	if err != nil {
		closeRights(oob)
		return nil, "", errors.New("invalid descriptor")
	}
	fds := []int{}
	for _, msg := range messages {
		rights, e := unix.ParseUnixRights(&msg)
		if e != nil {
			for _, fd := range fds {
				unix.Close(fd)
			}
			return nil, "", errors.New("invalid descriptor")
		}
		fds = append(fds, rights...)
	}
	if m.Type != "created" || len(fds) != 1 || m.Name == "" {
		for _, fd := range fds {
			unix.Close(fd)
		}
		return nil, "", errors.New("invalid helper response")
	}
	fd := fds[0]
	unix.CloseOnExec(fd)
	req, err := unix.NewIfreq("")
	if err != nil {
		unix.Close(fd)
		return nil, "", err
	}
	if err = unix.IoctlIfreq(fd, unix.TUNGETIFF, req); err != nil || req.Name() != m.Name || req.Uint16()&unix.IFF_TUN == 0 || req.Uint16()&unix.IFF_NO_PI == 0 {
		unix.Close(fd)
		return nil, "", errors.New("unexpected TUN descriptor")
	}
	return os.NewFile(uintptr(fd), m.Name), m.Name, nil
}
func Activate(c *net.UnixConn) error {
	_ = c.SetReadDeadline(time.Now().Add(30 * time.Second))
	if err := send(c, message{Type: "activate"}, -1); err != nil {
		return err
	}
	m, oob, err := receive(c)
	if len(oob) != 0 {
		closeRights(oob)
		return errors.New("unexpected descriptor")
	}
	if err != nil {
		return err
	}
	if m.Type != "active" {
		return errors.New("helper did not activate")
	}
	_ = c.SetReadDeadline(time.Time{})
	return nil
}
func Stop(c *net.UnixConn) error {
	_ = c.SetReadDeadline(time.Now().Add(15 * time.Second))
	if err := send(c, message{Type: "stop"}, -1); err != nil {
		return err
	}
	m, oob, err := receive(c)
	if len(oob) != 0 {
		closeRights(oob)
		return errors.New("unexpected descriptor")
	}
	if err != nil {
		return err
	}
	if m.Type != "stopped" {
		return errors.New("helper cleanup failed")
	}
	return nil
}
