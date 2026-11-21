//go:build !dot

package display

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The screen's text that was written straight into the drawing code, and the stored option values,
// follow the screen language (#324).
func TestScreenTextOutsideTheCatalogFollowsTheLanguage(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { _ = config.Set().Screen().Language("") })
	monWed := uint8(1<<1 | 1<<3)
	for _, c := range []struct {
		lang, weekdays, pin, forecast, initial, blurb string
	}{
		{"en", "weekdays", "Enter the PIN", "Home Assistant forecast", "S", "Rings at "},
		{"ja", "平日", "PINを入力", "Home Assistantの予報", "日", "に鳴ります（毎日）"},
		{"de", "werktags", "PIN eingeben", "Home Assistant-Vorhersage", "S", "Klingelt um "},
		{"es", "días laborables", "Introduce el PIN", "Previsión de Home Assistant", "D", "Suena a las "},
	} {
		if err := config.Set().Screen().Language(c.lang); err != nil {
			t.Fatal(err)
		}
		if got := daysLabel(config.DaysWeekdays); got != c.weekdays {
			t.Errorf("%s: weekdays %q, want %q", c.lang, got, c.weekdays)
		}
		// The days' own short names come from the date words, which more languages have than the catalog.
		if got := daysLabel(monWed); (got == "Mon Wed") != (c.lang == "en") {
			t.Errorf("%s: custom days %q", c.lang, got)
		}
		if got := pinNow(time.Now()).title; got != c.pin {
			t.Errorf("%s: PIN title %q, want %q", c.lang, got, c.pin)
		}
		if got := optionLabel("Home Assistant forecast"); got != c.forecast {
			t.Errorf("%s: weather source %q, want %q", c.lang, got, c.forecast)
		}
		if got := weekdayInitials(); len(got) != 7 || got[0] != c.initial {
			t.Errorf("%s: day chips %q", c.lang, got)
		}
		card := alarmEditorCard(sheetView{}, alarmDraft{alarm: config.Alarm{Hour: 7, Days: config.DaysEvery}})
		if !strings.Contains(card.blurb, c.blurb) {
			t.Errorf("%s: alarm editor heading %q, want %q in it", c.lang, card.blurb, c.blurb)
		}
	}
}
