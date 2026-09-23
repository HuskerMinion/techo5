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
