// Package wifi is the device's Wi-Fi as wpa_supplicant runs it on the Linux image: what it is
// joined to, what it can hear, and joining something else. It talks to the supplicant through
// wpa_cli on the control socket the boot scripts open, and writes the same configuration file
// they start it from, so a network chosen on the screen is the one the next boot joins.
package wifi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// iface is the Wi-Fi interface: wlan0 on every Echo, mlan0 on the Surface RT.
var iface = layout.WifiInterface

const (
	ctrlDir = "/run/wpa"

	// SetupFlag, while present, tells the boot scripts' network keeper that someone is setting the
	// network up, so it does not reboot the device for being offline.
	SetupFlag = "/run/techo5/wifi-setup"
)

// Conf is the supplicant's configuration on the data partition (boot.sh writes it there). A variable
// rather than a constant so that a test can write one somewhere harmless.
var Conf = "/data/techo5-linux/wpa_supplicant.conf"

// Network is one the radio can hear.
type Network struct {
	SSID    string
	Signal  int  // dBm
	Secured bool // wants a passphrase
}

// Status is the connection now.
type Status struct {
	SSID      string
	Connected bool // associated and authenticated
	Address   string
	State     string // the supplicant's word for it
}

// Available reports whether this device has a supplicant to talk to.
func Available() bool {
	_, err := os.Stat(ctrlDir)
	return err == nil
}

func cli(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "wpa_cli", append([]string{"-p", ctrlDir, "-i", iface()}, args...)...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("wpa_cli %s: %w: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	// wpa_cli exits 0 whether the supplicant did the thing or refused it: a refusal is the word FAIL
	// on a line of its own in the reply. Without reading that, a select_network the supplicant turned
	// down looks like a success here and the join is left to time out, which the person at the screen
	// is told was a network that would not take their passphrase.
	if bad := refusal(string(out)); bad != "" {
		return "", fmt.Errorf("wpa_cli %s: %s", args[0], bad)
	}
	return string(out), nil
}

// refusal is the supplicant's FAIL reply in a wpa_cli reply, empty when there is none. A line of its
// own, so that a network named FAIL in a table of results is not mistaken for one.
func refusal(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "FAIL" || strings.HasPrefix(line, "FAIL-") {
			return line
		}
	}
	return ""
}

// Current is the connection state.
func Current(ctx context.Context) Status {
	var st Status
	out, err := cli(ctx, "status")
	if err != nil {
		st.State = "no supplicant"
		return st
	}
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch k {
		case "ssid":
			st.SSID = decodeName(v)
		case "wpa_state":
			st.State = strings.ToLower(v)
			st.Connected = v == "COMPLETED"
		}
	}
	st.Address = address()
	return st
}

// address is the interface's IPv4 address, empty without one.
func address() string {
	i, err := net.InterfaceByName(iface())
	if err != nil {
		return ""
	}
	addrs, _ := i.Addrs()
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
			return ipn.IP.String()
		}
	}
	return ""
}

// Scan asks for a scan and returns what was heard, strongest first, one entry per name.
func Scan(ctx context.Context) ([]Network, error) {
	if _, err := cli(ctx, "scan"); err != nil && !strings.Contains(err.Error(), "FAIL-BUSY") {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(4 * time.Second):
	}
	out, err := cli(ctx, "scan_results")
	if err != nil {
		return nil, err
	}
	best := map[string]Network{}
	for i, line := range strings.Split(out, "\n") {
		if i == 0 {
			continue // the header
		}
		f := strings.Split(line, "\t")
		if len(f) < 5 || strings.TrimSpace(f[4]) == "" {
			continue
		}
		sig, _ := strconv.Atoi(f[2])
		n := Network{SSID: f[4], Signal: sig, Secured: strings.Contains(f[3], "WPA") || strings.Contains(f[3], "WEP")}
		if cur, ok := best[n.SSID]; !ok || n.Signal > cur.Signal {
			best[n.SSID] = n
		}
	}
	nets := make([]Network, 0, len(best))
	for _, n := range best {
		nets = append(nets, n)
	}
	sort.Slice(nets, func(i, j int) bool { return nets[i].Signal > nets[j].Signal })
	return nets, nil
}

