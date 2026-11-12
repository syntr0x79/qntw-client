//go:build darwin && !ios

package clienttinc

import "testing"

func TestDarwinCountersHandleTheBlankPointToPointAddress(t *testing.T) {
	output := `Name Mtu Network Address Ipkts Ierrs Ibytes Opkts Oerrs Obytes Coll
utun7 1280 <Link#35> 32 0 1808 190 0 16800 0
utun7 1280 198.18.94.1/32 198.18.94.1 32 - 1808 190 - 16800 -
`
	rx, tx, e := parseDarwinCounters(output, "utun7")
	if e != nil || rx != 1808 || tx != 16800 {
		t.Fatalf("packet/collision columns used as bytes: rx=%d tx=%d err=%v", rx, tx, e)
	}
	if _, _, e = parseDarwinCounters(output, "utun8"); e == nil {
		t.Fatal("foreign interface accepted")
	}
	if _, _, e = parseDarwinCounters("Name Mtu Network Address Ipkts Ierrs Ibytes Opkts Oerrs Obytes Coll\nutun7 1280 <Link#35> broken\n", "utun7"); e == nil {
		t.Fatal("broken counters must stay unknown")
	}
}
