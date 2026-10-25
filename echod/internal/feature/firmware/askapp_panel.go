//go:build panel

package firmware

import (
	"strings"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/panelctl"
)

// askApp has the panel app look for an update: the app carries this daemon and is updated as a whole,
// from the release server it was built for. A check only reports what the server offers ("update
// check"); an install looks again and installs a newer build if there is one ("update install"), which
// is Home Assistant's Install, or the night's automatic install (autoLoop) when that is switched on.
func askApp(install bool) {
	if install {
		tellApp("update install")
		return
	}
	tellApp("update check")
}

// tellApp is the control line, set apart so a test can read what the app is asked.
var tellApp = panelctl.Tell

// The app answers every check, and each new connection, with "update <running> [offered]": the app's
// versions, which are this daemon's too (build-apk.sh stamps the same one into both).
func init() {
	panelctl.Handle("update", func(args []string) {
		if len(args) == 0 {
			return
		}
		Get().appSays(args[0], args[len(args)-1])
	})
}

func (u *Firmware) appSays(running, offered string) {
	offered = "v" + strings.TrimPrefix(offered, "v")
	latest := layout.Version
	u.mu.Lock()
	u.appOffered = ""
	if offered != "v"+strings.TrimPrefix(running, "v") {
		u.appOffered, latest = offered, offered
	}
	u.mu.Unlock()
	u.entity.Set(esphome.UpdateState{CurrentVersion: layout.Version, LatestVersion: latest, Title: "TECHO5 Panel"})
}
