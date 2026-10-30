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