// Join writes the configuration for one network, makes the supplicant reread it, and waits for
// the association and an address. The previous configuration is kept as Conf.prev and put back
// if the new network never completes, so a typo does not leave the device off the air.
func Join(ctx context.Context, ssid, passphrase string) error {
	if ssid == "" {
		return errors.New("wifi: no network named")
	}
	// A name is at most 32 bytes on the air, and it is also the salt the key below is derived with:
	// a longer one is not a network anything could join, so it is refused here rather than written.
	if len(ssid) > 32 {
		return errors.New("wifi: a network name is at most 32 characters")
	}
	if passphrase != "" && (len(passphrase) < 8 || len(passphrase) > 63) {
		return errors.New("wifi: a passphrase is 8 to 63 characters")
	}
	if !oneLine(ssid) {
		return errors.New("wifi: a network name cannot hold a line ending")
	}
	if !oneLine(passphrase) {
		return errors.New("wifi: a passphrase cannot hold a line ending")
	}
	old, _ := os.ReadFile(Conf)
	// The new one goes first, since it is the one just asked for, and any older entry for the same
	// name goes, so a corrected passphrase replaces the one that was wrong. The rest are kept: a
	// device set up on one network still joins the network it is taken to.
	kept := []string{block(ssid, passphrase)}
	for _, b := range blocks(string(old)) {
		if ssidOf(b) != ssid {
			kept = append(kept, b)
		}
	}
	if err := writeFile(Conf, conf(kept)); err != nil {
		return err
	}
	if len(old) > 0 {
		_ = writeFile(Conf+".prev", string(old))
	}
	if _, err := cli(ctx, "reconfigure"); err != nil {
		return err
	}
	// Say which one, rather than leave it to be chosen. The supplicant picks among the networks it has
	// by signal and priority and not by the order they are written in, so on a router that carries
	// several names - which is most of them now - it will simply go back to the one already joined,
	// and the wait below would time out on a network nobody ever tried to reach. select_network puts
	// the others aside until this one has been attempted.
	if id, err := networkID(ctx, ssid); err != nil {
		slog.Warn("wifi: could not single out the network asked for; leaving the choice to the supplicant",
			"ssid", ssid, "err", err)
	} else if _, err := cli(ctx, "select_network", id); err != nil {
		return err
	}
	// A minute: on a Show 5 with no network before, the first tries on a 5 GHz network have been seen to
	// fail at the 40 seconds this was, with the right passphrase, before a third joined. The web
	// setup page gives the whole join 70 (setup.joinWifi).
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		st := Current(ctx)
		if st.Connected && st.SSID == ssid {
			// Everything else comes back now that this one has been joined: the way home is only kept
			// by being usable, and a device carried back to it should find it without being told again.
			if _, err := cli(ctx, "enable_network", "all"); err != nil {
				slog.Warn("wifi: the other saved networks are disabled until the next restart", "err", err)
			}
			renewLease()
			for i := 0; i < 20 && address() == ""; i++ {
				time.Sleep(time.Second)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	// Back to what worked. The reconfigure re-reads the file, which undoes the select above with it.
	if len(old) > 0 {
		_ = writeFile(Conf, string(old))
	}
	_, _ = cli(ctx, "reconfigure")
	return fmt.Errorf("wifi: could not join %q (%s)", ssid, Current(ctx).State)
}

// networkID is the supplicant's own number for a saved network, which is what select_network takes.
// list_networks is a tab separated table: id, ssid, bssid, flags.
func networkID(ctx context.Context, ssid string) (string, error) {
	out, err := cli(ctx, "list_networks")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\t")
		if len(f) >= 2 && decodeName(f[1]) == ssid {
			return f[0], nil
		}
	}
	return "", fmt.Errorf("wifi: %q is not among the saved networks", ssid)
}

// RenewLease gets an address on the network joined now, as a join does (#384).
func RenewLease() { renewLease() }

// renewLease gets the address on the new network: through techo5-net when the setting is a fixed
// address (address.go), which decides whether it holds there; otherwise by poking udhcpc for a lease,
// as always. Never through a techo5-net that is not the one that knows fixed addresses (AddressSupported).
func renewLease() {
	if AddressSupported() && LoadAddress().Fixed {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if out, err := exec.CommandContext(ctx, netTool, "renew").CombinedOutput(); err != nil {
			slog.Warn("wifi: techo5-net renew failed", "err", err, "out", strings.TrimSpace(string(out)))
		}
		return
	}
	if proc := dhcpClient(); proc != nil {
		_ = proc.Signal(sigRenew)
		return
	}
	if joinStartsDHCP {
		go startDHCP()
	}
}

// dhcpClient is the udhcpc running for wlan0, or nil when there is none.
func dhcpClient() *os.Process {
	pid, err := os.ReadFile(udhcpcPID)
	if err != nil {
		return nil
	}
	p, err := strconv.Atoi(strings.TrimSpace(string(pid)))
	if err != nil || p <= 0 {
		return nil
	}
	proc, err := os.FindProcess(p)
	if err != nil || proc.Signal(sigAlive) != nil {
		return nil
	}
	return proc
}

// sigAlive asks whether a process is there without doing anything to it.
var sigAlive = syscall.Signal(0)

// udhcpcPID is where techo5-net's udhcpc keeps its pid.
var udhcpcPID = "/run/udhcpc.pid"

// startDHCP starts the Echo Dot's DHCP client when none is running. techo5-net starts it at boot only
// once a network was joined, so a Dot that booted with no network it could join (the case Improv is
// for) had no one to ask for an address when it joined one later, and sat on it with IPv6 alone. This
// starts udhcpc only, the way techo5-net does: never techo5-net itself, which starts a supplicant of
// its own and so knocks the running one, and the network with it, off wlan0.
func startDHCP() {
	dhcpMu.Lock()
	defer dhcpMu.Unlock()
	if dhcpClient() != nil {
		return
	}
	// techo5-net's hook, when it wrote one; otherwise the same, written here.
	if _, err := os.Stat(udhcpcScript); err != nil {
		if err := os.WriteFile(udhcpcScript, []byte(udhcpcHook), 0o755); err != nil {
			slog.Warn("wifi: no DHCP client was running, and its script could not be written", "err", err)
			return
		}
	}
	// -b: into the background once it has a lease or has tried, so it stays to renew the lease, as
	// techo5-net's does. Its output goes to the log techo5-net's goes to, a file and not a pipe: the
	// client it leaves running keeps its stdout and stderr, and a pipe would hold Wait (and dhcpMu)
	// for as long as the client lives. No deadline: killed before -b takes effect, there would be no
	// client at all; on its own it is in the foreground for at most -t 8 tries of 3 s.
	cmd := exec.Command("udhcpc", "-i", "wlan0", "-s", udhcpcScript, "-b", "-t", "8", "-p", udhcpcPID)
	if f, err := os.OpenFile(udhcpcLog, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644); err == nil {
		defer f.Close()
		cmd.Stdout, cmd.Stderr = f, f
	}
	if err := cmd.Run(); err != nil {
		slog.Warn("wifi: no DHCP client was running, and starting one failed", "err", err)
		return
	}
	slog.Info("wifi: no DHCP client was running, started one")
}

// dhcpMu keeps two joins close together from starting two clients.
var dhcpMu sync.Mutex

// udhcpcLog is where techo5-net's udhcpc writes.
var udhcpcLog = "/data/techo5-linux/udhcpc.log"

// ownConf is the network the installer or wifi-set wrote on the Dot (techo5-net's OWN).
var ownConf = "/data/techo5-linux/wifi.conf"

// keptDate is the time the supplicant's file is to carry when it is written, for it to stay the one
// the Dot boots with, and whether that is not simply now. techo5-net keeps the file only while it is
// newer than ownConf (`test -nt`, whole seconds), and rebuilds it from ownConf otherwise: a Dot that
// booted with no network has no time from NTP, so a join stamped it with a clock years behind the
// installer's ownConf, and the next boot threw the network joined away for the one it could not join.
// So it is dated a second past ownConf whenever the clock is not already past it.
func keptDate() (time.Time, bool) {
	if !keptByDate {
		return time.Time{}, false
	}
	own, err := os.Stat(ownConf)
	if err != nil {
		return time.Time{}, false
	}
	if time.Now().Unix() > own.ModTime().Unix() {
		return time.Time{}, false
	}
	return time.Unix(own.ModTime().Unix()+1, 0), true
}

// udhcpcScript is where techo5-net puts the hook busybox's udhcpc hands a lease to.
var udhcpcScript = "/run/udhcpc.sh"

// udhcpcHook is techo5-net's hook (techo5-dot tools/linux/rootfs/usr/local/sbin/techo5-net), for when
// it never got as far as writing it: busybox's client hands a lease to a script or throws it away.
const udhcpcHook = `#!/bin/sh
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

// decodeName undoes the escaping wpa_cli puts on a network name on the way out. The supplicant prints
// a name through printf_encode, which spells a backslash, a quote, the whitespace characters and
// anything unprintable as an escape, while the name this code holds is the plain text: the two have to
// be brought to the same form before they are compared. Without this a name with a quote or a
// backslash in it never matches the one just asked for, so Join waits out its forty seconds on a
// network that is in fact already joined and then reports it could not be joined.
func decodeName(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 == len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'e':
			b.WriteByte(0x1b)
		case 'x':
			v, err := strconv.ParseUint(s[min(i+1, len(s)):min(i+3, len(s))], 16, 8)
			if err != nil {
				b.WriteByte('x')
				continue
			}
			b.WriteByte(byte(v))
			i += 2
		default:
			b.WriteByte(s[i]) // \\ and \" stand for themselves
		}
	}
	return b.String()
}

// oneLine is whether a value can go in the configuration file at all. The file is read a line at a
// time, so a newline inside a name or a passphrase ends the line it is on and everything after it is
// read as configuration — a network somebody typed into the "other network" box on the setup page
// could write settings nobody asked for. Nothing else in the file is under anyone's thumb, so this
// is the one place the file can be written from, and a value carrying a line ending is refused here
// rather than escaped: no name or passphrase has one, and a value this quietly rewrote would join a
// network the person never named. Carriage returns and the other control characters go with it, for
// the same reason and because the supplicant would not read them back as typed either.
//
// The quote and the backslash need no such care: block writes both values in hex.
func oneLine(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// SettingUp marks, or clears, the setup in progress for the boot scripts.
func SettingUp(on bool) {
	if on {
		_ = os.WriteFile(SetupFlag, []byte("1"), 0o644)
		return
	}
	_ = os.Remove(SetupFlag)
}
