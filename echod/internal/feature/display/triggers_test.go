//go:build !dot

package display

import "testing"

// What the screen reacts to, in each language it knows. These are sentences people actually say, so
// a word taken out of the lists has to fail here before it fails on a device.
func TestTriggers(t *testing.T) {
	cases := []struct {
		heard   string
		weather bool
		radar   bool
		home    bool
	}{
		{heard: "what's the weather", weather: true},
		{heard: "what's the forecast for tomorrow", weather: true},
		{heard: "will it rain today", weather: true},
		{heard: "how hot is it outside", weather: true},
		{heard: "show me the radar", weather: true, radar: true},
		{heard: "show me the rain map", weather: true, radar: true},
		{heard: "go home", home: true},
		{heard: "back to the home screen", home: true},

		{heard: "Wie ist das Wetter?", weather: true},
		{heard: "Wie ist die Vorhersage?", weather: true},
		{heard: "Regnet es morgen?", weather: true},
		{heard: "Schneit es heute?", weather: true},
		{heard: "Wie warm wird es?", weather: true},
		{heard: "Ist es bewölkt?", weather: true},
		{heard: "Zeig mir die Regenkarte", weather: true, radar: true},
		{heard: "Zeig die Uhr", home: true},
		{heard: "Zurück zur Uhr", home: true},

		// Nothing here is about the screen: the assistant answers and the clock stays put.
		{heard: "turn on the kitchen light"},
		{heard: "set a timer for ten minutes"},
		{heard: "mach das Licht an"},
		{heard: "spiel Musik"},
		{heard: "stell einen Timer auf zehn Minuten"},
	}
	for _, c := range cases {
		if got := aboutWeather(c.heard); got != c.weather {
			t.Errorf("aboutWeather(%q) = %v, want %v", c.heard, got, c.weather)
		}
		if got := aboutRadar(c.heard); got != c.radar {
			t.Errorf("aboutRadar(%q) = %v, want %v", c.heard, got, c.radar)
		}
		if got := aboutGoingHome(c.heard); got != c.home {
			t.Errorf("aboutGoingHome(%q) = %v, want %v", c.heard, got, c.home)
		}
	}
}

// The rain map is only reachable by voice through its own words; a plain weather question brings up
// the forecast page instead. This is what was broken in German: no words matched, so neither came up.
func TestRadarNeedsItsOwnWords(t *testing.T) {
	for _, s := range []string{"what's the weather", "Wie ist das Wetter?", "will it rain", "Regnet es?"} {
		if aboutRadar(s) {
			t.Errorf("aboutRadar(%q) = true, want the forecast page instead", s)
		}
		if !aboutWeather(s) {
			t.Errorf("aboutWeather(%q) = false", s)
		}
	}
}
