package detect

import "testing"

func TestMatchesWakePhrase(t *testing.T) {
	for _, tc := range []struct {
		transcript string
		phrase     string
		want       bool
	}{
		{"Alexa.", "alexa", true},
		{"HEY, Jarvis!", "Hey Jarvis", true},
		{"Alexa turn on the lights", "Alexa", false},
		{"Alex", "Alexa", false},
		{"Alexa", "", false},
		{"", "Alexa", false},
	} {
		if got := matchesWakePhrase(tc.transcript, tc.phrase); got != tc.want {
			t.Errorf("matchesWakePhrase(%q, %q) = %t, want %t", tc.transcript, tc.phrase, got, tc.want)
		}
	}
}
