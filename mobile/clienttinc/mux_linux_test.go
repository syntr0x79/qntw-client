//go:build linux && !android

package clienttinc

import (
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xtls/xray-core/common/mux"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/platform"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/core"
)

func TestAssignmentMuxSendsZeroGlobalIDOnRealUDPWire(t *testing.T) {
	t.Run("restricted_assignment", func(t *testing.T) {
		if got := observedMuxID(t, newAssignmentXray); got != [8]byte{} {
			t.Fatal("assignment mux retained an authorization-independent XUDP identity")
		}
	})
	t.Run("unrestricted_core_control", func(t *testing.T) {
		if got := observedMuxID(t, core.New); got == [8]byte{} {
			t.Fatal("wire control failed to expose the stock nonzero XUDP identity")
		}
	})
}

func observedMuxID(t *testing.T, create func(*core.Config) (*core.Instance, error)) [8]byte {
	t.Helper()
	// Observe decrypted test frames at a real TLS VLESS endpoint. No private
	// Xray fields or synthetic context stand in for the constructor capture.
	seed := httptest.NewTLSServer(http.NotFoundHandler())
	certificate, tlsConfig := seed.Certificate(), seed.TLS.Clone()
	seed.Close()
	listener, err := tls.Listen("tcp4", "127.0.0.1:0", tlsConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	type observed struct {
		ids [][8]byte
		err error
	}
	frames := make(chan observed, 1)
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			frames <- observed{err: err}
			return
		}
		defer connection.Close()
		connection.SetDeadline(time.Now().Add(5 * time.Second))
		header := make([]byte, 19)
		_, err = io.ReadFull(connection, header)
		if err != nil || header[0] != 0 || header[17] != 0 || header[18] != byte(protocol.RequestCommandMux) {
			frames <- observed{err: io.ErrUnexpectedEOF}
			return
		}
		if _, err = connection.Write([]byte{0, 0}); err != nil {
			frames <- observed{err: err}
			return
		}
		var ids [][8]byte
		for len(ids) < 1 {
			var frame mux.FrameMetadata
			if err = frame.Unmarshal(connection, false); err != nil {
				frames <- observed{err: fmt.Errorf("mux metadata after %d UDP sources: %w", len(ids), err)}
				return
			}
			if frame.Option.Has(mux.OptionData) {
				var size uint16
				if err = binary.Read(connection, binary.BigEndian, &size); err != nil {
					frames <- observed{err: err}
					return
				}
				if _, err = io.CopyN(io.Discard, connection, int64(size)); err != nil {
					frames <- observed{err: err}
					return
				}
			}
			if frame.SessionStatus == mux.SessionStatusNew && frame.Target.Network == xnet.Network_UDP {
				ids = append(ids, frame.GlobalID)
			}
		}
		frames <- observed{ids: ids}
	}()
	c := configFixture(t)
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	number, _ := strconv.Atoi(port)
	c.Assignment.Address, c.Assignment.Port = host, uint16(number)
	c.Assignment.TLSName = certificate.DNSNames[0]
	c.Assignment.CertificatePEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw}))
	config, err := core.LoadConfig("json", bytes.NewReader(xrayDocument(c, false)))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(platform.UseCone, "false") // Default/non-restricted caller policy.
	instance, err := create(config)
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()
	if os.Getenv(platform.UseCone) != "false" {
		t.Fatal("client constructor leaked its fixed context policy into caller env")
	}
	if err = instance.Start(); err != nil {
		t.Fatal(err)
	}
	control, err := net.DialTimeout("tcp4", "127.0.0.1:1089", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	control.SetDeadline(time.Now().Add(3 * time.Second))
	control.Write([]byte{5, 1, 0})
	reply := make([]byte, 2)
	if _, err = io.ReadFull(control, reply); err != nil || !bytes.Equal(reply, []byte{5, 0}) {
		t.Fatal("test SOCKS authorization failed")
	}
	control.Write([]byte{5, 3, 0, 1, 127, 0, 0, 1, 0, 0})
	associate := make([]byte, 10)
	if _, err = io.ReadFull(control, associate); err != nil || associate[1] != 0 || associate[3] != 1 {
		t.Fatal("test SOCKS UDP association failed")
	}
	server := &net.UDPAddr{IP: net.IP(associate[4:8]), Port: int(binary.BigEndian.Uint16(associate[8:10]))}
	for i := 0; i < 1; i++ {
		source, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
		if err != nil {
			t.Fatal(err)
		}
		defer source.Close()
		// Use a real nonzero UDP source port, so the unrestricted core control
		// has an actual source identity to include in its Mux frame.
		packet := append([]byte{0, 0, 0, 1, 93, 184, 215, 100, 0, byte(53 + i)}, []byte(strings.Repeat("test-dns", 4))...)
		if _, err = source.WriteToUDP(packet, server); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case got := <-frames:
		if got.err != nil {
			t.Fatal(got.err)
		}
		if len(got.ids) != 1 {
			t.Fatal("real mux UDP frame was not observed")
		}
		return got.ids[0]
	case <-time.After(6 * time.Second):
		t.Fatal("real ordinary mux UDP frames did not reach the verified TLS endpoint")
	}
	return [8]byte{}
}
