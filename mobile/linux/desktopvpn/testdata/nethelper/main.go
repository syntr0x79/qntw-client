// Test-only setuid fixture for a disposable Linux container. Never install it.
package main

import (
	"context"
	"github.com/syntr0x79/qntw-client/mobile/linux/privsep"
	"net"
	"net/netip"
	"os"
	"strconv"
)

type testDNS struct{}

func (testDNS) Apply(context.Context, string, netip.Addr) error { return nil }
func (testDNS) Revert(context.Context, string) error            { return nil }
func main() {
	uid, e := strconv.ParseUint(os.Getenv("TEST_UID"), 10, 32)
	if e != nil {
		os.Exit(1)
	}
	c, e := net.FileConn(os.Stdin)
	if e != nil {
		os.Exit(1)
	}
	u, ok := c.(*net.UnixConn)
	if !ok {
		os.Exit(1)
	}
	if e = privsep.Serve(u, uint32(uid), testDNS{}); e != nil {
		os.Exit(1)
	}
}
