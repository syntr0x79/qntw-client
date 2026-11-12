// Package topology contains client protocol values and share-link parsers only.
// Server persistence, catalogue and node validation stay in qntw-backend.
package topology

import "errors"

var ErrUnsupportedTransport = errors.New("unsupported transport")

const SecNone = "none"

var xhttpOuterKeys = []string{"host", "path", "mode"}

type Inbound struct {
	Tag       string
	Protocol  string
	Host      string
	Port      int
	Transport Transport
	Enabled   bool
}
type Transport struct {
	Kind   string         `json:"kind"`
	Sec    string         `json:"sec"`
	Params map[string]any `json:"params"`
}
