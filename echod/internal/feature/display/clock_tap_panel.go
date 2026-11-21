//go:build panel && !dot

package display

import (
	"slices"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// clockTapsHere are the Tap on the clock choices a panel offers: the Show's, and Apps, which opens the
// list of the tablet's other apps straight from the clock (tablet_apps_panel.go, discussion #652).
var clockTapsHere = append(slices.Clone(clockTaps), clockTap{"Apps", "apps"})

// nightTapOpensDashboard is whether a tap on the night screen opens the dashboard: when a tap on the
// clock does by day, as on the Show (clock_tap_show.go).
func nightTapOpensDashboard() bool { return config.Get().Screen.ClockTap == "dashboard" }
