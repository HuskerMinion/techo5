// Package wifi is the device's Wi-Fi as wpa_supplicant runs it on the Linux image: what it is
// joined to, what it can hear, and joining something else. It talks to the supplicant through
// wpa_cli on the control socket the boot scripts open, and writes the same configuration file
// they start it from, so a network chosen on the screen is the one the next boot joins.
package wifi

import (
	"context"
	"errors"
	"fmt"
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
	return string(out), nil
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
			st.SSID = v
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
	if passphrase != "" && (len(passphrase) < 8 || len(passphrase) > 63) {
		return errors.New("wifi: a passphrase is 8 to 63 characters")
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
	deadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) {
		st := Current(ctx)
		if st.Connected && st.SSID == ssid {
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
	// Back to what worked.
	if len(old) > 0 {
		_ = os.WriteFile(Conf, old, 0o600)
		_, _ = cli(ctx, "reconfigure")
	}
	return fmt.Errorf("wifi: could not join %q (%s)", ssid, Current(ctx).State)
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

func escape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)
}

// SettingUp marks, or clears, the setup in progress for the boot scripts.
func SettingUp(on bool) {
	if on {
		_ = os.WriteFile(SetupFlag, []byte("1"), 0o644)
		return
	}
	_ = os.Remove(SetupFlag)
}
