package ring

import (
	"strings"
	"testing"
)

func TestSnoozeAsked(t *testing.T) {
	for _, c := range []struct {
		text    string
		minutes int
		ok      bool
	}{
		{"Snooze.", 0, true},
		{"snooze the alarm", 0, true},
		{"Snooze it, please.", 0, true},
		{"Snooze for ten minutes.", 10, true},
		{"snooze for 10 minutes", 10, true},
		{"Snooze the alarm for fifteen minutes", 15, true},
		{"snooze another 5 minutes", 5, true},
		{"Snooze for twenty five minutes.", 25, true},
		{"snooze for twenty-five minutes", 25, true},
		{"snooze for a minute", 1, true},
		{"snooze for half an hour", 30, true},
		{"give me ten more minutes, snooze", 10, true},
		{"snooze for half hour", 30, true},
		{"snooze for seventy five minutes", 75, true},
		{"snooze for ninety minutes", 90, true},

		// Not a snooze, or not one this reads: left to Home Assistant.
		{"", 0, false},
		{"ten minutes", 0, false},
		{"set a timer for ten minutes", 0, false},
		{"snooze my phone", 0, false},
		{"snooze for ten", 0, false},
		{"snooze for ten minutes and five minutes", 0, false},
		{"snooze for an hour", 0, false},
		{"stop the alarm", 0, false},
		{"snooze for half hour and ten minutes", 0, false},
	} {
		minutes, ok := SnoozeAsked(c.text)
		if minutes != c.minutes || ok != c.ok {
			t.Errorf("SnoozeAsked(%q) = %d, %v; want %d, %v", c.text, minutes, ok, c.minutes, c.ok)
		}
	}
}

// The numbers past sixty, and the hundreds, read as said (#734); and where a number ends.
func TestNumberAt(t *testing.T) {
	for _, c := range []struct {
		text    string
		n, next int
	}{
		{"seventy", 70, 1},
		{"eighty five minutes", 85, 2},
		{"ninety nine", 99, 2},
		{"a hundred minutes", 100, 2},
		{"one hundred twenty minutes", 120, 3},
		{"one hundred and twenty minutes", 120, 4},
		{"two hundred and five", 205, 4},
		{"a hundred and a half", 100, 2},
		{"one hundred two hundred", 100, 2},
		{"sixty a", 60, 1},
		{"25 minutes", 25, 1},
	} {
		words := strings.Fields(c.text)
		n, next, ok := NumberAt(words, 0)
		if !ok || n != c.n || next != c.next {
			t.Errorf("NumberAt(%q) = %d, %d, %v; want %d, %d", c.text, n, next, ok, c.n, c.next)
		}
	}
	if _, _, ok := NumberAt([]string{"hundred"}, 0); ok {
		t.Error(`"hundred" alone was taken for a number`)
	}
}
