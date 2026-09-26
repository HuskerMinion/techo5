package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// A daily window of time, such as the screen's night hours or the speaker's quiet hours: from a time
// to a time, which may cross midnight. Written "22-7" for whole hours - the shape every release has
// stored - or "19:00-09:30" when either end is not on the hour. Empty, or anything else that does not
// read, is no window.

// ParseWindow is a window's two ends in minutes since midnight.
func ParseWindow(v string) (from, to int, ok bool) {
	a, b, found := strings.Cut(strings.TrimSpace(v), "-")
	if !found {
		return 0, 0, false
	}
	from, okFrom := windowEnd(a)
	to, okTo := windowEnd(b)
	if !okFrom || !okTo || from == to {
		return 0, 0, false
	}
	return from, to, true
}

// windowEnd is one end, "7", "07", "7:30" or "19:45", in minutes since midnight.
func windowEnd(s string) (int, bool) {
	h, m, hasMinutes := strings.Cut(strings.TrimSpace(s), ":")
	hour, err := strconv.Atoi(h)
	if err != nil || hour < 0 || hour > 23 {
		return 0, false
	}
	minute := 0
	if hasMinutes {
		if len(m) != 2 {
			return 0, false
		}
		if minute, err = strconv.Atoi(m); err != nil || minute < 0 || minute > 59 {
			return 0, false
		}
	}
	return hour*60 + minute, true
}

// FormatWindow writes a window: whole hours the way they have always been written, so a preset still
// reads as itself, and "HH:MM-HH:MM" otherwise.
func FormatWindow(from, to int) string {
	if from%60 == 0 && to%60 == 0 {
		return fmt.Sprintf("%d-%d", from/60, to/60)
	}
	return fmt.Sprintf("%02d:%02d-%02d:%02d", from/60, from%60, to/60, to%60)
}

// InWindow is whether now falls inside the window.
func InWindow(v string, now time.Time) bool {
	from, to, ok := ParseWindow(v)
	if !ok {
		return false
	}
	m := now.Hour()*60 + now.Minute()
	if from < to {
		return m >= from && m < to
	}
	return m >= from || m < to
}
