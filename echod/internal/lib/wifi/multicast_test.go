package wifi

import "testing"

// Broadcasts still arriving do not hide multicast that stopped: the multicast counters leave them out
// (TECHO5 #458), while In and Out keep both as before.
func TestBroadcastsAreNotCountedAsMulticast(t *testing.T) {
	g, ok := group4("IpExt: InNoRoutes InMcastPkts OutMcastPkts InBcastPkts OutBcastPkts\nIpExt: 0 10 4 500 2\n")
	if !ok || g.McastIn != 10 || g.McastOut != 4 || g.In != 510 || g.Out != 6 {
		t.Fatalf("group4 = %+v, %v", g, ok)
	}
	g, ok = group6("Ip6InMcastPkts 7\nIp6OutMcastPkts 3\nIcmp6InRouterAdvertisements 2\n")
	if !ok || g.McastIn != 7 || g.McastOut != 3 || g.Router != 2 {
		t.Fatalf("group6 = %+v, %v", g, ok)
	}
}
