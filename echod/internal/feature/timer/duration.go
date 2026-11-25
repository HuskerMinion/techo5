package timer

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/ring"
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
// selector), "1:30" (hours and minutes), or a bare number, which is minutes; and in words, as said:
// "five minutes", "twenty five minutes", "an hour and a half".
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
		return scaled(n, time.Minute)
	}
	if d, err := time.ParseDuration(strings.ReplaceAll(s, " ", "")); err == nil {
		return d, nil
	}
	if strings.Contains(s, ":") {
		return clockDuration(s)
	}

	var fields []string
	for _, w := range strings.Fields(strings.ReplaceAll(s, ",", " ")) {
		// "twenty-five" is "twenty five"; a minus sign stays, so "-5 minutes" is still refused.
		if a, b, ok := strings.Cut(w, "-"); ok && isWord(a) && isWord(b) {
			fields = append(fields, a, b)
			continue
		}
		fields = append(fields, w)
	}
	return wordsDuration(fields)
}

func isWord(s string) bool {
	return s != "" && strings.IndexFunc(s, func(r rune) bool { return r < 'a' || r > 'z' }) < 0
}

// wordsDuration reads lengths, each a number and its unit, joined by "and" or nothing: "1 hour and 30
// minutes", "five minutes", "twenty five minutes". The number is in digits or in the words a snooze
// takes (ring.NumberAt), since speech to text writes it either way and an automation passes on what was
// heard (#734). Halves are read as said: "half an hour", "a half hour", "an hour and a half", "two and
// a half minutes".
func wordsDuration(f []string) (time.Duration, error) {
	at := func(i int, w ...string) bool {
		if i+len(w) > len(f) {
			return false
		}
		for k, x := range w {
			if f[i+k] != x {
				return false
			}
		}
		return true
	}
	andAHalf := func(i int) bool { return at(i, "and", "a", "half") || at(i, "and", "one", "half") }

	var total time.Duration
	lengths := 0
	for i := 0; i < len(f); {
		if f[i] == "and" && lengths > 0 && i+1 < len(f) {
			i++
			continue
		}
		// "half an hour", "half a minute", and "half hour" as it is said too
		if f[i] == "half" && i+2 < len(f) && (f[i+1] == "a" || f[i+1] == "an") {
			unit, ok := units[f[i+2]]
			if !ok {
				return 0, fmt.Errorf("half of %q", f[i+2])
			}
			total = add(total, unit/2)
			lengths++
			i += 3
			continue
		}
		if f[i] == "half" && i+1 < len(f) {
			if unit, ok := units[f[i+1]]; ok {
				total = add(total, unit/2)
				lengths++
				i += 2
				continue
			}
		}
		var n float64
		j := i + 1
		if v, err := strconv.ParseFloat(f[i], 64); err == nil {
			n = v
		} else if v, next, ok := ring.NumberAt(f, i); ok {
			n, j = float64(v), next
		} else {
			return 0, fmt.Errorf("%q is not a number", f[i])
		}
		switch {
		case (f[i] == "a" || f[i] == "an") && at(j, "half"): // "a half hour"
			n, j = 0.5, j+1
		case andAHalf(j): // "two and a half minutes"
			n, j = n+0.5, j+3
		}
		if j >= len(f) {
			return 0, fmt.Errorf("%v with no unit", n)
		}
		unit, ok := units[f[j]]
		if !ok {
			return 0, fmt.Errorf("%q is not a unit", f[j])
		}
		d, err := scaled(n, unit)
		if err != nil {
			return 0, err
		}
		total = add(total, d)
		lengths++
		j++
		if andAHalf(j) { // "an hour and a half"
			total = add(total, unit/2)
			j += 3
		}
		i = j
	}
	if lengths == 0 {
		return 0, fmt.Errorf("no length")
	}
	return total, nil
}

// tooLong is a length past Longest, which ParseDuration refuses: what a length too long to hold is
// read as, so it is refused as too long rather than wrapping round to a length that fits.
const tooLong = Longest + time.Nanosecond

// scaled is n of unit, no longer than tooLong. A number too large for a Duration ("1e19 seconds")
// would otherwise wrap, and a sum of two such lengths could come back as one that fits (#734). Not a
// number at all ("nan", "inf") is not a length, and nor is one below nothing, which would otherwise
// take away from the lengths beside it: "-5 minutes and 10 minutes" is not five minutes.
func scaled(n float64, unit time.Duration) (time.Duration, error) {
	if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 {
		return 0, fmt.Errorf("%v is not a length", n)
	}
	return time.Duration(min(n*float64(unit), float64(tooLong))), nil
}

// add is total and d, held at tooLong for the same reason.
func add(total, d time.Duration) time.Duration { return min(total+d, tooLong) }

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

// LeftText is a timer's time left as the screen writes it and as Home Assistant is told it: "4:05", or
// "1:02:05" past an hour. It lives here so the two cannot drift — what the sensor says and what the panel
// shows are the same timer said twice, and a second implementation would eventually say it differently.
func LeftText(left time.Duration) string {
	secs := int(left.Round(time.Second).Seconds())
	if secs >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", secs/3600, secs/60%60, secs%60)
	}
	return fmt.Sprintf("%d:%02d", secs/60, secs%60)
}
