//go:build linux && !android

// Package tincvpn connects the unprivileged desktop to its dedicated root tinc
// helper. Credentials cross only an inherited private packet socket.
package tincvpn

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/syntr0x79/qntw-client/mobile/clienttinc"
	"golang.org/x/sys/unix"
)

type Status = clienttinc.Status

const maxPacket = 8192

// Keep this primitive independent of the ordinary VPN helper. Importing that
// helper's privsep package also imports mobile/engine, whose init installs a
// process-wide Xray dialer that requires the ordinary engine's socket owner.
// The dedicated tinc runtime must keep Xray's independent system dialer.
func newPair() (*net.UnixConn, *os.File, error) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_SEQPACKET|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	parent := os.NewFile(uintptr(fds[0]), "quantway-tinc-parent")
	child := os.NewFile(uintptr(fds[1]), "quantway-tinc-child")
	conn, err := net.FileConn(parent)
	parent.Close()
	if err != nil {
		child.Close()
		return nil, nil, err
	}
	peer, ok := conn.(*net.UnixConn)
	if !ok {
		conn.Close()
		child.Close()
		return nil, nil, errors.New("invalid private socket")
	}
	return peer, child, nil
}

type packet struct {
	Type     string  `json:"type"`
	Origin   string  `json:"origin,omitempty"`
	Token    string  `json:"token,omitempty"`
	Country  string  `json:"country,omitempty"`
	ID       uint64  `json:"id,omitempty"`
	Status   *Status `json:"status,omitempty"`
	Error    string  `json:"error,omitempty"`
	Code     string  `json:"code,omitempty"`
	WantMesh bool    `json:"want_mesh,omitempty"`
}

func receive(c *net.UnixConn) (packet, error) {
	buf := make([]byte, maxPacket)
	oob := make([]byte, unix.CmsgSpace(256*4))
	n, on, flags, _, err := c.ReadMsgUnix(buf, oob)
	if on > 0 {
		messages, _ := unix.ParseSocketControlMessage(oob[:on])
		for _, m := range messages {
			fds, _ := unix.ParseUnixRights(&m)
			for _, fd := range fds {
				_ = unix.Close(fd)
			}
		}
	}
	if err != nil {
		return packet{}, err
	}
	if n == 0 {
		return packet{}, io.EOF
	}
	if on != 0 || flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) != 0 {
		return packet{}, errors.New("invalid private packet")
	}
	var p packet
	d := json.NewDecoder(bytes.NewReader(buf[:n]))
	d.DisallowUnknownFields()
	if d.Decode(&p) != nil || d.Decode(new(any)) != io.EOF {
		return packet{}, errors.New("invalid private packet")
	}
	return p, nil
}

// One writer per connection serializes all replies and status updates.
type writer struct {
	mu   sync.Mutex
	conn *net.UnixConn
}

func (w *writer) send(p packet) error {
	b, err := json.Marshal(p)
	// Optional observation data must not tear down an otherwise valid VPN if
	// a future larger ingress catalog exceeds the existing private IPC budget.
	if err == nil && len(b) > maxPacket && p.Type == "status" && p.Status != nil && p.Status.Mesh != nil {
		status := copyStatus(*p.Status)
		status.Mesh = &clienttinc.MeshStatus{ObservationState: "unavailable", ObservedAt: status.Mesh.ObservedAt, Egress: status.Mesh.Egress}
		p.Status = &status
		b, err = json.Marshal(p)
	}
	if err != nil || len(b) > maxPacket {
		return errors.New("invalid private response")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = w.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	n, err := w.conn.Write(b)
	if err != nil || n != len(b) {
		return errors.New("private channel closed")
	}
	return nil
}

func checkPeer(c *net.UnixConn, uid uint32) error {
	raw, err := c.SyscallConn()
	if err != nil {
		return errors.New("invalid private peer")
	}
	var cred *unix.Ucred
	var sockErr error
	if err = raw.Control(func(fd uintptr) {
		cred, sockErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil || sockErr != nil || cred == nil || cred.Uid != uid {
		return errors.New("unexpected private peer")
	}
	return nil
}

func countryValid(code string) bool {
	return len(code) == 2 && code[0] >= 'A' && code[0] <= 'Z' && code[1] >= 'A' && code[1] <= 'Z'
}

func copyStatus(s Status) Status { return clienttinc.CloneStatus(s) }

// keepLatest never holds up a lease watchdog, a socket reader or a GTK callback.
func keepLatest(ch chan Status, s Status) {
	s = copyStatus(s)
	select {
	case ch <- s:
		return
	default:
	}
	select {
	case <-ch:
	default:
	}
	select {
	case ch <- s:
	default:
	}
}
