// Package netbird is what is left of the built-in NetBird client, taken out of TECHO5 for the
// techo5-netbird add-on (#580, docs/netbird.md), which runs NetBird's own client from userdata at boot.
// On the first start after the update, a device that had the built-in on, or still has the client it
// downloaded, says so once in the log, and the client goes.
//
// The device's identity on its NetBird network (state, under stateDir) is left where it is: the
// add-on's migrate takes it from there, so the device stays the same peer and needs no new setup key.
// The settings' NetBird section stays for the same reason (config.Netbird); only its switch is turned
// off, which the add-on waits for before it starts NetBird. A reset for someone else deletes both
// (feature/firstrun).
package netbird

import (
	"log/slog"
	"os"
	"path/filepath"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

func init() {
	component.Register(component.Network, Leftovers{}, component.Order(56))
}

// stateDir is where the built-in kept its client and its state. A variable, for tests.
var stateDir = filepath.Join(layout.StateDir, "netbird")

// AddOn is where NetBird went.
const AddOn = "https://github.com/tioan/techo5-netbird"

// Leftovers clears away what the built-in client left that is of no use without it.
type Leftovers struct{}

func (Leftovers) Name() string { return "netbird" }

// Restore turns the old switch off and deletes the downloaded client (about 40 MB on userdata), its
// license, and the mark a join left while it waited for the download.
func (Leftovers) Restore(c config.Config) {
	removed := clearClient()
	if c.Netbird.On {
		if err := config.Set().Netbird().Off(); err != nil {
			slog.Warn("netbird: turning the built-in's switch off", "err", err)
		}
	}
	if c.Netbird.On || removed {
		slog.Info("netbird: the built-in NetBird client was removed from TECHO5; the techo5-netbird add-on runs NetBird instead and can take over this device's peer",
			"add_on", AddOn)
	}
}

// clearClient deletes the built-in's client files, and says whether there were any.
func clearClient() bool {
	files, _ := filepath.Glob(filepath.Join(stateDir, "netbird-*")) // the client, and its archive
	files = append(files, filepath.Join(stateDir, "LICENSE"), filepath.Join(stateDir, "join-pending"))
	removed := false
	for _, f := range files {
		if err := os.Remove(f); err == nil {
			removed = true
		} else if !os.IsNotExist(err) {
			slog.Warn("netbird: removing what the built-in client left", "file", f, "err", err)
		}
	}
	return removed
}
