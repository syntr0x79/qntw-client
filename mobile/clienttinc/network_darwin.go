//go:build darwin && !ios

package clienttinc

import (
	"context"
	macnet "github.com/syntr0x79/qntw-client/mobile/macos/network"
	"net/http"
	"os"
)

type tunDevice struct {
	file   *os.File
	name   string
	device *macnet.Device
}

func newTUN(_ string) (*tunDevice, error) {
	d, e := macnet.NewTUN()
	if e != nil {
		return nil, e
	}
	return &tunDevice{d.File, d.Name, d}, nil
}
func (d *tunDevice) close() {
	if d != nil {
		d.device.Close()
	}
}
func command(ctx context.Context, input, name string, args ...string) ([]byte, error) {
	return macnet.Command(ctx, input, name, args...)
}

type network struct {
	session  *macnet.Session
	system   *tunDevice
	verified bool
}

func prepareNetwork(ctx context.Context, _ string, system bool, _ string) (*network, error) {
	s, e := macnet.Open(ctx, system)
	if e != nil {
		return nil, e
	}
	if e = s.PinControlPlane(ctx); e != nil {
		s.Close()
		return nil, e
	}
	n := &network{session: s}
	if s.System != nil {
		n.system = &tunDevice{s.System.File, s.System.Name, s.System}
	}
	return n, nil
}
func (n *network) prepare(fd int) error                           { return n.session.PrepareSocket(fd) }
func (n *network) transport() *http.Transport                     { return n.session.Transport() }
func (n *network) applyDNS(ctx context.Context, a []string) error { return n.session.ApplyDNS(ctx, a) }
func (n *network) close() error {
	if n != nil {
		return n.session.Close()
	}
	return nil
}

func (n *network) activate(ctx context.Context) error {
	if n.verified {
		return nil // Subsequent readiness already checked the active system path.
	}
	if e := n.session.Activate(ctx); e != nil {
		return e
	}
	if e := n.session.VerifySystem(ctx); e != nil {
		return e
	}
	n.verified = true
	return nil
}

func (n *network) checkActiveHealth(ctx context.Context) (bool, error) {
	return n.session.VerifyIfActive(ctx)
}
