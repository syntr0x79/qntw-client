//go:build linux

// Package privsep contains the experimental local network-helper protocol.
// The privileged side sees only tunnel address and DNS, never VPN credentials.
package privsep

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"time"

	netroute "github.com/libp2p/go-netroute"

	"github.com/syntr0x79/qntw-client/mobile/linux/tun"
	"github.com/syntr0x79/qntw-client/mobile/linux/vpn"
	"golang.org/x/sys/unix"
)

type message struct {
	Type    string `json:"type"`
	Address string `json:"address,omitempty"`
	DNS     string `json:"dns,omitempty"`
	Name    string `json:"name,omitempty"`
}

const maxPacket = 4096

func receive(c *net.UnixConn) (message, []byte, error) {
	buf := make([]byte, maxPacket)
	oob := make([]byte, unix.CmsgSpace(4))
	n, on, flags, _, err := c.ReadMsgUnix(buf, oob)
	if err != nil {
		return message{}, nil, err
	}
	if n == 0 || flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) != 0 {
		closeRights(oob[:on])
		return message{}, nil, errors.New("invalid packet")
	}
	var m message
	if err = json.Unmarshal(buf[:n], &m); err != nil {
		closeRights(oob[:on])
		return message{}, nil, errors.New("invalid packet")
	}
	return m, oob[:on], nil
}
func closeRights(oob []byte) {
	messages, _ := unix.ParseSocketControlMessage(oob)
	for _, msg := range messages {
		fds, _ := unix.ParseUnixRights(&msg)
		for _, fd := range fds {
			_ = unix.Close(fd)
		}
	}
}
func send(c *net.UnixConn, m message, fd int) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	var oob []byte
	if fd >= 0 {
		oob = unix.UnixRights(fd)
	}
	n, _, err := c.WriteMsgUnix(data, oob, nil)
	if err != nil {
		return err
	}
	if n != len(data) {
		return errors.New("short packet")
	}
	return nil
}
func checkPeer(c *net.UnixConn, uid uint32) error {
	raw, err := c.SyscallConn()
	if err != nil {
		return err
	}
	var cred *unix.Ucred
	var controlErr error
	if err = raw.Control(func(fd uintptr) { cred, controlErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) }); err != nil {
		return err
	}
	if controlErr != nil {
		return controlErr
	}
	if cred == nil || cred.Uid != uid {
		return errors.New("unexpected peer")
	}
	return nil
}

// Serve owns the privileged network state until the peer sends stop or exits.
// DNS must be a link-scoped implementation. It always removes its routes before
// releasing the TUN, including when a peer retains a duplicate descriptor.
func Serve(c *net.UnixConn, uid uint32, dns vpn.DNS) (err error) {
	if c == nil || dns == nil {
		return errors.New("missing channel or DNS")
	}
	defer c.Close()
	if err = checkPeer(c, uid); err != nil {
		return err
	}
	_ = c.SetReadDeadline(time.Now().Add(20 * time.Second))
	initial, extra, err := receive(c)
	if len(extra) != 0 {
		closeRights(extra)
	}
	if err != nil || len(extra) != 0 || initial.Type != "create" {
		return errors.New("invalid create request")
	}
	address, err := netip.ParsePrefix(initial.Address)
	if err != nil || !address.Addr().Is4() || address.Bits() != 32 {
		return errors.New("invalid tunnel address")
	}
	dnsIP, err := netip.ParseAddr(initial.DNS)
	if err != nil || !dnsIP.Is4() || dnsIP.IsUnspecified() || dnsIP.IsMulticast() || dnsIP.IsLoopback() {
		return errors.New("invalid DNS address")
	}
	dev, err := tun.Create()
	if err != nil {
		return err
	}
	defer dev.Close()
	if err = dev.Configure(address, 1280); err != nil {
		return err
	}
	if err = send(c, message{Type: "created", Name: dev.Name}, int(dev.File.Fd())); err != nil {
		return err
	}
	next, extra, err := receive(c)
	if len(extra) != 0 {
		closeRights(extra)
	}
	if err != nil || len(extra) != 0 || next.Type != "activate" {
		return errors.New("invalid activate request")
	}
	router, err := netroute.New()
	if err != nil {
		return err
	}
	physical, _, _, err := router.Route(net.ParseIP("1.1.1.1"))
	if err != nil || physical == nil {
		return errors.New("no physical network")
	}
	dnsAttempted := true
	var policy *tun.PolicyRoutes
	defer func() {
		var clean error
		if policy != nil {
			clean = errors.Join(clean, policy.Remove())
		}
		if dnsAttempted {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			clean = errors.Join(clean, dns.Revert(ctx, dev.Name))
			cancel()
		}
		err = errors.Join(err, clean)
		if err == nil {
			_ = send(c, message{Type: "stopped"}, -1)
		} else {
			_ = send(c, message{Type: "cleanup_error"}, -1)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	err = dns.Apply(ctx, dev.Name, dnsIP)
	cancel()
	if err != nil {
		return fmt.Errorf("DNS: %w", err)
	}
	policy, err = dev.InstallPolicy(physical.Name)
	if err != nil {
		return err
	}
	if err = send(c, message{Type: "active"}, -1); err != nil {
		return err
	}
	_ = c.SetReadDeadline(time.Time{})
	last, extra, readErr := receive(c)
	if len(extra) != 0 {
		closeRights(extra)
	}
	if readErr == nil && (len(extra) != 0 || last.Type != "stop") {
		return errors.New("invalid stop request")
	}
	// EOF or process loss has the same cleanup semantics as an explicit stop.
	return nil
}

// NewPair creates a private packet socketpair; the child file may be used as
// stdin for pkexec. The caller owns both returned descriptors.
func NewPair() (*net.UnixConn, *os.File, error) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_SEQPACKET|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	a := os.NewFile(uintptr(fds[0]), "quantway-parent")
	b := os.NewFile(uintptr(fds[1]), "quantway-child")
	conn, err := net.FileConn(a)
	a.Close()
	if err != nil {
		b.Close()
		return nil, nil, err
	}
	u, ok := conn.(*net.UnixConn)
	if !ok {
		conn.Close()
		b.Close()
		return nil, nil, errors.New("not a Unix socket")
	}
	return u, b, nil
}
