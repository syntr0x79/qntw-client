package agentproto

import (
	"net/netip"
	"strings"
	"testing"
)

const (
	tSelf = "n0123456789ab"
	tKey1 = "/YsLMYpYiJIl5dU9irpkQebvuP+0YvrAW8bOiYwsmyH"
	tKey2 = "XRgcUuA74GEho1v5gNnOTrFrgx4ZJNj9jRyQ0ulZbLE"
)

var tOwn = []string{"10.44.0.15/32", "10.45.0.15/32"}

var tAllowed = []netip.Prefix{netip.MustParsePrefix("10.44.0.0/24"), netip.MustParsePrefix("10.45.0.0/24")}

func validPeers() TincPeers {
	return TincPeers{Self: tSelf, Peers: []TincPeer{
		{Name: "n00000000000a", Pubkey: tKey1, Address: "203.0.113.5", Port: 7443, Subnets: []string{"10.44.0.5/32", "10.45.0.5/32"}},
		{Name: "n00000000000b", Pubkey: tKey2, Address: "edge.example.com", Port: 7444, Subnets: []string{"10.44.0.6/32"}},
	}}
}

func TestTincPeersValid(t *testing.T) {
	if err := validPeers().Validate(tSelf, tOwn, tAllowed); err != nil {
		t.Fatal(err)
	}
	if err := (TincPeers{Self: tSelf}).Validate(tSelf, tOwn, tAllowed); err != nil {
		t.Fatalf("no peers: %v", err)
	}
}

