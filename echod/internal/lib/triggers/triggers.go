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
	"ja": {
		// Match phrases rather than single kanji: 雨宮 and 雪子 are names, and
		// 雪見だいふく is food. A bare レーダー can be a car's radar as well.
		Weather: []string{"天気", "気温", "降水確率", "雨が降", "雨は降", "雨降", "雪が降", "雪は降", "雪降",
			"雨の予報", "雪の予報", "降雪予報", "雷雨の予報", "雨雲レーダー", "降水レーダー", "雨雲の動き"},
		Radar:      []string{"雨雲レーダー", "降水レーダー", "雨雲の動き"},
		NotWeather: []string{"雨の音", "雨音", "雨の曲", "雨の音楽", "雨が降る音", "天気の子", "を再生", "を流して", "という曲"},
		GoHome:     []string{"ホーム画面に戻", "時計に戻", "時計を表示", "ホームに戻"},
		Camera:     []string{"カメラ", "の映像", "を映して"},
	},
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
		// Matched inside what was heard, and under Match all inside English too: so not "neig"
		// (neighbor) or "orage" alone (storage).
		Weather: []string{"météo", "meteo", "quel temps", "prévisions", "previsions", "température", "temperature",
			"pluie", "pleu", "neige", "neigé", "un orage", "d'orage", "l'orage", "des orages", "orageux", "tempête", "tempete",
			"ensoleillé", "ensoleille", "nuageux", "radar"},
		Radar:  []string{"radar", "carte des pluies", "carte météo", "carte meteo"},
		GoHome: []string{"écran d'accueil", "ecran d'accueil", "page d'accueil", "retour à l'horloge", "retour a l'horloge", "montre l'horloge"},
		Camera: []string{"montre", "affiche", "caméra", "camera"},
	},
	"ca": {
		// Not "neu" or "plou" alone: they are inside "neutral" and "plough", which Match all would take as
		// the weather.
		Weather: []string{"el temps", "quin temps", "previsió", "temperatura", "pluja", "plourà", "plovent",
			"plou avui", "plou demà", "neu avui", "neu demà", "nevarà", "nevant",
			"tempesta", "assolellat", "ennuvolat", "radar"},
		Radar:  []string{"radar", "mapa de pluja", "mapa del temps"},
		GoHome: []string{"pantalla d'inici", "ves a l'inici", "torna al rellotge", "mostra el rellotge"},
		Camera: []string{"mostra", "ensenya", "càmera"},
	},
	"ca-valencia": {
		// Not "neu" or "plou" alone: they are inside "neutral" and "plough", which Match all would take as
		// the weather.
		Weather: []string{"el temps", "quin temps", "previsió", "temperatura", "pluja", "plourà", "plovent",
			"plou hui", "plou demà", "neu hui", "neu demà", "nevarà", "nevant",
			"tempesta", "assolellat", "ennuvolat", "radar"},
		Radar:  []string{"radar", "mapa de pluja", "mapa del temps"},
		GoHome: []string{"pantalla d'inici", "ves a l'inici", "torna al rellotge", "mostra el rellotge"},
		Camera: []string{"mostra", "ensenya", "càmera"},
	},
	"gl": {
		// Not "neve" alone: it is inside the English "never", which Match all would take as the weather.
		Weather: []string{"o tempo", "que tempo", "previsión", "temperatura", "choiva", "chove", "choverá",
			"neve hoxe", "neve mañá", "nevará", "nevando",
			"treboada", "tormenta", "soleado", "anubrado", "radar"},
		Radar:  []string{"radar", "mapa da choiva", "mapa do tempo"},
		GoHome: []string{"pantalla de inicio", "pantalla principal", "volve ao reloxo", "amosa o reloxo"},
		Camera: []string{"amosa", "ensina", "móstrame", "cámara"},
	},
	"it": {
		// Not "neve" alone: it is inside the English "never", which Match all would take as the weather.
		Weather: []string{"meteo", "che tempo", "previsioni", "temperatura", "pioggia", "piov", "la neve", "di neve",
			"c'è neve", "sarà neve", "neve oggi", "neve domani", "nevic",
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
	"pl": {
		// Matched inside what was heard, and under Match all inside every other language too, so each
		// form with diacritics also without them, as speech to text sometimes leaves them off. "pogod"
		// carries pogoda, pogody, pogodę and pogodzie. Not "pada" alone: it is inside "spada" and the
		// Spanish "espada"; not a bare "temperatura", which is a room's thermostat as often as the
		// weather.
		Weather: []string{"pogod", "prognoz", "temperatura na zewnątrz", "temperatura na zewnatrz", "temperatura na dworze",
			"jaka będzie temperatura", "jaka bedzie temperatura", "ile stopni na", "ile będzie stopni", "ile bedzie stopni",
			"deszcz", "czy pada", "będzie padać", "bedzie padac", "śnieg", "snieg", "śnieżn", "burza", "burzy", "burze",
			"ulewa", "ulewy", "opady", "opadów", "opadow", "słonecznie", "slonecznie", "pochmurno", "zachmurzenie",
			"jak ciepło", "jak cieplo", "jak zimno", "radar"},
		Radar: []string{"radar", "mapa opadów", "mapę opadów", "mapa opadow", "mape opadow", "mapa deszczu", "mapę deszczu",
			"mape deszczu", "mapa pogody", "mapę pogody", "mape pogody", "mapa pogodowa", "mapę pogodową", "mape pogodowa"},
		NotWeather: []string{"odgłos", "odglos", "dźwięk", "dzwiek", "szum", "puść deszcz", "pusc deszcz", "włącz deszcz", "wlacz deszcz"},
		GoHome: []string{"ekran główny", "ekran glowny", "ekranu głównego", "ekranu glownego", "ekran startowy", "wróć do zegara",
			"wroc do zegara", "powrót do zegara", "powrot do zegara", "pokaż zegar", "pokaz zegar"},
		Camera: []string{"pokaż", "pokaz", "kamera", "kamerę", "kamere", "kamery", "kamerze", "podgląd", "podglad"},
	},
	"pt": {
		// Matched inside what was heard, so "tempo" alone is left out: it is in "quanto tempo" (how long)
		// and timers. The weather is asked for in phrases instead, and "temporal" too: alone, it is the
		// English "temporal lobe" under Match all. Brazilian Portuguese, as it is spoken.
		Weather: []string{"previsão do tempo", "previsao do tempo", "como está o tempo", "como esta o tempo", "como tá o tempo",
			"como ta o tempo", "como vai estar o tempo", "tempo hoje", "tempo amanhã", "tempo amanha", "clima hoje", "clima amanhã",
			"clima amanha", "mapa do tempo", "qual a temperatura", "qual é a temperatura", "quantos graus", "chuva", "chover",
			"chove", "nevar", "tempestade", "vai fazer calor", "vai fazer frio", "ensolarado", "nublado", "radar",
			"previsão", "previsao", "como vai o tempo", "que tempo faz", "tá frio", "ta frio", "tá calor", "ta calor", "faz frio",
			"fazer sol", "ter temporal", "um temporal", "vem temporal", "temporal hoje", "temporal amanhã", "temporal amanha"},
		Radar: []string{"radar", "mapa de chuva", "mapa da chuva", "mapa do tempo"},
		NotWeather: []string{"som de chuva", "som da chuva", "sons de chuva", "barulho de chuva", "barulho da chuva", "ruído", "ruido",
			"toca chuva", "toca a chuva", "toque chuva", "coloca chuva", "coloca a chuva", "bota chuva", "bota a chuva",
			"põe chuva", "poe chuva", "põe a chuva", "poe a chuva"},
		GoHome: []string{"tela inicial", "tela principal", "volta pro relógio", "volta pro relogio", "volta para o relógio",
			"volta para o relogio", "voltar ao relógio", "voltar ao relogio", "mostra o relógio", "mostra o relogio",
			"volta pro início", "volta pro inicio", "volta para o início", "volta para o inicio", "tela de início", "tela de inicio"},
		Camera: []string{"mostra", "mostre", "mostrar", "câmera", "camera"},
	},
	"ro": {
		// Each word with diacritics also without them: speech to text often leaves them off. Not "vreme"
		// alone: it is inside "devreme" (early), said to alarms, and "în vreme ce". Not "ninge" alone: it
		// is inside Dutch plurals (rekeningen, woningen), which Match all would take as the weather. Not
		// "cate grade" alone: it is inside the English "duplicate grade", as "temperatur" is inside
		// "temperature"; "temperatura" is not.
		Weather: []string{"vremea", "ce vreme", "vremii", "meteo", "prognoz", "temperatura", "temperatură",
			"câte grade", "cate grade sunt", "cate grade afara", "cate grade afară",
			"ploaie", "plouă", "ploua", "harta ploilor", "va ninge", "ninge azi", "ninge mâine", "ninge maine",
			"ninge afară", "ninge afara", "o să ningă", "o sa ninga", "ninsoare", "zăpadă", "zapada",
			"furtun", "însorit", "insorit", "înnorat", "innorat", "radar"},
		Radar:      []string{"radar", "harta ploilor", "harta meteo", "harta vremii"},
		NotWeather: []string{"sunet de ploaie", "sunete de ploaie", "sunetul ploii", "zgomot de ploaie", "pune ploaie"},
		GoHome:     []string{"înapoi la ceas", "inapoi la ceas", "arată ceasul", "arata ceasul", "ecranul principal"},
		Camera:     []string{"arată", "arata", "afișează", "afiseaza", "camera"},
	},
	"ru": {
		// Matched inside what was heard, so each form is listed where the stem is inside another word:
		// "погод" is in "погоди" (wait), "облачн" in cloud storage. Thunderstorm is left out: every
		// form of "гроза" is inside one of "угроза" (threat).
		Weather: []string{"погода", "погоду", "погоде", "погоды", "погодой", "погодка", "погодку", "погодке", "погодкой", "погодн", "прогноз", "температур", "дожд", "снег", "снеж",
			"шторм", "буря", "бури", "бурю", "ураган", "осадк", "сколько градус", "радар"},
		Radar:      []string{"радар", "карта дождя", "карту дождя", "карта осадков", "карту осадков", "карта погоды", "карту погоды"},
		NotWeather: []string{"звук", "шум", "включи дождь"},
		GoHome:     []string{"главный экран", "главном экран", "вернись на главную", "на главную страниц", "домашний экран", "домашнем экран", "верни часы", "вернись к часам", "вернись домой"},
		Camera:     []string{"покажи", "показать", "камер"},
	},
	"tr": {
		Weather: []string{"hava durumu", "hava nasıl", "sıcaklık", "yağmur", "yağış", "kar yağ", "kar var",
			"fırtına", "güneşli", "bulutlu", "kaç derece", "radar", "hava tahmini", "hava haritası"},
		Radar:      []string{"radar", "yağmur haritası", "hava haritası"},
		NotWeather: []string{"yağmur sesi", "yağmur müziği", "yağmur çal"},
		GoHome:     []string{"ana ekran", "ana sayfa", "saate dön", "saati göster", "ana ekrana dön", "geri dön"},
		Camera:     []string{"göster", "kamera"},
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
