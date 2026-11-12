package probe

import (
	"context"
	"errors"
	wnetwork "github.com/syntr0x79/qntw-client/mobile/windows/network"
)

type network struct {
	physical    *wnetwork.Physical
	fingerprint string
}

func currentNetwork() (*network, error) {
	p, e := wnetwork.Discover(context.Background(), "")
	if e != nil {
		return nil, e
	}
	fingerprint, e := p.Snapshot()
	if e != nil {
		return nil, e
	}
	return &network{p, fingerprint}, nil
}
func (n *network) snapshot() (string, error) { return n.physical.Snapshot() }
func (n *network) PrepareSocket(fd int) error {
	s, e := n.snapshot()
	if e != nil || s != n.fingerprint {
		return errors.New("physical network changed")
	}
	return n.physical.PrepareSocket(fd)
}
func (n *network) Resolve(host string) (string, error) { return n.physical.Resolve(host) }
