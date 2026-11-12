//go:build windows || (darwin && !ios)

package clienttinc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/proxyman"
	"github.com/xtls/xray-core/core"
	"google.golang.org/protobuf/proto"
)

func assignedMuxFixture(t *testing.T) (*core.Config, int) {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	// No real endpoint is contacted: the actual strict binder below stops
	// before bind/connect. Both requests still traverse the real SOCKS, Mux,
	// VLESS and TCP transport implementations in the pinned Xray core.
	document := fmt.Sprintf(`{"log":{"loglevel":"none"},"inbounds":[{"listen":"127.0.0.1","port":%d,"protocol":"socks","settings":{"auth":"noauth","udp":true,"ip":"127.0.0.1"}}],"outbounds":[{"tag":"assigned","protocol":"vless","settings":{"vnext":[{"address":"10.76.0.10","port":18443,"users":[{"id":"d98e0695-35cc-4572-ad36-49300dcd7236","encryption":"none"}]}]},"mux":{"enabled":true,"concurrency":8,"xudpConcurrency":0,"xudpProxyUDP443":"allow"},"streamSettings":{"security":"tls","tlsSettings":{"serverName":"private-fixture.invalid"}}}]}`, port)
	config, err := core.LoadConfig("json", bytes.NewBufferString(document))
	if err != nil {
		t.Fatal(err)
	}
	return config, port
}

func TestWindowsAssignmentRealMuxTCPAndUDPReachOwnedBinder(t *testing.T) {
	for _, protocol := range []string{"tcp", "udp"} {
		t.Run(protocol, func(t *testing.T) {
			prepared := make(chan struct{}, 1)
			owner := newAssignedDialer(netip.MustParseAddrPort("10.76.0.10:18443"), netip.MustParseAddr("10.77.4.1"), func(int) error {
				select {
				case prepared <- struct{}{}:
				default:
				}
				return errors.New("test stops before any overlay connection")
			})
			defer owner.close()
			config, port := assignedMuxFixture(t)
			instance, err := newAssignmentXray(config, owner)
			if err != nil {
				t.Fatal(err)
			}
			defer instance.Close()
			if err = instance.Start(); err != nil {
				t.Fatal(err)
			}
			control, err := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer control.Close()
			control.SetDeadline(time.Now().Add(2 * time.Second))
			control.Write([]byte{5, 1, 0})
			reply := make([]byte, 2)
			if _, err = io.ReadFull(control, reply); err != nil || !bytes.Equal(reply, []byte{5, 0}) {
				t.Fatalf("SOCKS negotiation: %v %v", reply, err)
			}
			if protocol == "tcp" {
				control.Write([]byte{5, 1, 0, 1, 93, 184, 215, 100, 0, 80})
			} else {
				control.Write([]byte{5, 3, 0, 1, 127, 0, 0, 1, 0, 0})
				associate := make([]byte, 10)
				if _, err = io.ReadFull(control, associate); err != nil || associate[1] != 0 || associate[3] != 1 {
					t.Fatalf("SOCKS UDP association: %v %v", associate, err)
				}
				udp, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
				if err != nil {
					t.Fatal(err)
				}
				defer udp.Close()
				server := &net.UDPAddr{IP: net.IP(associate[4:8]), Port: int(binary.BigEndian.Uint16(associate[8:10]))}
				packet := append([]byte{0, 0, 0, 1, 93, 184, 215, 100, 0, 53}, []byte("private UDP Mux fixture")...)
				if _, err = udp.WriteToUDP(packet, server); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case <-prepared:
			case <-time.After(4 * time.Second):
				t.Fatal("real detached Mux transport never reached the strict owned binder")
			}
			owner.close()
		})
	}
}

func TestWindowsAssignmentOwnerConfigIsUniqueAndDoesNotMutateInput(t *testing.T) {
	config, _ := assignedMuxFixture(t)
	first, err := assignmentOwnerConfig(config, 73019)
	if err != nil {
		t.Fatal(err)
	}
	second, err := assignmentOwnerConfig(config, 73020)
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range []*core.Config{config, first, second} {
		settings, err := c.Outbound[0].SenderSettings.GetInstance()
		if err != nil {
			t.Fatal(err)
		}
		sender := settings.(*proxyman.SenderConfig)
		want := []int32{0, 73019, 73020}[i]
		if got := sender.StreamSettings.GetSocketSettings().GetMark(); got != want {
			t.Fatalf("owner config %d changed or lost lifetime: got=%d want=%d", i, got, want)
		}
	}
	if _, err = assignmentOwnerConfig(config, 0); err == nil {
		t.Fatal("ownerless configuration accepted")
	}
	duplicate := proto.Clone(config).(*core.Config)
	duplicate.Outbound = append(duplicate.Outbound, config.Outbound[0])
	if _, err = assignmentOwnerConfig(duplicate, 73021); err == nil {
		t.Fatal("ambiguous assigned outbounds accepted")
	}
}
