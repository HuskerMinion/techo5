// Package triggers is what the screen listens for in a turn's transcript.
//
// The assistant itself is not involved: Home Assistant answers the question whatever language it is
// asked in. These words decide only whether a page comes up beside the answer — the forecast, the
// rain map, a camera, or the clock again — so a word matched here that was meant for something else
// costs a page, not a reply.
//
// One table per language, because matching every language at once means one language's ordinary word
// is another's trigger. Which table is used comes from the Screen language setting; unset, all of
// them are, which is how this behaved before the tables existed and is still the safe default for a
// household that speaks more than one.
//
// The device is never told which pipeline language it is on — the ESPHome API carries no such field —
// so this has to be set rather than followed.
package triggers

import "strings"

// A Set is one language's words. Stems are used where a language inflects: the German "regn" carries
// regnet, regnerisch and regnen; the Spanish "llov" carries llueve, lloviendo and lloverá.
type Set struct {
	// Weather brings up the forecast, Radar the rain map instead of it (Radar's words must also be
	// in Weather, since the rain map is reached through the weather page).
	Weather []string
	Radar   []string

	// NotWeather is words that make a sentence with a weather word in it something else: "play the
	// sounds of rain" is asking for a sound, not the forecast.
	NotWeather []string

	// GoHome takes whatever is up back down to the clock. Camera is the words that make a sentence
	// worth matching against the camera names, which are in the owner's own language already.
	GoHome []string
	Camera []string
}

// Languages are the tables, by the language tag Home Assistant uses. English and German have been
// used on real devices; the rest are the common words for these few things and are not yet confirmed
// by a native speaker on hardware. A word that turns out to be wrong makes a page not appear, which
// is what happens today anyway, so they are worth shipping and worth correcting.
var Languages = map[string]Set{
	"en": {
		Weather:    []string{"weather", "forecast", "temperature", "rain", "snow", "how hot", "how cold", "storm", "radar", "weather map"},
		Radar:      []string{"radar", "rain map", "weather map"},
		NotWeather: []string{"sound", "noise", "play rain", "play the rain", "put on rain", "put on the rain", "put on some rain"},
		GoHome:     []string{"go home", "home screen", "main screen"},
		Camera:     []string{"show", "camera"},
	},
	"de": {
		Weather: []string{"wetter", "vorhersage", "temperatur", "regen", "regn", "schnee", "schnei", "sturm", "gewitter",
			"wie warm", "wie kalt", "sonnig", "bewölkt", "niederschlag", "radar"},
		Radar:  []string{"radar", "regenkarte", "wetterkarte", "niederschlagskarte"},
		GoHome: []string{"startbildschirm", "hauptbildschirm", "zurück zur uhr", "zeig die uhr"},
		Camera: []string{"zeig", "kamera"},
	},
	"es": {
		Weather: []string{"tiempo", "clima", "pronóstico", "pronostico", "temperatura", "lluvia", "llov", "nieve", "niev",
			"tormenta", "qué calor", "que calor", "qué frío", "que frio", "soleado", "nublado", "radar"},
		Radar:  []string{"radar", "mapa de lluvia", "mapa del tiempo"},
		GoHome: []string{"pantalla de inicio", "pantalla principal", "volver al reloj", "muestra el reloj"},
		Camera: []string{"muestra", "muéstrame", "muestrame", "enseña", "cámara", "camara"},
	},
	"fr": {
		Weather: []string{"météo", "meteo", "quel temps", "prévisions", "previsions", "température", "temperature",
			"pluie", "pleu", "neige", "neig", "orage", "tempête", "tempete", "ensoleillé", "ensoleille", "nuageux", "radar"},
		Radar:  []string{"radar", "carte des pluies", "carte météo", "carte meteo"},
		GoHome: []string{"écran d'accueil", "ecran d'accueil", "page d'accueil", "retour à l'horloge", "retour a l'horloge", "montre l'horloge"},
		Camera: []string{"montre", "affiche", "caméra", "camera"},
	},
	"it": {
		Weather: []string{"meteo", "che tempo", "previsioni", "temperatura", "pioggia", "piov", "neve", "nevic",
			"temporale", "tempesta", "soleggiato", "nuvoloso", "radar"},
		Radar:  []string{"radar", "mappa della pioggia", "mappa meteo"},
		GoHome: []string{"schermata principale", "schermata iniziale", "torna all'orologio", "mostra l'orologio"},
		Camera: []string{"mostra", "fammi vedere", "telecamera", "videocamera"},
	},
	"nl": {
		Weather: []string{"weer", "weersverwachting", "verwachting", "temperatuur", "regen", "regent", "sneeuw", "sneeuwt",
			"storm", "onweer", "zonnig", "bewolkt", "radar"},
		Radar:  []string{"radar", "regenkaart", "buienradar", "weerkaart"},
		GoHome: []string{"beginscherm", "startscherm", "terug naar de klok", "laat de klok zien"},
		Camera: []string{"laat", "toon", "camera"},
	},
}

// MatchAll is the Language setting that consults every table, as this behaved before the tables
// existed: right for a household that speaks more than one, at the cost of one language's ordinary
// word now and then being another's trigger.
const MatchAll = ""

// sets are the tables the setting selects: the one named, or all of them.
func sets(lang string) []Set {
	if s, ok := Languages[lang]; ok {
		return []Set{s}
	}
	return allSets()
}

func allSets() []Set {
	out := make([]Set, 0, len(Languages))
	for _, s := range Languages {
		out = append(out, s)
	}
	return out
}

func matches(heard string, lang string, pick func(Set) []string) bool {
	h := strings.ToLower(heard)
	for _, s := range sets(lang) {
		for _, w := range pick(s) {
			if strings.Contains(h, w) {
				return true
			}
		}
	}
	return false
}

// AboutWeather is whether what was heard asked about the weather; AboutRadar whether it asked for the
// rain map rather than the forecast; AboutGoingHome whether it asked for the clock back; AboutCamera
// whether it is worth matching against the camera names at all.
func AboutWeather(heard, lang string) bool {
	return matches(heard, lang, func(s Set) []string { return s.Weather }) &&
		!matches(heard, lang, func(s Set) []string { return s.NotWeather })
}

func AboutRadar(heard, lang string) bool {
	return matches(heard, lang, func(s Set) []string { return s.Radar })
}

func AboutGoingHome(heard, lang string) bool {
	return matches(heard, lang, func(s Set) []string { return s.GoHome })
}

func AboutCamera(heard, lang string) bool {
	return matches(heard, lang, func(s Set) []string { return s.Camera })
}
