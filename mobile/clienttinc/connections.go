package clienttinc

import (
	"strconv"
	"strings"

	wire "github.com/syntr0x79/qntw-client/internal/clienttincwire"
)

// The pinned tinc dump includes pending TCP attempts, not only authenticated
// connections. A direct self->peer graph edge is created after ACK activation.
// Combine that evidence with a valid non-connecting socket and deduplicate.
func connectedIngresses(connections, edges string, config wire.ClientConfig) []string {
	known, authenticated, active := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, peer := range config.Ingresses {
		known[peer.Name] = true
	}
	for _, line := range strings.Split(edges, "\n") {
		f := strings.Fields(line)
		if len(f) >= 3 && f[0] == config.Self.Name && f[1] == "to" && known[f[2]] {
			authenticated[f[2]] = true
		}
	}
	for _, line := range strings.Split(connections, "\n") {
		f := strings.Fields(line)
		if len(f) != 11 || !known[f[0]] || !authenticated[f[0]] || f[1] != "at" || f[3] != "port" || f[5] != "options" || f[7] != "socket" || f[9] != "status" {
			continue
		}
		fd, fdErr := strconv.Atoi(f[8])
		flags, flagErr := strconv.ParseUint(f[10], 16, 32)
		if fdErr == nil && fd >= 0 && flagErr == nil && flags&0x4 == 0 {
			active[f[0]] = true
		}
	}
	var result []string
	for _, peer := range config.Ingresses {
		if active[peer.Name] {
			result = append(result, peer.Name)
			delete(active, peer.Name)
		}
	}
	return result
}
