package timer

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Longest is the longest timer Home Assistant may set here. Past a day it is an alarm, and a number
// that large is more likely a mistake in the unit than a timer anybody wants.
const Longest = 24 * time.Hour

var units = map[string]time.Duration{
	"h": time.Hour, "hr": time.Hour, "hrs": time.Hour, "hour": time.Hour, "hours": time.Hour,
	"m": time.Minute, "min": time.Minute, "mins": time.Minute, "minute": time.Minute, "minutes": time.Minute,
	"s": time.Second, "sec": time.Second, "secs": time.Second, "second": time.Second, "seconds": time.Second,
}

// ParseDuration reads how long a timer runs, as a person or an automation would write it: "10
// minutes", "1 hour and 30 minutes", "in 20 minutes", "1h30m", "00:10:00" (Home Assistant's duration
// selector), "1:30" (hours and minutes), or a bare number, which is minutes.
func ParseDuration(s string) (time.Duration, error) {
	in := s
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimSpace(strings.TrimPrefix(s, "in "))
	d, err := parseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("timers: %q is not a length of time", in)
	}
	if d <= 0 || d > Longest {
		return 0, fmt.Errorf("timers: %q must be more than nothing and at most %v", in, Longest)
	}
	return d, nil
}

func parseDuration(s string) (time.Duration, error) {
	if n, err := strconv.ParseFloat(s, 64); err == nil {
		return time.Duration(n * float64(time.Minute)), nil
	}
	if d, err := time.ParseDuration(strings.ReplaceAll(s, " ", "")); err == nil {
		return d, nil
	}
	if strings.Contains(s, ":") {
		return clockDuration(s)
	}

	fields := strings.Fields(strings.NewReplacer(",", " ", " and ", " ").Replace(s))
	if len(fields) == 0 || len(fields)%2 != 0 {
		return 0, fmt.Errorf("not pairs of number and unit")
	}
	var total time.Duration
	for i := 0; i < len(fields); i += 2 {
		n, err := strconv.ParseFloat(fields[i], 64)
		if fields[i] == "a" || fields[i] == "an" {
			n, err = 1, nil
		}
		unit, ok := units[fields[i+1]]
		if err != nil || !ok {
			return 0, fmt.Errorf("%q %q", fields[i], fields[i+1])
		}
		total += time.Duration(n * float64(unit))
	}
	return total, nil
}

// clockDuration reads "h:mm:ss" or "h:mm".
func clockDuration(s string) (time.Duration, error) {
	parts := strings.Split(s, ":")
	if len(parts) != 2 && len(parts) != 3 {
		return 0, fmt.Errorf("too many parts")
	}
	scale := []time.Duration{time.Hour, time.Minute, time.Second}
	var total time.Duration
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || (i > 0 && n > 59) {
			return 0, fmt.Errorf("part %q", p)
		}
		total += time.Duration(n) * scale[i]
	}
	return total, nil
}
