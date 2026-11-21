//go:build !dot && !spot && !panel

package display

import "github.com/HuskerMinion/techo5/echod/internal/config"

// clockTapsHere are the Tap on the clock choices the Show offers: all of them (clock_tap.go); a
// panel adds Apps (clock_tap_panel.go).
var clockTapsHere = clockTaps

// nightTapOpensDashboard is whether a tap on the night screen opens the dashboard: when a tap on the
// clock does by day. A night light is otherwise left alone when tapped, a hand in the dark being more
// likely a knock than a request; somebody who chose the dashboard for the clock's tap asked for it.
func nightTapOpensDashboard() bool { return config.Get().Screen.ClockTap == "dashboard" }

// openTabletApps is a panel's (tablet_apps_panel.go): a Show offers no Apps for the clock's tap.
func (*Display) openTabletApps() bool { return false }
