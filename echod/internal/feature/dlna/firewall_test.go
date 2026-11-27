package dlna

import (
	"os"
	"strings"
	"testing"
)

// The image's firewall on Wi-Fi lets in what SSDP needs before its last rule drops the rest: the
// searches (UDP 1900), and IGMP, the queries a switch sends to learn which groups the device is in.
// Dropped, they went unanswered, the switch forgot the device's membership after a few minutes, and
// searches stopped reaching it (#779).
func TestTheFirewallLetsSSDPAndIGMPIn(t *testing.T) {
	b, err := os.ReadFile("../../../../tools/linux/rootfs/usr/local/sbin/techo5-firewall")
	if err != nil {
		t.Fatal(err)
	}
	script := string(b)
	drop := strings.Index(script, "iptables-legacy -A $CHAIN -j DROP")
	if drop < 0 {
		t.Fatal("no DROP at the end of the Wi-Fi chain")
	}
	for _, rule := range []string{"iptables-legacy -A $CHAIN -p udp --dport 1900 -j ACCEPT", "iptables-legacy -A $CHAIN -p igmp -j ACCEPT"} {
		if at := strings.Index(script, rule); at < 0 || at > drop {
			t.Errorf("%q is not in the Wi-Fi chain before its DROP", rule)
		}
	}
}
