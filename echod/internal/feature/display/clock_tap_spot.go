//go:build spot

package display

import (
	"log/slog"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
)

// clockTapsHere are the Tap on the clock choices the Spot offers: Assist, Dashboard and Nothing. The
// deck is the Show's; a Deck shared from a Show reads here as Assist, and a tap starts a turn.
var clockTapsHere = clockTaps[:3]

// clockTapSel is the Tap on the clock setting in Home Assistant.
func (d *Display) clockTapSel() *esphome.Select { return d.clockTap }

// clockTapTaken is a tap on the idle clock face as Tap on the clock says: true when it put the
// dashboard up or did nothing, false for the voice turn a tap has always started here (Assist, the
// default). With no dashboard set up, a tap meant for one starts a turn, as on the Show.
func (d *Display) clockTapTaken() bool {
	switch config.Get().Screen.ClockTap {
	case "nothing":
		return true
	case "dashboard":
		return d.openDashboardSpot()
	}
	return false
}

// openDashboardSpot puts the dashboard up, if there is one to put up: the menu's Dashboard item from
// the clock.
func (d *Display) openDashboardSpot() bool {
	if dashboard.Get().Mode() == config.DashboardOff {
		return false
	}
	d.mu.Lock()
	up := d.dashShowing
	d.mu.Unlock()
	if !up {
		d.toggleDashboard()
	}
	slog.Info("screen: dashboard by a tap on the clock")
	return true
}
