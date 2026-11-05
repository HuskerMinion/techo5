package wifi

import "testing"

// The addresses are from 192.0.2.0/24, the range set aside for documentation.
func TestTheGatewayIsReadFromTheRouteTable(t *testing.T) {
	// The machine running the tests may have Wi-Fi of its own under another name.
	defer func(was func() string) { iface = was }(iface)
	iface = func() string { return "wlan0" }
	table := `Iface	Destination	Gateway 	Flags	RefCnt	Use	Metric	Mask		MTU	Window	IRTT
wlan0	000200C0	00000000	0001	0	0	0	00FFFFFF	0	0	0
wlan0	00000000	010200C0	0003	0	0	0	00000000	0	0	0
`
	if got := gatewayIn(table); got.String() != "192.0.2.1" {
		t.Errorf("gateway %v, want 192.0.2.1", got)
	}
	if got := gatewayIn("Iface\tDestination\tGateway\nwlan0\t000200C0\t00000000\n"); got != nil {
		t.Errorf("a table with no default route gave %v", got)
	}
	if got := gatewayIn("Iface\tDestination\tGateway\neth0\t00000000\t010200C0\n"); got != nil {
		t.Errorf("another interface's default route gave %v", got)
	}
}

// Trimmed from an Echo Dot on TECHO5 Dot v0.5.54, but for the router lines, which are made up.
func TestGroupPacketsAreReadFromTheIPCounters(t *testing.T) {
	netstat := `TcpExt: SyncookiesSent SyncookiesRecv
TcpExt: 0 0
IpExt: InNoRoutes InTruncatedPkts InMcastPkts OutMcastPkts InBcastPkts OutBcastPkts InOctets
IpExt: 0 0 12414 1313 4763 0 184480919
`
	if g, ok := group4(netstat); !ok || g.In != 12414+4763 || g.Out != 1313 || g.Router != 0 {
		t.Errorf("IPv4 gave %+v ok %v, want in %d out 1313", g, ok, 12414+4763)
	}
	snmp6 := "Ip6InReceives                   \t40213\nIp6InMcastPkts                  \t5694\nIp6OutMcastPkts                 \t1341\n" +
		"Icmp6InRouterAdvertisements     \t212\nIcmp6InGroupMembQueries         \t97\nIcmp6OutMLDv2Reports            \t130\n"
	if g, ok := group6(snmp6); !ok || g.In != 5694 || g.Out != 1341 || g.Router != 212+97 {
		t.Errorf("IPv6 gave %+v ok %v, want in 5694 out 1341 router %d", g, ok, 212+97)
	}
	if _, ok := group4("TcpExt: A\nTcpExt: 1\n"); ok {
		t.Error("a netstat without IpExt read as counters")
	}
	if _, ok := group6(""); ok {
		t.Error("an empty snmp6 read as counters")
	}
}
