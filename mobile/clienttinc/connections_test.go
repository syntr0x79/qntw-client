package clienttinc

import (
	"reflect"
	"testing"

	wire "github.com/syntr0x79/qntw-client/internal/clienttincwire"
)

func TestLiveIngressesExcludePendingAndUnauthenticatedConnections(t *testing.T) {
	c := wire.ClientConfig{Self: wire.ClientIdentity{Name: "device1"}, Ingresses: []wire.ClientIngress{{Name: "i1"}, {Name: "i2"}, {Name: "i3"}}}
	connections := `i1 at 192.0.2.3 port 17445 options 0 socket 12 status 100
i1 at 192.0.2.3 port 17445 options 0 socket 13 status 100
i2 at 192.0.2.4 port 17445 options 0 socket 14 status 0
i3 at 192.0.2.5 port 17445 options 0 socket 15 status 4
i3 at 192.0.2.5 port 17445 options 0 socket -1 status 100
i3 at 192.0.2.5 port 17445 options 0 socket invalid status 100
i3 at 192.0.2.5 port 17445 options 0 socket 15 status invalid
`
	edges := "device1 to i1 at 192.0.2.3 port 17445\nother to i2 at 192.0.2.4 port 17445\ndevice1 to i3 at 192.0.2.5 port 17445\n"
	if got := connectedIngresses(connections, edges, c); !reflect.DeepEqual(got, []string{"i1"}) {
		t.Fatalf("pending/unauthenticated/duplicate peer counted: %v", got)
	}
	// A non-MST authenticated link is still a real metadata connection.
	edges += "device1 to i2 at 192.0.2.4 port 17445\n"
	if got := connectedIngresses(connections, edges, c); !reflect.DeepEqual(got, []string{"i1", "i2"}) {
		t.Fatalf("authenticated non-MST peer not counted: %v", got)
	}
	if got := connectedIngresses(connections, "", c); len(got) != 0 {
		t.Fatal("an incomplete handshake was reported connected")
	}
}
