package voice

import (
	"strings"
	"unicode"
)

// stopWords are the words a request to stop a ring is made of, and nothing else: "stop", "stop the
// alarm", "turn the timer off please". A sentence with any other word in it is something else and is
// left to Home Assistant - "stop the music" is not about the alarm.
var stopWords = map[string]bool{
	"stop": true, "turn": true, "off": true, "it": true, "that": true, "the": true, "alarm": true,
	"timer": true, "ringing": true, "please": true, "ok": true, "okay": true, "now": true, "shut": true,
	"up": true,
}

// stopsRing reports whether a transcript asks for the ring to stop: only the words above, and either
// "stop" or "off" among them.
func stopsRing(text string) bool {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && r != '\''
	})
	asks := false
	for _, w := range words {
		if !stopWords[w] {
			return false
		}
		if w == "stop" || w == "off" {
			asks = true
		}
	}
	return asks
}
