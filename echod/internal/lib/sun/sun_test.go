package sun

import (
	"testing"
	"time"
)

// Against published almanac times, to within three minutes.
func TestTimes(t *testing.T) {
	london, _ := time.LoadLocation("Europe/London")
	denver, _ := time.LoadLocation("America/Denver")
	for _, c := range []struct {
		name      string
		lat, lon  float64
		day       time.Time
		rise, set string
	}{
		{"London, midsummer", 51.5074, -0.1278, time.Date(2024, 6, 21, 0, 0, 0, 0, london), "04:43", "21:21"},
		{"Denver, midwinter", 39.7392, -104.9903, time.Date(2024, 12, 21, 0, 0, 0, 0, denver), "07:18", "16:39"},
	} {
		rise, set, ok := Times(c.lat, c.lon, c.day)
		if !ok {
			t.Fatalf("%s: no sunrise", c.name)
		}
		for _, p := range []struct {
			got  time.Time
			want string
		}{{rise, c.rise}, {set, c.set}} {
			w, _ := time.ParseInLocation("15:04", p.want, c.day.Location())
			want := time.Date(c.day.Year(), c.day.Month(), c.day.Day(), w.Hour(), w.Minute(), 0, 0, c.day.Location())
			if d := p.got.Sub(want); d > 3*time.Minute || d < -3*time.Minute {
				t.Errorf("%s: got %s, want %s", c.name, p.got.Format("15:04"), p.want)
			}
		}
	}
}

// Above the Arctic Circle at midsummer the sun does not set.
func TestMidnightSun(t *testing.T) {
	if _, _, ok := Times(78.2, 15.6, time.Date(2024, 6, 21, 0, 0, 0, 0, time.UTC)); ok {
		t.Error("Svalbard at midsummer has a sunset")
	}
}

// The sun's height: over the Arctic it stays down at noon in December and up at midnight in June,
// and in mid latitudes it is up at noon and down at midnight, agreeing with Times.
func TestTheSunsHeight(t *testing.T) {
	tromso := func(m time.Month, d, h int) time.Time { return time.Date(2026, m, d, h, 0, 0, 0, time.UTC) }
	if Up(69.65, 18.96, tromso(time.December, 21, 11)) {
		t.Error("the sun was up at noon in the polar night")
	}
	if !Up(69.65, 18.96, tromso(time.June, 21, 23)) {
		t.Error("the midnight sun was down")
	}
	denver, _ := time.LoadLocation("America/Denver")
	day := time.Date(2026, 3, 20, 0, 0, 0, 0, denver)
	rise, set, ok := Times(39.74, -104.99, day)
	if !ok {
		t.Fatal("no times")
	}
	if a := Altitude(39.74, -104.99, rise); a < -1.5 || a > 0 {
		t.Errorf("at sunrise the sun is at %.2f degrees", a)
	}
	if a := Altitude(39.74, -104.99, set); a < -1.5 || a > 0 {
		t.Errorf("at sunset the sun is at %.2f degrees", a)
	}
	if !Up(39.74, -104.99, day.Add(12*time.Hour)) || Up(39.74, -104.99, day) {
		t.Error("noon and midnight the wrong way round")
	}
}

// Known heights, from NOAA's solar calculator: Tromso at local solar noon on December 21 (10:42 UTC)
// and at local solar midnight on June 21 (22:46 UTC), both within half a degree.
func TestTheSunsHeightAtTromso(t *testing.T) {
	for _, c := range []struct {
		name string
		at   time.Time
		want float64
	}{
		{"midwinter noon", time.Date(2026, time.December, 21, 10, 42, 0, 0, time.UTC), -3.1},
		{"midsummer midnight", time.Date(2026, time.June, 21, 22, 46, 0, 0, time.UTC), 3.1},
	} {
		if got := Altitude(69.65, 18.96, c.at); got < c.want-0.5 || got > c.want+0.5 {
			t.Errorf("%s: %.2f degrees, want %.1f", c.name, got, c.want)
		}
	}
}
