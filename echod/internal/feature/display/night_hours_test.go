//go:build !dot && !spot

package display

import (
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// nightTestDisplay is a Show with the night's Home Assistant settings, and a fresh config.
func nightTestDisplay(t *testing.T) *Display {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "config.json"))
	d := &Display{poke: make(chan struct{}, 1)}
	d.nightHours = nightHoursSelect(d)
	d.nightStart, d.nightEnd = nightEndSelect(d, true), nightEndSelect(d, false)
	return d
}

// On the screen, Custom picks the start hour, its minutes, the end hour and its minutes, and the night
// is then those hours: 7 PM to 9:30 AM, which no preset could say.
func TestCustomNightHoursOnTheScreen(t *testing.T) {
	d := nightTestDisplay(t)
	p, _ := pickerFor("night", sheetView{})
	if p.opts[len(p.opts)-1] != nightCustomRow {
		t.Fatalf("the night list ends %q", p.opts[len(p.opts)-1])
	}
	d.choose("night", len(nightPresets))
	for _, step := range []struct {
		id   string
		pick int
	}{{"nightfromh", 19}, {"nightfromm", 0}, {"nighttoh", 9}, {"nighttom", 2}} {
		if d.picker != step.id {
			t.Fatalf("expected the %s picker, have %q", step.id, d.picker)
		}
		d.picker = "" // as a tap on a choice does, before choose
		d.choose(step.id, step.pick)
	}
	if got := config.Get().Screen.Night; got != "19:00-09:30" {
		t.Fatalf("night hours %q", got)
	}
	if got := d.nightHours.Get(); got != nightCustom {
		t.Errorf("Home Assistant shows %q", got)
	}
	if d.nightStart.Get() != "19:00" || d.nightEnd.Get() != "09:30" {
		t.Errorf("starts %q, ends %q", d.nightStart.Get(), d.nightEnd.Get())
	}
	if p, _ := pickerFor("night", sheetView{st: settings{night: "19:00-09:30"}}); p.cur != len(p.opts)-1 {
		t.Errorf("the list marks choice %d, not Custom", p.cur)
	}
}

// From Home Assistant: a preset is still a preset, Night starts and Night ends move one end each, and
// the action sets both, or none.
func TestNightHoursFromHomeAssistant(t *testing.T) {
	d := nightTestDisplay(t)
	d.nightHours.OnCommand(nightHoursText("23-6"))
	if got := config.Get().Screen.Night; got != "23-6" {
		t.Fatalf("preset: %q", got)
	}
	if d.nightHours.Get() != nightHoursText("23-6") {
		t.Errorf("the preset shows as %q", d.nightHours.Get())
	}
	d.nightStart.OnCommand("19:15")
	if got := config.Get().Screen.Night; got != "19:15-06:00" {
		t.Fatalf("after Night starts: %q", got)
	}
	d.nightEnd.OnCommand("19:15") // the same as the start: refused
	if got := config.Get().Screen.Night; got != "19:15-06:00" {
		t.Fatalf("a night of no length was taken: %q", got)
	}

	if d.Actions()[0].Name != "screen_night_hours" {
		t.Fatalf("action %q", d.Actions()[0].Name)
	}
	run := d.setNightHours
	if err := run("20:00", "09:30"); err != nil || config.Get().Screen.Night != "20:00-09:30" {
		t.Errorf("action: %v, %q", err, config.Get().Screen.Night)
	}
	if err := run("21:00", "7:00"); err != nil || config.Get().Screen.Night != "21-7" {
		t.Errorf("whole hours are kept the old way: %v, %q", err, config.Get().Screen.Night)
	}
	if err := run("late", "9"); err == nil {
		t.Error("a time that does not read was taken")
	}
	if err := run("", ""); err != nil || config.Get().Screen.Night != "" {
		t.Errorf("clearing: %v, %q", err, config.Get().Screen.Night)
	}
}
