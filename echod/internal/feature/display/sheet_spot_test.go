//go:build spot

package display

import (
	"image"
	"math"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

func spotScene(cat category) roundScene {
	now := time.Date(2026, 9, 18, 15, 0, 0, 0, time.Local)
	st := settings{cat: cat, now: now, brightness: 75, volume: 5, name: "Kitchen", version: "v0.0.0",
		slot: "a", address: "192.168.1.50", wifiName: "Connected", weather: "Home", night: defaultNight}
	sv := sheetView{st: st, now: now, snooze: 9}
	sv.bt.Available, sv.bt.Remembered = true, "Speaker"
	sv.security.SSHAvailable = true
	sv.alarms.Local = []config.Alarm{{ID: "a", Hour: 6, Minute: 30, Days: config.DaysWeekdays, On: true}}
	return roundScene{now: now, sheetOpen: true, sheet: sv}
}

// inCircle is whether a zone's middle is on the round panel, where a finger can reach it.
func inCircle(r image.Rectangle) bool {
	c := r.Min.Add(r.Max).Div(2)
	return math.Hypot(float64(c.X-center), float64(c.Y-center)) < center
}

// The six categories are tiles to tap and Done closes; every page of every category draws a place to
// tap for each row in view, and all of them are on the round panel.
func TestSpotSettingsDraw(t *testing.T) {
	r := newRoundRenderer(image.NewRGBA(image.Rect(0, 0, side, side)))
	grid := spotScene(catDisplay)
	grid.sheetGrid = true
	r.sheetFace(grid)
	cats, done := 0, 0
	for _, z := range r.zones {
		switch z.kind {
		case zoneCat:
			cats++
		case zoneDone:
			done++
		}
		if !inCircle(z.r) {
			t.Errorf("grid zone %+v is off the panel", z)
		}
	}
	if cats != int(categories) || done != 1 {
		t.Errorf("grid has %d categories and %d Done, want %d and 1", cats, done, categories)
	}

	for c := category(0); c < categories; c++ {
		r.sheetFace(spotScene(c))
		back, rows := false, 0
		for _, z := range r.zones {
			if !inCircle(z.r) {
				t.Errorf("%s: zone %+v is off the panel", categoryNames[c], z)
			}
			back = back || (z.kind == zoneAction && z.id == "back")
			if z.kind == zoneRow {
				rows++
			}
		}
		if !back || rows == 0 {
			t.Errorf("%s: back %v, %d row zones", categoryNames[c], back, rows)
		}
	}
}

// The round card's rows: Updates splits into its channel and its check, a button's status goes under
// its name, and a choice keeps its value without a second button.
func TestSpotAdaptRows(t *testing.T) {
	rows := adaptRows([]settingRow{
		{id: "updates", label: "Updates", sub: "This is v1", kind: ctlChoice, value: "Stable", button: "Check now"},
		{id: "bt", label: "Speaker", kind: ctlButton, value: "Not connected", button: "Connect"},
		{id: "weather", label: "Weather", kind: ctlChoice, value: "Home", button: "Show"},
	}, sheetView{})
	if len(rows) != 5 || rows[0].button != "" || rows[1].id != "updatecheck" || rows[1].button != "Check now" {
		t.Fatalf("updates = %+v", rows[:2])
	}
	if rows[2].sub != "Not connected" || rows[2].value != "" {
		t.Errorf("bt row = %+v", rows[2])
	}
	if rows[3].id != "btforget" || rows[3].kind != ctlDanger {
		t.Errorf("no Forget after the speaker: %+v", rows[3])
	}
	if rows[4].button != "" || rows[4].value != "Home" {
		t.Errorf("weather row = %+v", rows[4])
	}
}

// On the Spot an empty night is the default hours, so Never is a value of its own, read as no night.
func TestSpotNight(t *testing.T) {
	if nightPresets[0] == "" || nightText(nightPresets[0]) != "Never" {
		t.Errorf("Never is %q (%q)", nightPresets[0], nightText(nightPresets[0]))
	}
	if nightPresets[1] != defaultNight {
		t.Errorf("the default night isn't offered: %v", nightPresets)
	}
}
