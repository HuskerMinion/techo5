package firstrun

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wifi"
	"github.com/HuskerMinion/techo5/echod/internal/update"
)

// The marker a gift install leaves (the installers' --for-someone-else), and a reset for someone else
// too: first run is to do whatever the device was before. It goes when first run finishes.
func markerPath() string { return filepath.Join(stateDir, "firstrun") }

// stateDir is the device's state directory (a variable for the tests: it is a constant on most).
var stateDir = layout.StateDir

func marked() bool {
	_, err := os.Stat(markerPath())
	return err == nil
}

func unmark() {
	if err := os.Remove(markerPath()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		slog.Warn("removing the first-run marker failed", "err", err)
	}
}

// personal is what a reset for someone else deletes from the state directory, beside the settings
// (state.json, put back to the defaults): everything that is the old home's or says who they are. Kept:
// the wake word models, the device's own MAC and serial, and what the hardware measured of itself.
// docs/setup.md lists the same.
var personal = []string{
	"psk",                    // the key Home Assistant connects with: none, so a Home Assistant can add it
	"name",                   // the name: it is named from its MAC again
	"phone.json",             // the SIP account
	"phone-contacts.json",    // the phone book
	"photos",                 // the slideshow's photos
	"sounds",                 // sounds of its own
	"fonts",                  // fonts of its own
	"recordings",             // recordings kept for the wake word
	"wake-clips",             // wake word clips kept
	"ssh",                    // SSH keys allowed in
	"tailscale",              // Tailscale's login
	"netbird",                // NetBird's login
	"tessera-layout.json",    // the Tessera dashboard's layout
	"ntp-server",             // the home's time server
	"timezone",               // the time zone chosen here, which follows Home Assistant again
	"localtime",              //   and its zone file
	"timezone-set-here",      //   and that it was chosen here
	"crash",                  // crash reports, which can hold names and addresses
	"techo5.log", "boot.log", // logs, the same
}

// CanReset is whether this device can be reset for someone else. One with no screen gets its new network
// over Bluetooth (Improv, #355) afterward; on a board Improv has not been tried on yet (improvUntried), a
// reset could leave it off the network with only a reinstall over USB to bring it back.
func CanReset() bool { return !improvUntried() }

// improvUntried is whether this board has no screen and Improv has not been tried on it yet (screen_dot.go).
var improvUntried = func() bool { return false }

// forgetHome deletes the giver's Home Assistant entry for this device, when the device was given a token
// for that Home Assistant (lib/hass): after the reset it holds a key the device no longer has, and fails
// until someone deletes it. Without a token, or with one that may not delete it, the setup page and the
// docs say to delete it by hand. A variable for the tests.
var forgetHome = func(ctx context.Context) {
	if !hass.Get().Ready() {
		return
	}
	mac, err := layout.FactoryMAC()
	if err != nil || mac == "" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	n, err := hass.Get().RemoveOwnEntry(ctx, mac)
	if err != nil {
		slog.Warn("reset: could not delete this device from Home Assistant; delete it there by hand", "err", err)
		return
	}
	slog.Info("reset: deleted this device from Home Assistant", "entries", n)
}

// restart is the daemon restarting into first run (a variable for the tests).
var restart = func() { update.Restart("reset for someone else") }

// resetPath is the marker a reset leaves for the next start, which does the wipe (wipe) before any
// feature has started: done now, a feature's state written back on the way down would survive it.
func resetPath() string { return filepath.Join(stateDir, "reset") }

// Reset clears the device for somebody else and restarts it into first run. Now it leaves the reset and
// first-run markers and, once its answer is away, deletes its Home Assistant entry where it can
// (forgetHome) and forgets the Wi-Fi networks, from the running supplicant and from every file a boot
// could bring one back from (wipeWifi); the next start wipes the rest (wipe) before anything else runs.
func Reset() error {
	if !CanReset() {
		return errors.New("this device cannot be reset for someone else yet: it might not be given a new Wi-Fi network afterwards")
	}
	if err := os.WriteFile(markerPath(), nil, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(resetPath(), nil, 0o600); err != nil {
		return err
	}
	slog.Warn("reset for someone else: restarting to wipe the device")
	safe.Go("reset for someone else", func() {
		// The answer reaches whoever asked first: Home Assistant, when it was its action, would report
		// the action failed with its entry deleted or the network gone before then. A restart in these
		// moments is no loss, since the markers are written and the wipe at the next start does it all.
		time.Sleep(2 * time.Second)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		// While it is still on the network: Home Assistant's entry for it would only fail from now on.
		forgetHome(ctx)
		for _, ssid := range wifi.Saved() {
			if err := wifi.Forget(ctx, ssid); err != nil {
				slog.Warn("reset: forgetting a network failed", "err", err)
			}
		}
		wipeWifi()
		if wifiAvailable() {
			dropNetwork(ctx)
		}
		restart()
	})
	return nil
}

// wipe is the reset itself, at the start after Reset, from Settle: the name, the room, Home Assistant's
// key and access, alarms, timers, the phone book and every other setting (back to the defaults,
// automatic updates off), and the old home's files (personal). Like leaving Home Assistant
// (api/leave.go), the access to Home Assistant goes first; then, unlike it, the key is removed rather
// than replaced, so the next Home Assistant can add the device with nothing typed. The reset marker
// goes last, so a wipe cut short is done again at the next start.
func wipe() {
	wipeFiles()
	if err := config.Set().FirstRun().ForSomeoneElse(); err != nil {
		slog.Error("reset: putting the settings back failed", "err", err)
		return
	}
	if err := os.Remove(resetPath()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		slog.Warn("reset: removing its marker failed", "err", err)
	}
	slog.Warn("reset for someone else: wiped; first run from the start")
}

// BeforeConfig is the part of a reset's wipe that has to come before the daemon reads who it is
// (boot, before config.Started): the name file above all, which the name the device announces is read
// from once at the start, so wiped any later the old name is announced until the next restart. The
// settings are Settle's, once they are loaded.
func BeforeConfig() {
	if _, err := os.Stat(resetPath()); err == nil {
		wipeFiles()
	}
}

// wipeFiles deletes the old home's files: Home Assistant's access first, the networks a boot could
// bring back, then the rest (personal). Done twice after a reset, harmlessly: before the settings are
// read and with them.
func wipeFiles() {
	if err := os.Remove(hass.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		slog.Warn("reset: removing Home Assistant's access failed", "err", err)
	}
	wipeWifi() // again: a wipe after a reset that stopped short of it
	for _, p := range personal {
		if err := os.RemoveAll(filepath.Join(stateDir, p)); err != nil {
			slog.Warn("reset: removing failed", "what", p, "err", err)
		}
	}
	if err := os.RemoveAll(filepath.Join(filepath.Dir(stateDir), "techo5-spotify")); err != nil {
		slog.Warn("reset: removing Spotify's login failed", "err", err)
	}
}

// dropNetwork takes the device off the network after the networks are forgotten (a variable for the
// tests): a restart of the daemon keeps the interface's address, and a device that still has one looks
// online to everything that asks, Improv's offer at boot among them.
var (
	dropNetwork   = wifi.Drop
	wifiAvailable = wifi.Available
)
