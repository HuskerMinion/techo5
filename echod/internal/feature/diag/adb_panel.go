//go:build panel

package diag

import (
	"log/slog"
	"os"

	"github.com/HuskerMinion/techo5/echod/internal/lib/panelctl"
)

// Android must grant the app WRITE_SECURE_SETTINGS explicitly before the daemon starts. Other
// tablets and ungranted installs keep the diagnostic entity absent.
var adbHere = os.Getenv("TECHO5_PANEL_ADB_CONTROL") == "1"

const adbViaPanel = true

func panelADBSetup(d *Diag) {
	if !adbHere {
		return
	}
	d.adb.Base.Name = "ADB debugging (USB + Wi-Fi)"
	d.adb.OnCommand = func(on bool) {
		if on {
			panelctl.Tell("adb 1")
		} else {
			panelctl.Tell("adb 0")
		}
	}
	panelctl.Handle("adb", func(args []string) {
		if len(args) < 2 {
			return
		}
		if args[1] != "1" {
			slog.Warn("panel ADB control unavailable")
			d.adb.Set(false)
			return
		}
		d.adb.Set(args[0] == "1")
	})
	panelctl.Send("adb status")
}
