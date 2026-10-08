package home

import (
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
	"github.com/HuskerMinion/techo5/echod/internal/lib/locale"
)

// With the sun down at home, every condition drawn with a sun by day takes its night form, drawn with
// the moon; the rest are left alone. Night is from home's own sunrise and sunset, so a device with no
// Home Assistant gets it too, and one that does not know where home is yet is left as reported.
func TestTheSkyAtNightHasNoSun(t *testing.T) {
	noon, late, early := homeOnThePlains(t)
	k := &sunKept
	f := &Feature{}
	for _, c := range []struct {
		cond      string
		day, dark string
	}{
		{"sunny", "sunny", "clear-night"},
		{"partlycloudy", "partlycloudy", PartlyCloudyNight},
		{"clear-night", "clear-night", "clear-night"},
		{"cloudy", "cloudy", "cloudy"},
		{"rainy", "rainy", "rainy"},
		{"lightning-rainy", "lightning-rainy", "lightning-rainy"},
		{"", "", ""},
	} {
		if got := f.SkyAt(c.cond, noon); got != c.day {
			t.Errorf("%q at one in the afternoon is %q, want %q", c.cond, got, c.day)
		}
		if got := f.SkyAt(c.cond, late); got != c.dark {
			t.Errorf("%q at eleven at night is %q, want %q", c.cond, got, c.dark)
		}
		if got := f.SkyAt(c.cond, early); got != c.dark {
			t.Errorf("%q at one in the morning is %q, want %q", c.cond, got, c.dark)
		}
	}

	k.mu.Lock()
	k.placed = false
	k.mu.Unlock()
	if got := f.SkyAt("partlycloudy", late); got != "partlycloudy" {
		t.Errorf("with home's place unknown, a partly cloudy night is %q, want it as reported", got)
	}

	// The night form is said as partly cloudy is, in every language.
	if got := ConditionWords(PartlyCloudyNight); got != "Partly cloudy" {
		t.Errorf("ConditionWords(PartlyCloudyNight) = %q", got)
	}
	for _, lang := range []string{"en", "de", "fr"} {
		if got, want := locale.Sky(PartlyCloudyNight, lang), locale.Sky("partlycloudy", lang); got != want {
			t.Errorf("in %s the night form is %q, want %q", lang, got, want)
		}
	}
}

// Without a reading, a screen shows today's forecast in its place. That is a daytime forecast, and
// standing in for the reading after dark it takes the night form too, or the page showed a sun at
// night. A reading is taken as it is: it has its night form already (Weather).
func TestTodaysForecastInPlaceOfTheReadingAtNight(t *testing.T) {
	noon, late, _ := homeOnThePlains(t)
	f := &Feature{}
	days := []hass.Day{{When: noon, Condition: "partlycloudy"}, {When: noon.AddDate(0, 0, 1), Condition: "sunny"}}

	if got := f.SkyNow(Weather{}, days, noon); got != "partlycloudy" {
		t.Errorf("by day the forecast in place of the reading is %q, want partlycloudy", got)
	}
	if got := f.SkyNow(Weather{}, days, late); got != PartlyCloudyNight {
		t.Errorf("at night the forecast in place of the reading is %q, want %q", got, PartlyCloudyNight)
	}
	if got := f.SkyNow(Weather{Condition: "rainy"}, days, late); got != "rainy" {
		t.Errorf("with a reading the screen shows %q, want the reading", got)
	}
	if got := f.SkyNow(Weather{}, nil, late); got != "" {
		t.Errorf("with nothing at all the screen shows %q", got)
	}
}

// homeOnThePlains puts home at 40 N 100 W for the test, where the sun is up from about 7:25 to 7:45
// on the day it returns times for: one in the afternoon, eleven at night and one in the morning.
func homeOnThePlains(t *testing.T) (noon, late, early time.Time) {
	t.Helper()
	zone := time.FixedZone("CDT", -5*60*60)
	noon = time.Date(2026, 9, 16, 13, 0, 0, 0, zone)
	late = time.Date(2026, 9, 16, 23, 0, 0, 0, zone)
	early = time.Date(2026, 9, 16, 1, 0, 0, 0, zone)

	k := &sunKept
	k.mu.Lock()
	lat, lon, placed, looked := k.lat, k.lon, k.placed, k.looked
	k.lat, k.lon, k.placed = 40.0, -100.0, true
	k.looked, k.day = noon.Format("2006-01-02"), "" // looked up already, so nothing asks Home Assistant
	k.mu.Unlock()
	t.Cleanup(func() {
		k.mu.Lock()
		k.lat, k.lon, k.placed, k.looked, k.day = lat, lon, placed, looked, ""
		k.mu.Unlock()
	})
	return noon, late, early
}

// Inside the polar circle, where the sun neither rises nor sets that day, night is from the sun's
// height: the polar night has no sun even at noon, and the midnight sun shows one at midnight.
func TestThePolarNightHasNoSun(t *testing.T) {
	k := &sunKept
	k.mu.Lock()
	lat, lon, placed, looked := k.lat, k.lon, k.placed, k.looked
	k.lat, k.lon, k.placed, k.day = 69.65, 18.96, true, ""
	k.mu.Unlock()
	t.Cleanup(func() {
		k.mu.Lock()
		k.lat, k.lon, k.placed, k.looked, k.day = lat, lon, placed, looked, ""
		k.mu.Unlock()
	})
	f := &Feature{}
	for _, c := range []struct {
		at   time.Time
		want string
	}{
		{time.Date(2026, 12, 21, 11, 0, 0, 0, time.UTC), "clear-night"},
		{time.Date(2026, 6, 21, 23, 0, 0, 0, time.UTC), "sunny"},
	} {
		k.mu.Lock()
		k.looked, k.day = c.at.Format("2006-01-02"), ""
		k.mu.Unlock()
		if got := f.SkyAt("sunny", c.at); got != c.want {
			t.Errorf("sunny at %v is %q, want %q", c.at, got, c.want)
		}
	}
}
