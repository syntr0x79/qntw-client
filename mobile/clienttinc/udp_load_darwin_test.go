//go:build darwin && !ios

package clienttinc

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/amnezia-vpn/amneziawg-go/v3/tun/netstack"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/mux"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/platform"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/transport/internet"
	"golang.org/x/sys/unix"
)

// Only this test substitutes the physical overlay with a loopback TLS peer.
// The actual Darwin TUN, UDP lifetime wrapper, Mux and VLESS remain in use.
// No host interface, route, DNS setting or server configuration is changed.
type loopbackAssignmentTestDialer struct {
	owner *assignedDialer
	peer  string
}

func (d loopbackAssignmentTestDialer) DestIpAddress() xnet.IP { return nil }
func (d loopbackAssignmentTestDialer) Dial(ctx context.Context, source xnet.Address, dest xnet.Destination, options *internet.SocketConfig) (net.Conn, error) {
	if options == nil || options.Mark != d.owner.id {
		return nil, errors.New("test transport is restricted to its assignment")
	}
	if err := validateAssignedDestination(d.owner.target, d.owner.self, source, dest); err != nil {
		return nil, err
	}
	return (&net.Dialer{}).DialContext(ctx, "tcp4", d.peer)
}

func TestDarwinTUN1024UDPSourcesDoNotExhaust64MuxConnections(t *testing.T) {
	seed := httptest.NewTLSServer(http.NotFoundHandler())
	certificate, tlsConfig := seed.Certificate(), seed.TLS.Clone()
	seed.Close()
	listener, err := tls.Listen("tcp4", "127.0.0.1:0", tlsConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var current, peak, refused, ended, nonzeroID atomic.Int32
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			n := current.Add(1)
			for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
			}
			if n > 64 {
				refused.Add(1)
				current.Add(-1)
				conn.Close()
				continue
			}
			go func() {
				defer conn.Close()
				defer current.Add(-1)
				conn.SetDeadline(time.Now().Add(80 * time.Second))
				header := make([]byte, 19)
				if _, err := io.ReadFull(conn, header); err != nil || header[0] != 0 || header[17] != 0 || header[18] != 3 {
					return
				}
				if _, err := conn.Write([]byte{0, 0}); err != nil {
					return
				}
				targets := make(map[uint16]xnet.Destination)
				for {
					var frame mux.FrameMetadata
					if err := frame.Unmarshal(conn, false); err != nil {
						return
					}
					if frame.SessionStatus == mux.SessionStatusNew {
						targets[frame.SessionID] = frame.Target
						if frame.Target.Network == xnet.Network_UDP && frame.GlobalID != [8]byte{} {
							nonzeroID.Add(1)
						}
					}
					if frame.SessionStatus == mux.SessionStatusEnd {
						delete(targets, frame.SessionID)
						ended.Add(1)
					}
					if !frame.Option.Has(mux.OptionData) {
						continue
					}
					var size uint16
					if binary.Read(conn, binary.BigEndian, &size) != nil {
						return
					}
					payload := make([]byte, size)
					if _, err := io.ReadFull(conn, payload); err != nil {
						return
					}
					target := targets[frame.SessionID]
					if frame.Target.Network != xnet.Network_Unknown {
						target = frame.Target
					}
					response := buf.New()
					if target.Network == xnet.Network_UDP {
						response.UDP = &target
					}
					meta := mux.FrameMetadata{SessionID: frame.SessionID, SessionStatus: mux.SessionStatusKeep, Option: mux.OptionData}
					if meta.WriteTo(response) != nil {
						response.Release()
						return
					}
					wire := append([]byte(nil), response.Bytes()...)
					response.Release()
					wire = binary.BigEndian.AppendUint16(wire, uint16(len(payload)))
					wire = append(wire, payload...)
					if _, err := conn.Write(wire); err != nil {
						return
					}
				}
			}()
		}
	}()
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_DGRAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	unix.SetNonblock(pair[0], true)
	unix.SetNonblock(pair[1], true)
	xrayFD, kernel := os.NewFile(uintptr(pair[0]), "isolated-utun"), os.NewFile(uintptr(pair[1]), "isolated-kernel")
	defer xrayFD.Close()
	defer kernel.Close()
	device, client, err := netstack.CreateNetTUN([]netip.Addr{netip.MustParseAddr("198.18.94.1")}, nil, 1280)
	if err != nil {
		t.Fatal(err)
	}
	defer device.Close()
	go bridgeKernel(device, kernel)
	c := configFixture(t)
	c.Assignment.TLSName = certificate.DNSNames[0]
	c.Assignment.CertificatePEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw}))
	var document map[string]any
	if err := json.Unmarshal(xrayDocument(c, false), &document); err != nil {
		t.Fatal(err)
	}
	document["inbounds"] = []any{map[string]any{"tag": "system-tun", "protocol": "tun", "settings": map[string]any{"name": "isolated-utun", "mtu": 1280}}}
	raw, _ := json.Marshal(document)
	config, err := core.LoadConfig("json", strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	owner := newAssignedDialer(netip.AddrPortFrom(netip.MustParseAddr(c.Assignment.Address), c.Assignment.Port), netip.MustParseAddr(c.Self.Address), func(int) error { return errors.New("external socket forbidden in test") })
	defer owner.close()
	t.Setenv(platform.TunFdKey, strconv.Itoa(pair[0]))
	instance, err := newAssignmentXray(config, owner)
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()
	internet.UseAlternativeSystemDialer(loopbackAssignmentTestDialer{owner: owner, peer: listener.Addr().String()})
	defer internet.UseAlternativeSystemDialer(windowsAssignedSystemDialer{})
	if err := instance.Start(); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	for i := 0; i < 1024; i++ {
		// Match the previously failing sustained rate, rather than a burst
		// which legitimately needs more than the fixed server capacity.
		if delay := time.Until(start.Add(time.Duration(i) * 50 * time.Millisecond)); delay > 0 {
			time.Sleep(delay)
		}
		conn, err := client.DialUDPAddrPort(netip.AddrPortFrom(netip.MustParseAddr("198.18.94.1"), uint16(30000+i)), netip.MustParseAddrPort("1.1.1.1:53"))
		if err != nil {
			t.Fatal(err)
		}
		conn.SetDeadline(time.Now().Add(2 * time.Second))
		payload := []byte(fmt.Sprintf("isolated-udp-%04d", i))
		_, err = conn.Write(payload)
		got := make([]byte, 128)
		n, readErr := conn.Read(got)
		conn.Close()
		if err != nil || readErr != nil || !bytes.Equal(got[:n], payload) {
			t.Fatalf("UDP source %d failed: write=%v read=%v peak=%d refused=%d", i, err, readErr, peak.Load(), refused.Load())
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := client.DialContext(ctx, "tcp4", "93.184.215.100:443")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	payload := []byte("TCP remains usable after 1024 UDP sources")
	if _, err := conn.Write(payload); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("post-load TCP: %v", err)
	}
	if refused.Load() != 0 || peak.Load() >= 64 || ended.Load() < 512 || nonzeroID.Load() != 0 {
		t.Fatalf("peak=%d refused=%d expired_substreams=%d nonzero_ids=%d", peak.Load(), refused.Load(), ended.Load(), nonzeroID.Load())
	}
	t.Logf("1024/1024 UDP source replies; subsequent TCP passed; peak physical TLS=%d/64; reclaimed substreams=%d; zero unauthorized XUDP IDs", peak.Load(), ended.Load())
}
