package elevation

import (
	"bytes"
	"encoding/binary"
	"io"
	"strings"
	"sync"
	"testing"
)

func TestFrameRejectsUnboundedInputBeforeBody(t *testing.T) {
	for _, size := range []uint32{0, MaxPacket + 1, ^uint32(0)} {
		var head [4]byte
		binary.BigEndian.PutUint32(head[:], size)
		reader := bytes.NewReader(head[:])
		if _, e := Read(reader); e == nil {
			t.Fatal("accepted invalid length", size)
		}
	}
	for _, b := range [][]byte{{0, 0}, {0, 0, 0, 3, '{', '}'}, {0, 0, 0, 1, 'x'}} {
		if _, e := Read(bytes.NewReader(b)); e == nil {
			t.Fatal("accepted truncated or invalid frame")
		}
	}
}

type shortWriter struct{ bytes.Buffer }

func (w *shortWriter) Write(b []byte) (int, error) {
	if len(b) > 3 {
		b = b[:3]
	}
	return w.Buffer.Write(b)
}
func TestConcurrentFrameWritesPreserveBoundaries(t *testing.T) {
	output := &shortWriter{}
	writer := &Writer{To: output}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e := writer.Write(Packet{Type: "country", Country: "NL"}); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	count := 0
	for {
		p, e := Read(&output.Buffer)
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		if p.Type != "country" || p.Country != "NL" {
			t.Fatal("interleaved frame")
		}
		count++
	}
	if count != 20 {
		t.Fatal(count)
	}
}
func TestPrivilegedStartRejectsForeignOrSecretReplayFields(t *testing.T) {
	valid := Packet{Type: "start", Mode: "tinc", Origin: "https://qntw.link", Token: strings.Repeat("a", 32), Country: "NL"}
	if e := ValidateStart(valid, "tinc"); e != nil {
		t.Fatal(e)
	}
	for name, mutate := range map[string]func(*Packet){
		"origin":  func(p *Packet) { p.Origin = "https://attacker.example" },
		"http":    func(p *Packet) { p.Origin = "http://qntw.link" },
		"country": func(p *Packet) { p.Country = "../root" },
		"job":     func(p *Packet) { p.Job = []byte(`{}`) },
		"mode":    func(p *Packet) { p.Mode = "vpn" },
		"nonce":   func(p *Packet) { p.Nonce = "replayed" },
		"status":  func(p *Packet) { p.Code = "accepted" },
	} {
		t.Run(name, func(t *testing.T) {
			p := valid
			mutate(&p)
			if ValidateStart(p, "tinc") == nil {
				t.Fatal("unsafe start accepted")
			}
		})
	}
	for _, p := range []Packet{{Type: "start", Mode: "vpn", Job: []byte(`{}`), Token: "foreign"}, {Type: "start", Mode: "vpn", Job: make([]byte, 256<<10+1)}, {Type: "start", Mode: "other", Job: []byte(`{}`)}} {
		if ValidateStart(p, p.Mode) == nil {
			t.Fatal("unsafe VPN packet accepted")
		}
	}
}
func TestControlCannotReplaceAuthorizationOrInjectPaths(t *testing.T) {
	for _, p := range []Packet{{Type: "stop"}, {Type: "country", Country: "DE"}} {
		if e := ValidateControl(p); e != nil {
			t.Fatal(e)
		}
	}
	for _, p := range []Packet{{Type: "country", Country: "../DE"}, {Type: "stop", Country: "NL"}, {Type: "start"}, {Type: "country", Country: "NL", Token: "replacement"}, {Type: "stop", ControlURL: "file:///private"}, {Type: "stop", Origin: "https://qntw.link"}} {
		if ValidateControl(p) == nil {
			t.Fatal("unsafe command accepted")
		}
	}
}
