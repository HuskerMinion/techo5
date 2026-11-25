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

		// In words, as speech to text writes them and an automation passes them on (#734).
		"five minutes":              5 * time.Minute,
		"one minutes":               time.Minute,
		"Three minutes":             3 * time.Minute,
		"twenty five minutes":       25 * time.Minute,
		"twenty-five minutes":       25 * time.Minute,
		"ten seconds":               10 * time.Second,
		"two hours and ten minutes": 2*time.Hour + 10*time.Minute,
		"half an hour":              30 * time.Minute,
		"half a minute":             30 * time.Second,
		"a half hour":               30 * time.Minute,
		"an hour and a half":        90 * time.Minute,
		"a minute and a half":       90 * time.Second,
		"two and a half minutes":    150 * time.Second,
		"1 and a half hours":        90 * time.Minute,
		"one hour and a half":       90 * time.Minute,
		"in five minutes":           5 * time.Minute,
		"seventy minutes":           70 * time.Minute,
		"seventy five minutes":      75 * time.Minute,
		"seventy-five minutes":      75 * time.Minute,
		"eighty seconds":            80 * time.Second,
		"ninety minutes":            90 * time.Minute,
		"a hundred seconds":         100 * time.Second,
		"one hundred minutes":       100 * time.Minute,
		"one hundred and ten mins":  110 * time.Minute,
		"half hour":                 30 * time.Minute,
		"half minute":               30 * time.Second,
		"an hour and half hour":     90 * time.Minute,
		"24 hours":                  24 * time.Hour,
	}
	for in, want := range good {
		got, err := ParseDuration(in)
		if err != nil || got != want {
			t.Errorf("ParseDuration(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "soon", "10 fortnights", "minutes", "0", "-5", "0:00:00", "25:00:00", "1:75", "1:2:3:4", "2 days",
		"five", "half", "half an", "half a day", "and five minutes", "five minutes and", "-5 minutes", "five fortnights",
		"minutes five", "twenty five hours", "a half",
		// Too long to hold: held at too long rather than wrapping round to a length that fits (#734).
		"1e19 seconds and 1e19 seconds and 1 hour", "1e19 seconds", "1e19", "-1e19 seconds and 1e19 seconds and 1 hour", "-5 minutes and 10 minutes",
		"9e18 nanoseconds", "inf minutes", "nan minutes", "inf", "nan", "half day", "24 hours and 1 second"} {
		if got, err := ParseDuration(in); err == nil {
			t.Errorf("ParseDuration(%q) = %v, want an error", in, got)
		}
	}
}
