//go:build darwin && !ios

package clienttinc

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	awgtun "github.com/amnezia-vpn/amneziawg-go/v3/tun"
	"github.com/amnezia-vpn/amneziawg-go/v3/tun/netstack"
	"github.com/xtls/xray-core/common/platform"
	"github.com/xtls/xray-core/core"
	"golang.org/x/sys/unix"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Model the kernel side of utun with a TCP/IP stack and packet socket. This
// exercises the real Darwin Xray TUN inbound, not its separate SOCKS listener.
func TestDarwinTUNFeedsTheOwnedAssignedOutbound(t *testing.T) {
	pair, e := unix.Socketpair(unix.AF_UNIX, unix.SOCK_DGRAM, 0)
	if e != nil {
		t.Fatal(e)
	}
	unix.SetNonblock(pair[0], true)
	unix.SetNonblock(pair[1], true)
	xrayFD := os.NewFile(uintptr(pair[0]), "fake-utun")
	defer xrayFD.Close()
	kernel := os.NewFile(uintptr(pair[1]), "kernel")
	defer kernel.Close()
	device, client, e := netstack.CreateNetTUN([]netip.Addr{netip.MustParseAddr("198.18.94.1")}, nil, 1280)
	if e != nil {
		t.Fatal(e)
	}
	defer device.Close()
	go bridgeKernel(device, kernel)
	var document map[string]any
	if e = json.Unmarshal(xrayDocument(configFixture(t), false), &document); e != nil {
		t.Fatal(e)
	}
	document["inbounds"] = []any{map[string]any{"tag": "system-tun", "protocol": "tun", "settings": map[string]any{"name": "utun-test", "mtu": 1280}}}
	raw, _ := json.Marshal(document)
	config, e := core.LoadConfig("json", strings.NewReader(string(raw)))
	if e != nil {
		t.Fatal(e)
	}
	called := make(chan struct{}, 1)
	owner := newAssignedDialer(netip.MustParseAddrPort("10.76.0.4:18443"), netip.MustParseAddr("10.77.1.1"), func(int) error {
		select {
		case called <- struct{}{}:
		default:
		}
		return errors.New("test stops before external networking")
	})
	defer owner.close()
	t.Setenv(platform.TunFdKey, strconv.Itoa(pair[0]))
	instance, e := newAssignmentXray(config, owner)
	if e != nil {
		t.Fatal(e)
	}
	defer instance.Close()
	if e = instance.Start(); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, e := client.DialContext(ctx, "tcp4", "1.1.1.1:443")
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	conn.SetWriteDeadline(time.Now().Add(time.Second))
	conn.Write([]byte("test payload"))
	select {
	case <-called:
	case <-ctx.Done():
		t.Fatal("Darwin TUN accepted TCP but never dispatched its traffic to the assignment")
	}
}
func bridgeKernel(device awgtun.Device, kernel *os.File) {
	go func() {
		buffer := make([]byte, 65540)
		sizes := make([]int, 1)
		for {
			n, e := device.Read([][]byte{buffer}, sizes, 4)
			if e != nil {
				return
			}
			if n > 0 {
				family := uint32(unix.AF_INET)
				if buffer[4]>>4 == 6 {
					family = unix.AF_INET6
				}
				binary.BigEndian.PutUint32(buffer[:4], family)
				if _, e = kernel.Write(buffer[:sizes[0]+4]); e != nil {
					return
				}
			}
		}
	}()
	b := make([]byte, 65540)
	for {
		n, e := kernel.Read(b)
		if e != nil {
			return
		}
		if n > 4 {
			if _, e = device.Write([][]byte{b[4:n]}, 0); e != nil {
				return
			}
		}
	}
}
