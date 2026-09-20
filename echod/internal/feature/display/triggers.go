//go:build !dot

package display

import "strings"

// What the screen listens for in a turn's transcript, for both screens: the Show's pages and the
// Spot's faces react to the same sentences, so the words live here once rather than in each.
//
// The assistant itself is not involved. Home Assistant answers the question whatever language it is
// in; these decide only whether a page comes up beside the answer, so a word matched here that was
// meant for something else costs a page, not a reply.
//
// Every language is matched at once rather than the pipeline's own. That is a stopgap: the words of
// one language can collide with another's, and the lists grow with each language added. Keyed tables,
// chosen by the pipeline's language, are the way out of it.
var (
	// weatherWords bring up the forecast; radarWords the rain map instead of it. The German stems
	// regn and schnei carry regnet, regnerisch, schneit and schneien.
	weatherWords = []string{
		"weather", "forecast", "temperature", "rain", "snow", "how hot", "how cold", "storm", "radar", "weather map",
		"wetter", "vorhersage", "temperatur", "regen", "regn", "schnee", "schnei", "sturm", "gewitter",
		"wie warm", "wie kalt", "sonnig", "bewölkt",
	}
	radarWords = []string{
		"radar", "rain map", "weather map",
		"regenkarte", "wetterkarte", "niederschlagskarte",
	}

	// goHomeWords take whatever is up back down to the clock.
	goHomeWords = []string{
		"go home", "home screen", "main screen",
		"startbildschirm", "hauptbildschirm", "zurück zur uhr", "zeig die uhr",
	}
)

// containsAny is whether h holds any of the words.
func containsAny(h string, words ...string) bool {
	for _, w := range words {
		if strings.Contains(h, w) {
			return true
		}
	}
	return false
}

// aboutWeather is whether what was heard asked about the weather.
func aboutWeather(heard string) bool { return containsAny(strings.ToLower(heard), weatherWords...) }

// aboutRadar is whether what was heard asked for the rain map rather than the forecast.
func aboutRadar(heard string) bool { return containsAny(strings.ToLower(heard), radarWords...) }

// aboutGoingHome is whether what was heard asked for the clock back.
func aboutGoingHome(heard string) bool { return containsAny(strings.ToLower(heard), goHomeWords...) }
