package triggers

import (
	"strings"
	"testing"
)

// One sentence per language for each thing the screen does, as someone would actually say it.
var spoken = map[string]struct{ weather, radar, home, camera string }{
	"en": {"what's the weather", "show me the rain map", "go home", "show the front door"},
	"de": {"Wie ist das Wetter?", "Zeig mir die Regenkarte", "Zurück zur Uhr", "Zeig die Haustür"},
	"es": {"¿Qué tiempo hace?", "Muéstrame el mapa de lluvia", "Volver al reloj", "Muestra la puerta principal"},
	"fr": {"Quel temps fait-il ?", "Montre la carte des pluies", "Retour à l'horloge", "Montre la porte d'entrée"},
	"it": {"Che tempo fa?", "Mostra la mappa della pioggia", "Torna all'orologio", "Mostra la porta d'ingresso"},
	"nl": {"Hoe is het weer?", "Laat de buienradar zien", "Terug naar de klok", "Laat de voordeur zien"},
}

// Each language's own sentences bring up what they ask for, both when that language is chosen and
// under Match all.
func TestEachLanguage(t *testing.T) {
	for lang, s := range spoken {
		for _, sel := range []string{lang, MatchAll} {
			where := lang + " under " + map[bool]string{true: "match all", false: lang}[sel == MatchAll]
			if !AboutWeather(s.weather, sel) {
				t.Errorf("%s: AboutWeather(%q) = false", where, s.weather)
			}
			if !AboutRadar(s.radar, sel) {
				t.Errorf("%s: AboutRadar(%q) = false", where, s.radar)
			}
			if !AboutGoingHome(s.home, sel) {
				t.Errorf("%s: AboutGoingHome(%q) = false", where, s.home)
			}
			if !AboutCamera(s.camera, sel) {
				t.Errorf("%s: AboutCamera(%q) = false", where, s.camera)
			}
		}
	}
}

// The point of choosing a language: another language's words are not consulted, so they cannot fire
// on a sentence that was not meant for them. Only pairs that genuinely do not share a word are
// checked — "radar" is the same in most of these, which is why it is left out of the weather line.
func TestOneLanguageIgnoresAnother(t *testing.T) {
	for lang, s := range spoken {
		for other := range Languages {
			if other == lang {
				continue
			}
			if AboutGoingHome(s.home, other) && !sharesWord(Languages[lang].GoHome, Languages[other].GoHome) {
				t.Errorf("%q matched going home under %q, which shares no word with %q", s.home, other, lang)
			}
		}
	}
}

func sharesWord(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if strings.Contains(x, y) || strings.Contains(y, x) {
				return true
			}
		}
	}
	return false
}

// The rain map is reached through the weather page, so anything that asks for the radar has to bring
// the weather page up as well, in every language.
func TestRadarImpliesWeather(t *testing.T) {
	for lang, set := range Languages {
		for _, w := range set.Radar {
			if !AboutWeather(w, lang) {
				t.Errorf("%s: %q asks for the radar but not the weather, so the page it lives on never opens", lang, w)
			}
		}
	}
}

// Ordinary commands are the assistant's business; the screen stays where it is.
func TestOrdinaryCommandsLeaveTheScreenAlone(t *testing.T) {
	quiet := []string{
		"turn on the kitchen light", "set a timer for ten minutes", "play some music",
		"mach das Licht an", "stell einen Timer auf zehn Minuten",
		"enciende la luz de la cocina", "pon música",
		"allume la lumière du salon", "mets de la musique",
		"accendi la luce", "metti un timer di dieci minuti",
		"doe het licht aan", "zet een timer van tien minuten",
	}
	for _, s := range quiet {
		if AboutWeather(s, MatchAll) {
			t.Errorf("AboutWeather(%q) = true under match all", s)
		}
		if AboutGoingHome(s, MatchAll) {
			t.Errorf("AboutGoingHome(%q) = true under match all", s)
		}
	}
}

// Asking for rain to fall asleep to is asking for a sound, and the forecast coming up over it is
// wrong; asking whether it will rain still is a weather question.
func TestRainSoundsAreNotTheForecast(t *testing.T) {
	for _, s := range []string{
		"Play the sounds of rain.", "play rain sounds", "put on the rain", "play rain",
		"start the rain noise", "play the sound of a thunderstorm",
	} {
		if AboutWeather(s, "en") || AboutWeather(s, MatchAll) {
			t.Errorf("AboutWeather(%q) = true", s)
		}
	}
	for _, s := range []string{"Is it going to rain today?", "Will it storm tonight?", "What's the forecast?",
		"Should I put on a jacket, is it going to rain?"} {
		if !AboutWeather(s, "en") {
			t.Errorf("AboutWeather(%q) = false", s)
		}
	}
}