func TestTincPeersRefused(t *testing.T) {
	for name, c := range map[string]struct {
		mut  func(*TincPeers)
		want string
	}{
		"self mismatch":      {func(p *TincPeers) { p.Self = "n00000000000c" }, "файл соседей"},
		"name -up script":    {func(p *TincPeers) { p.Peers[0].Name = "n00000000000a-up" }, "имя"},
		"name tinc-up":       {func(p *TincPeers) { p.Peers[0].Name = "tinc-up" }, "имя"},
		"name traversal":     {func(p *TincPeers) { p.Peers[0].Name = "../tinc-up" }, "имя"},
		"name slash":         {func(p *TincPeers) { p.Peers[0].Name = "n00000000000a/x" }, "имя"},
		"name newline":       {func(p *TincPeers) { p.Peers[0].Name = "n00000000000a\n" }, "имя"},
		"name upper":         {func(p *TincPeers) { p.Peers[0].Name = "N00000000000A" }, "имя"},
		"name empty":         {func(p *TincPeers) { p.Peers[0].Name = "" }, "имя"},
		"name is self":       {func(p *TincPeers) { p.Peers[0].Name = tSelf }, "сам узел"},
		"name repeated":      {func(p *TincPeers) { p.Peers[1].Name = p.Peers[0].Name }, "повторяется"},
		"key directive":      {func(p *TincPeers) { p.Peers[0].Pubkey = tKey1 + "\nScriptsInterpreter = /bin/sh" }, "ключ"},
		"key spaced":         {func(p *TincPeers) { p.Peers[0].Pubkey = "Ed25519PublicKey = " + tKey1 }, "ключ"},
		"key wireguard":      {func(p *TincPeers) { p.Peers[0].Pubkey = "AioIiUHPqhMCrLu3S8gWCzHuSMu8EhzkyR9v46Fi1nQ=" }, "ключ"},
		"key repeated":       {func(p *TincPeers) { p.Peers[1].Pubkey = tKey1 }, "ключ повторяется"},
		"address directive":  {func(p *TincPeers) { p.Peers[0].Address = "203.0.113.5\nConnectTo = x" }, "адрес"},
		"address with port":  {func(p *TincPeers) { p.Peers[0].Address = "203.0.113.5 655" }, "адрес"},
		"address ipv6":       {func(p *TincPeers) { p.Peers[0].Address = "2001:db8::1" }, "адрес"},
		"address empty":      {func(p *TincPeers) { p.Peers[0].Address = "" }, "адрес"},
		"address underscore": {func(p *TincPeers) { p.Peers[0].Address = "a_b.example.com" }, "адрес"},
		"address bad ipv4":   {func(p *TincPeers) { p.Peers[0].Address = "300.1.1.1" }, "адрес"},
		"address no dot":     {func(p *TincPeers) { p.Peers[0].Address = "localhost" }, "адрес"},
		"port zero":          {func(p *TincPeers) { p.Peers[0].Port = 0 }, "порт"},
		"port big":           {func(p *TincPeers) { p.Peers[0].Port = 65536 }, "порт"},
		"subnet /24":         {func(p *TincPeers) { p.Peers[0].Subnets = []string{"10.44.0.0/24"} }, "подсеть"},
		"subnet outside":     {func(p *TincPeers) { p.Peers[0].Subnets = []string{"10.46.0.5/32"} }, "подсеть"},
		"subnet default":     {func(p *TincPeers) { p.Peers[0].Subnets = []string{"0.0.0.0/0"} }, "подсеть"},
		"subnet newline":     {func(p *TincPeers) { p.Peers[0].Subnets = []string{"10.44.0.5/32\nScriptsInterpreter = /bin/sh"} }, "подсеть"},
		"subnet no mask":     {func(p *TincPeers) { p.Peers[0].Subnets = []string{"10.44.0.5"} }, "подсеть"},
		"subnet mac":         {func(p *TincPeers) { p.Peers[0].Subnets = []string{"00:11:22:33:44:55"} }, "подсеть"},
		"subnet repeated":    {func(p *TincPeers) { p.Peers[0].Subnets = []string{"10.44.0.5/32", "10.44.0.5/32"} }, "повторяется"},
		"subnet of peer":     {func(p *TincPeers) { p.Peers[1].Subnets = []string{"10.44.0.5/32"} }, "уже у n00000000000a"},
		"subnet of self":     {func(p *TincPeers) { p.Peers[1].Subnets = []string{"10.45.0.15/32"} }, "уже у n0123456789ab"},
		"subnets none":       {func(p *TincPeers) { p.Peers[0].Subnets = nil }, "нет подсетей"},
		"too many":           {func(p *TincPeers) { p.Peers = make([]TincPeer, MaxTincPeers+1) }, "больше"},
	} {
		p := validPeers()
		c.mut(&p)
		err := p.Validate(tSelf, tOwn, tAllowed)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
	if err := validPeers().Validate("../x", tOwn, tAllowed); err == nil {
		t.Error("bad own name accepted")
	}
}

func TestTincFiles(t *testing.T) {
	if got, want := TincConf(tSelf, "tmesh0", 7443, []string{"n00000000000b", "n00000000000a"}),
		"Name = n0123456789ab\nInterface = tmesh0\nMode = router\nPort = 7443\nExperimentalProtocol = yes\nStrictSubnets = yes\nAutoConnect = no\nBroadcast = no\n"+
			"ConnectTo = n00000000000a\nConnectTo = n00000000000b\n"; got != want {
		t.Errorf("tinc.conf:\n%s", got)
	}
	if got, want := TincHostFile("203.0.113.5", 7443, []string{"10.44.0.5/32", "10.45.0.5/32"}, tKey1),
		"Address = 203.0.113.5\nPort = 7443\nSubnet = 10.44.0.5/32\nSubnet = 10.45.0.5/32\nEd25519PublicKey = "+tKey1+"\n"; got != want {
		t.Errorf("host file:\n%s", got)
	}
	if got := TincHostFile("203.0.113.5", 7443, []string{"10.44.0.5/32"}, ""); strings.Contains(got, "Ed25519") {
		t.Errorf("own host file carries a key:\n%s", got)
	}
}

func TestParseTincConnectTo(t *testing.T) {
	conf := TincConf(tSelf, "tmesh0", 7443, []string{"n00000000000b", "n00000000000a"}) + "ConnectTo = ../x\n"
	if got := strings.Join(ParseTincConnectTo(conf), " "); got != "n00000000000a n00000000000b" {
		t.Errorf("got %q", got)
	}
}
