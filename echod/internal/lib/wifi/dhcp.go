package wifi

import (
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// The DHCP client. A boot with no network starts none (techo5-net stops at "no network configured"),
// so a network joined later, by the setup page, the hotspot or Improv, got no IPv4 address: renewLease
// starts one when there is none running, with the arguments the boot uses.
var (
	dhcpPid     = "/run/udhcpc.pid"
	dhcpScripts = []string{"/etc/techo5/udhcpc.sh", "/run/udhcpc.sh"}
	// dhcpOwn is where the script goes when no boot left one (a Dot that started with no network).
	dhcpOwn  = "/run/udhcpc.sh"
	startDHC = func(args ...string) error {
		cmd := exec.Command("udhcpc", args...)
		if err := cmd.Start(); err != nil {
			return err
		}
		// With -b the first process exits once the client has gone to the background: reaped here,
		// so it is not left behind as a zombie.
		go func() { _ = cmd.Wait() }()
		return nil
	}
)

// dhcpScript is the Dot's techo5-net's: the address, the route and the name servers from the lease.
const dhcpScript = `#!/bin/sh
case "$1" in
deconfig)
	ifconfig "$interface" 0.0.0.0
	;;
bound | renew)
	ifconfig "$interface" "$ip" netmask "$subnet"
	if [ -n "$router" ]; then
		while route del default gw 0.0.0.0 dev "$interface" 2>/dev/null; do :; done
		route add default gw "${router%% *}" dev "$interface"
	fi
	[ -n "$dns" ] && { for s in $dns; do echo "nameserver $s"; done > /etc/resolv.conf; }
	;;
esac
`

// dhcpRunning is whether the client the pid file names is alive.
func dhcpRunning() bool {
	b, err := os.ReadFile(dhcpPid)
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return false
	}
	_, err = os.Stat(filepath.Join("/proc", strconv.Itoa(pid)))
	return err == nil
}

// startDHCP starts a DHCP client on the Wi-Fi interface when none is running; it reports whether it
// started one.
func startDHCP() bool {
	if dhcpRunning() {
		return false
	}
	script := ""
	for _, s := range dhcpScripts {
		if fi, err := os.Stat(s); err == nil && !fi.IsDir() {
			script = s
			break
		}
	}
	if script == "" {
		if err := os.WriteFile(dhcpOwn, []byte(dhcpScript), 0o755); err != nil {
			slog.Warn("wifi: writing a DHCP script failed", "err", err)
			return false
		}
		script = dhcpOwn
	}
	if err := startDHC("-i", iface(), "-b", "-R", "-t", "10", "-p", dhcpPid, "-s", script); err != nil {
		slog.Warn("wifi: starting the DHCP client failed", "err", err)
		return false
	}
	slog.Info("wifi: started a DHCP client: the boot had no network to start one for")
	return true
}
