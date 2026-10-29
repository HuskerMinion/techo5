//go:build !dot && !spot

package display

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The rows angelor87 found still in English follow the screen language (#310): the calendar lists,
// the timers, the dates of alarms and the confirm buttons.
func TestTheLeftoverEnglishFollowsTheLanguage(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { _ = config.Set().Screen().Language("") })
	sat := time.Date(2026, time.October, 31, 12, 0, 0, 0, time.Local)
	thu := time.Date(2026, time.October, 29, 12, 0, 0, 0, time.Local)
	for _, c := range []struct {
		lang, done, all, hour, two, running, weekday, later string
	}{
		{"en", "Done", "All calendars shown", "1 hour", "2 minutes", "Running", "Saturday", "Nov 20"},
		{"de", "Fertig", "Alle angezeigten Kalender", "1 Stunde", "2 Minuten", "Läuft", "Samstag", "20. November"},
		{"it", "Fatto", "Tutti i calendari mostrati", "1 ora", "2 minuti", "In corso", "Sabato", "20 novembre"},
	} {
		if err := config.Set().Screen().Language(c.lang); err != nil {
			t.Fatal(err)
		}
		if got := calendarsDone(); got != c.done {
			t.Errorf("%s: Done %q, want %q", c.lang, got, c.done)
		}
		if got := popupAllCalendars(); got != c.all {
			t.Errorf("%s: all calendars %q, want %q", c.lang, got, c.all)
		}
		labels := timerLabels()
		if len(labels) != len(timerLengths) || labels[len(labels)-1] != c.hour || labels[1] != c.two {
			t.Errorf("%s: timer lengths %q", c.lang, labels)
		}
		if got := tr("ui_running"); got != c.running {
			t.Errorf("%s: Running %q, want %q", c.lang, got, c.running)
		}
		if got := dayWord(sat, thu); got != c.weekday {
			t.Errorf("%s: two days on %q, want %q", c.lang, got, c.weekday)
		}
		if got := dayWord(time.Date(2026, time.November, 20, 7, 0, 0, 0, time.Local), thu); got != c.later {
			t.Errorf("%s: three weeks on %q, want %q", c.lang, got, c.later)
		}
	}
}

// Dim after idle goes down to Dimmest, so the Dimmest row is there whenever either needs it (#310).
func TestTheDimmestRowIsThereForDimAfterIdle(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	has := func(auto bool) bool {
		rows, _ := categoryRows(sheetView{st: settings{cat: catDisplay, auto: auto}})
		return slices.ContainsFunc(rows, func(r settingRow) bool { return r.id == "dimmest" })
	}
	if has(false) {
		t.Error("Dimmest shown with auto-brightness and Dim after idle both off")
	}
	if !has(true) {
		t.Error("Dimmest missing with auto-brightness on")
	}
	if err := config.Set().Screen().DimAfterMinutes(5); err != nil {
		t.Fatal(err)
	}
	if !has(false) {
		t.Error("Dimmest missing with Dim after idle on and auto-brightness off")
	}
}
