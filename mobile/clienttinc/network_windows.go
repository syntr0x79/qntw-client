package clienttinc

import (
	"context"
	"errors"
	"net/http"
	"os/exec"
	"strings"
	"sync/atomic"
	"syscall"

	wnetwork "github.com/syntr0x79/qntw-client/mobile/windows/network"
	"golang.org/x/sys/windows"
)

type tunDevice struct {
	name    string
	session *wnetwork.Session
}
type network struct {
	session *wnetwork.Session
	system  *tunDevice
}

var windowsPhysicalIndex atomic.Uint32

func command(ctx context.Context, input, name string, args ...string) ([]byte, error) {
	c := exec.CommandContext(ctx, name, args...)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	// Native control also creates its loopback TCP socket through the guard.
	// Never inherit a caller-provided interface value; the supervisor owns it.
	var err error
	c.Env, err = trustedNativeEnvironment(windowsPhysicalIndex.Load())
	if err != nil {
		return nil, err
	}
	if input != "" {
		c.Stdin = strings.NewReader(input)
	}
	b, e := c.Output()
	if e != nil {
		return nil, errors.New("native tinc operation failed")
	}
	return b, nil
}

func trustedNativeEnvironment(index uint32) ([]string, error) {
	root, err := windows.GetSystemWindowsDirectory()
	if err != nil {
		return nil, errors.New("cannot locate trusted Windows directory")
	}
	system, err := windows.GetSystemDirectory()
	if err != nil {
		return nil, errors.New("cannot locate trusted Windows system directory")
	}
	return nativeEnvironment(root, system, index), nil
}
func prepareNetwork(ctx context.Context, iface string, system bool, _ string) (*network, error) {
	session, e := wnetwork.Prepare(ctx, wnetwork.Options{Interface: iface, NativeTincPath: windowsNativePath()})
	if e != nil {
		return nil, e
	}
	n := &network{session: session}
	windowsPhysicalIndex.Store(session.PhysicalIndex())
	if system {
		n.system = &tunDevice{name: "qtc0", session: session}
	}
	return n, nil
}
func (n *network) transport() *http.Transport { return n.session.Transport() }
func (n *network) close() error {
	if n != nil && n.session != nil {
		err := n.session.Close()
		windowsPhysicalIndex.Store(0)
		return err
	}
	return nil
}
