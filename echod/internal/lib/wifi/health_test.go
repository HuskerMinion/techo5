package wifi

import "testing"

// The addresses are from 192.0.2.0/24, the range set aside for documentation.
func TestTheGatewayIsReadFromTheRouteTable(t *testing.T) {
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

// Trimmed from an Echo Dot on TECHO5 Dot v0.5.54.
func TestGroupPacketsAreReadFromTheIPCounters(t *testing.T) {
	netstat := `TcpExt: SyncookiesSent SyncookiesRecv
TcpExt: 0 0
IpExt: InNoRoutes InTruncatedPkts InMcastPkts OutMcastPkts InBcastPkts OutBcastPkts InOctets
IpExt: 0 0 12414 1313 4763 0 184480919
`
	if in, out, ok := groupIn4(netstat); !ok || in != 12414+4763 || out != 1313 {
		t.Errorf("IPv4 gave in %d out %d ok %v, want in %d out 1313", in, out, ok, 12414+4763)
	}
	snmp6 := "Ip6InReceives                   \t40213\nIp6InMcastPkts                  \t5694\nIp6OutMcastPkts                 \t1341\n"
	if in, out, ok := groupIn6(snmp6); !ok || in != 5694 || out != 1341 {
		t.Errorf("IPv6 gave in %d out %d ok %v, want in 5694 out 1341", in, out, ok)
	}
	if _, _, ok := groupIn4("TcpExt: A\nTcpExt: 1\n"); ok {
		t.Error("a netstat without IpExt read as counters")
	}
	if _, _, ok := groupIn6(""); ok {
		t.Error("an empty snmp6 read as counters")
	}
}
