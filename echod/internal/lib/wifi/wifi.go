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
	"time"
)

const (
	iface   = "wlan0"
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
	out, err := exec.CommandContext(ctx, "wpa_cli", append([]string{"-p", ctrlDir, "-i", iface}, args...)...).CombinedOutput()
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
	i, err := net.InterfaceByName(iface)
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
	if err := os.WriteFile(Conf, []byte(conf(kept)), 0o600); err != nil {
		return err
	}
	if len(old) > 0 {
		_ = os.WriteFile(Conf+".prev", old, 0o600)
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
	deadline := time.Now().Add(40 * time.Second)
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
		_ = os.WriteFile(Conf, old, 0o600)
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

// renewLease pokes udhcpc for a new lease on the new network.
func renewLease() {
	pid, err := os.ReadFile("/run/udhcpc.pid")
	if err != nil {
		return
	}
	p, err := strconv.Atoi(strings.TrimSpace(string(pid)))
	if err != nil {
		return
	}
	if proc, err := os.FindProcess(p); err == nil {
		_ = proc.Signal(sigRenew)
	}
}

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
