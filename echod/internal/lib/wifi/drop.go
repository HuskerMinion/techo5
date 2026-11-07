package wifi

import (
	"context"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

// Drop takes the device off the network it is on: the supplicant rereads a configuration that is
// meant to hold no network (so it leaves the one it was on and joins none), and the interface's
// addresses go, so everything asking whether the device is online (Current's Address, Improv's
// offer at boot) hears no. The DHCP client is left running, since a network joined later gets its lease
// from it (renewLease); with nothing to associate with, it has nothing to renew over. For a reset for
// someone else (feature/firstrun), after the networks are forgotten.
func Drop(ctx context.Context) {
	if _, err := cli(ctx, "reconfigure"); err != nil {
		slog.Warn("wifi: rereading the configuration failed", "err", err)
	}
	if _, err := cli(ctx, "disconnect"); err != nil {
		slog.Warn("wifi: disconnecting failed", "err", err)
	}
	for _, args := range [][]string{
		{"-4", "addr", "flush", "dev", iface()},
		{"-6", "addr", "flush", "dev", iface(), "scope", "global"},
	} {
		c, cancel := context.WithTimeout(ctx, 5*time.Second)
		out, err := ipCommand(c, args...)
		cancel()
		if err != nil {
			slog.Warn("wifi: dropping an address failed", "args", strings.Join(args, " "), "err", err, "out", out)
		}
	}
	slog.Info("wifi: off the network, with no address")
}

// ipCommand runs ip (a variable for the tests).
var ipCommand = func(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "ip", args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
