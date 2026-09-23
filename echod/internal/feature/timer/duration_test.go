package timer

import (
	"testing"
	"time"
)

func TestParseDuration(t *testing.T) {
	good := map[string]time.Duration{
		"10":                    10 * time.Minute,
		"1.5":                   90 * time.Second,
		"10 minutes":            10 * time.Minute,
		"1 minute":              time.Minute,
		"a minute":              time.Minute,
		"an hour":               time.Hour,
		"1 hour and 30 minutes": 90 * time.Minute,
		"1 hr, 5 min":           65 * time.Minute,
		"90 seconds":            90 * time.Second,
		"In 20 Minutes":         20 * time.Minute,
		"1h30m":                 90 * time.Minute,
		"1h 30m":                90 * time.Minute,
		"45s":                   45 * time.Second,
		"00:10:00":              10 * time.Minute,
		"1:30":                  90 * time.Minute,
		"0:00:30":               30 * time.Second,
		"24:00:00":              24 * time.Hour,
	}
	for in, want := range good {
		got, err := ParseDuration(in)
		if err != nil || got != want {
			t.Errorf("ParseDuration(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "soon", "10 fortnights", "minutes", "0", "-5", "0:00:00", "25:00:00", "1:75", "1:2:3:4", "2 days"} {
		if got, err := ParseDuration(in); err == nil {
			t.Errorf("ParseDuration(%q) = %v, want an error", in, got)
		}
	}
}
