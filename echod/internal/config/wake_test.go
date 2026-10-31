package config

import "testing"

// The same wake word in two slots is named, so the device can warn about it; an empty slot repeated is
// not a word (#344).
func TestRepeatedWord(t *testing.T) {
	for _, c := range []struct {
		ids  []string
		want string
	}{
		{nil, ""},
		{[]string{"hey_google"}, ""},
		{[]string{"okay_nabu", "hey_google"}, ""},
		{[]string{"", ""}, ""},
		{[]string{"hey_google", "hey_google"}, "hey_google"},
	} {
		var w Wake
		for _, id := range c.ids {
			w.Words = append(w.Words, WakeWord{ID: id})
		}
		if got := w.RepeatedWord(); got != c.want {
			t.Errorf("%q: got %q, want %q", c.ids, got, c.want)
		}
	}
}

func TestPlayingThresholdFallback(t *testing.T) {
	for _, tc := range []struct{ threshold, want float64 }{
		{0.85, 0.75}, {0.95, 0.85}, {0.52, 0.50},
	} {
		w := WakeWord{Threshold: tc.threshold}
		if got := w.PlayingThresholdAt(); got-tc.want > 1e-9 || tc.want-got > 1e-9 {
			t.Errorf("threshold %v: got %v, want %v", tc.threshold, got, tc.want)
		}
	}
	w := WakeWord{Threshold: 0.85, PlayingThreshold: 0.93}
	if got := w.PlayingThresholdAt(); got != 0.93 {
		t.Errorf("explicit threshold = %v, want 0.93", got)
	}
	w.Threshold = 0.91
	if got := w.PlayingThresholdAt(); got != 0.93 {
		t.Errorf("changing the normal threshold changed the explicit playing threshold to %v", got)
	}
}
