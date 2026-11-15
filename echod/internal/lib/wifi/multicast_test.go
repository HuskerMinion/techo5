package wifi

import "testing"

// Broadcasts still arriving do not hide multicast that stopped: the multicast counters leave them out
// (TECHO5 #458), while In and Out keep both as before.
func TestBroadcastsAreNotCountedAsMulticast(t *testing.T) {
	g, ok := group4("IpExt: InNoRoutes InMcastPkts OutMcastPkts InBcastPkts OutBcastPkts\nIpExt: 0 10 4 500 2\n")
	if !ok || g.McastIn != 10 || g.McastOut != 4 || g.In != 510 || g.Out != 6 {
		t.Fatalf("group4 = %+v, %v", g, ok)
	}
	// The broadcasts alone, for a radio that loses them while multicast still comes (techo5-dot#45).
	if g.BcastIn != 500 || g.BcastOut != 2 {
		t.Fatalf("group4 broadcasts = %d in, %d out; want 500 and 2", g.BcastIn, g.BcastOut)
	}
	g, ok = group6("Ip6InMcastPkts 7\nIp6OutMcastPkts 3\nIcmp6InRouterAdvertisements 2\n")
	if !ok || g.McastIn != 7 || g.McastOut != 3 || g.Router != 2 || g.BcastIn != 0 {
		t.Fatalf("group6 = %+v, %v", g, ok)
	}
}
