package streaming

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"
)

// The firewall makes AirPlay 2's rules at boot, in a chain of their own (TECHO5-AP2: its session on 7000,
// PTP's 319 and 320, and every port above 1023 from a sender that reached 7000 in the last ten minutes),
// but leaves the chain out of the way: a sender would otherwise be let in to all of those ports whether
// AirPlay is on or not. The daemon puts the chain in the way only while the AirPlay 2 receiver runs, and
// takes it out again when it stops. Where the jump goes is the firewall's to say (airplay2Jump): the
// Show's Wi-Fi chain, the Dot's INPUT.

// airplay2Jump is where techo5-firewall says the chain's jump goes: a chain, a position, and the match
// before "-j TECHO5-AP2", as "TECHO5-IN 1" or "INPUT 2 -i wlan0". Not there: no chain to put in the way.
// A variable, for tests.
var airplay2Jump = "/run/techo5-airplay2-jump"

const airplay2Chain = "TECHO5-AP2"

// iptables runs one iptables command; a variable, for tests. -w waits for the tables' lock, as the video
// fence's does (feature/video, fence.go): the add-ons' chain and the firewall's script change the tables
// too, and a check that could not run would be taken for a door that is shut.
var iptables = func(tool string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, tool, append([]string{"-w"}, args...)...).Run()
}

// airplay2Door puts AirPlay 2's chain in the way (open) or takes it out, for IPv4 and IPv6 alike.
func airplay2Door(open bool) {
	b, err := os.ReadFile(airplay2Jump)
	if err != nil {
		return
	}
	f := strings.Fields(strings.TrimSpace(string(b)))
	if len(f) < 2 {
		return
	}
	chain, pos, match := f[0], f[1], f[2:]
	rule := append(append([]string{chain}, match...), "-j", airplay2Chain)
	for _, tool := range []string{"iptables-legacy", "ip6tables-legacy"} {
		if open {
			if iptables(tool, append([]string{"-C"}, rule...)...) == nil {
				continue
			}
			ins := append(append([]string{"-I", chain, pos}, match...), "-j", airplay2Chain)
			if err := iptables(tool, ins...); err != nil {
				slog.Warn("streaming: letting AirPlay 2's senders in failed", "tool", tool, "err", err)
			}
			continue
		}
		// Every copy goes, should one have been put in twice.
		for range 4 {
			if iptables(tool, append([]string{"-D"}, rule...)...) != nil {
				break
			}
		}
	}
}
