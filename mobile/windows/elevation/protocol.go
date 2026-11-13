// Package elevation owns the bounded private protocol between the ordinary
// desktop process and its installed Windows administrator helper.
package elevation

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"sync"

	"github.com/syntr0x79/qntw-client/mobile/clienttinc"
)

const MaxPacket = 384 << 10

type Packet struct {
	Type       string             `json:"type"`
	Mode       string             `json:"mode,omitempty"`
	Nonce      string             `json:"nonce,omitempty"`
	Origin     string             `json:"origin,omitempty"`
	Token      string             `json:"token,omitempty"`
	Country    string             `json:"country,omitempty"`
	Job        json.RawMessage    `json:"job,omitempty"`
	ControlURL string             `json:"control_url,omitempty"`
	Status     *clienttinc.Status `json:"status,omitempty"`
	Code       string             `json:"code,omitempty"`
	// Handle is used only on the GUI broker pipe: a SYNCHRONIZE|QUERY handle to
	// the started helper, duplicated into the requesting desktop process.
	Handle uint64 `json:"handle,omitempty"`
}

func Read(r io.Reader) (Packet, error) {
	var p Packet
	var size [4]byte
	if _, err := io.ReadFull(r, size[:]); err != nil {
		return p, err
	}
	n := binary.BigEndian.Uint32(size[:])
	if n == 0 || n > MaxPacket {
		return p, errors.New("invalid private packet size")
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(r, data); err != nil {
		return p, err
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return Packet{}, errors.New("invalid private packet")
	}
	return p, nil
}

type Writer struct {
	mu sync.Mutex
	To io.Writer
}

func (w *Writer) Write(p Packet) error {
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if len(data) == 0 || len(data) > MaxPacket {
		return errors.New("private packet too large")
	}
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(data)))
	w.mu.Lock()
	defer w.mu.Unlock()
	if err = writeAll(w.To, size[:]); err != nil {
		return err
	}
	return writeAll(w.To, data)
}
func writeAll(w io.Writer, b []byte) error {
	for len(b) > 0 {
		n, e := w.Write(b)
		if e != nil {
			return e
		}
		if n <= 0 || n > len(b) {
			return io.ErrShortWrite
		}
		b = b[n:]
	}
	return nil
}

func CountryValid(s string) bool {
	return len(s) == 2 && s[0] >= 'A' && s[0] <= 'Z' && s[1] >= 'A' && s[1] <= 'Z'
}
func ValidateStart(p Packet, mode string) error {
	if p.Type != "start" || p.Mode != mode || p.Nonce != "" || p.Status != nil || p.Code != "" {
		return errors.New("invalid start packet")
	}
	switch mode {
	case "tinc":
		c, e := clienttinc.ValidateCredentials(clienttinc.Credentials{Origin: p.Origin, Token: p.Token})
		if e != nil || c.Origin != "https://qntw.link" || !CountryValid(p.Country) || len(p.Job) > 0 || p.ControlURL != "" {
			return errors.New("invalid device request")
		}
	case "vpn":
		if len(p.Job) == 0 || len(p.Job) > 256<<10 || p.Origin != "" || p.Token != "" || p.Country != "" || len(p.ControlURL) > 8192 {
			return errors.New("invalid VPN request")
		}
	default:
		return errors.New("invalid helper mode")
	}
	return nil
}
func ValidateControl(p Packet) error {
	if p.Mode != "" || p.Nonce != "" || p.Origin != "" || p.Token != "" || len(p.Job) > 0 || p.ControlURL != "" || p.Status != nil || p.Code != "" {
		return errors.New("invalid control packet")
	}
	if p.Type == "stop" && p.Country == "" {
		return nil
	}
	if p.Type == "country" && CountryValid(p.Country) {
		return nil
	}
	return errors.New("invalid control packet")
}
