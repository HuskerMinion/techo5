// Package locale writes the dates and weather words the screen shows in the screen's language: the
// clock's day and date, the forecast's day names, and what the sky is doing. Six languages, the ones
// the screen's Language setting offers; anything else, empty included, is English.
//
// Only those words. The rest of the screen's own text - its settings, its buttons - is English.
package locale

import (
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// words is one language's names: days from Sunday, as time.Weekday counts, and months from January.
type words struct {
	days, shortDays     [7]string
	months, shortMonths [12]string
	sky                 map[string]string
}

var langs = map[string]*words{
	"de": {
		days:        [7]string{"Sonntag", "Montag", "Dienstag", "Mittwoch", "Donnerstag", "Freitag", "Samstag"},
		shortDays:   [7]string{"So", "Mo", "Di", "Mi", "Do", "Fr", "Sa"},
		months:      [12]string{"Januar", "Februar", "März", "April", "Mai", "Juni", "Juli", "August", "September", "Oktober", "November", "Dezember"},
		shortMonths: [12]string{"Jan.", "Feb.", "März", "Apr.", "Mai", "Juni", "Juli", "Aug.", "Sep.", "Okt.", "Nov.", "Dez."},
		sky: map[string]string{
			"clear-night": "Klar", "cloudy": "Bewölkt", "exceptional": "Unwetter", "fog": "Nebel", "hail": "Hagel",
			"lightning": "Gewitter", "lightning-rainy": "Gewitter", "partlycloudy": "Teilweise bewölkt",
			"pouring": "Starkregen", "rainy": "Regen", "snowy": "Schnee", "snowy-rainy": "Schneeregen",
			"sunny": "Sonnig", "windy": "Windig", "windy-variant": "Windig",
		},
	},
	"es": {
		days:        [7]string{"domingo", "lunes", "martes", "miércoles", "jueves", "viernes", "sábado"},
		shortDays:   [7]string{"dom", "lun", "mar", "mié", "jue", "vie", "sáb"},
		months:      [12]string{"enero", "febrero", "marzo", "abril", "mayo", "junio", "julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre"},
		shortMonths: [12]string{"ene", "feb", "mar", "abr", "may", "jun", "jul", "ago", "sept", "oct", "nov", "dic"},
		sky: map[string]string{
			"clear-night": "Despejado", "cloudy": "Nublado", "exceptional": "Extremo", "fog": "Niebla", "hail": "Granizo",
			"lightning": "Relámpagos", "lightning-rainy": "Tormentas", "partlycloudy": "Parcialmente nublado",
			"pouring": "Lluvia intensa", "rainy": "Lluvia", "snowy": "Nieve", "snowy-rainy": "Aguanieve",
			"sunny": "Soleado", "windy": "Ventoso", "windy-variant": "Ventoso",
		},
	},
	"fr": {
		days:        [7]string{"dimanche", "lundi", "mardi", "mercredi", "jeudi", "vendredi", "samedi"},
		shortDays:   [7]string{"dim.", "lun.", "mar.", "mer.", "jeu.", "ven.", "sam."},
		months:      [12]string{"janvier", "février", "mars", "avril", "mai", "juin", "juillet", "août", "septembre", "octobre", "novembre", "décembre"},
		shortMonths: [12]string{"janv.", "févr.", "mars", "avr.", "mai", "juin", "juil.", "août", "sept.", "oct.", "nov.", "déc."},
		sky: map[string]string{
			"clear-night": "Dégagé", "cloudy": "Nuageux", "exceptional": "Extrême", "fog": "Brouillard", "hail": "Grêle",
			"lightning": "Éclairs", "lightning-rainy": "Orages", "partlycloudy": "Partiellement nuageux",
			"pouring": "Pluie forte", "rainy": "Pluie", "snowy": "Neige", "snowy-rainy": "Neige fondue",
			"sunny": "Ensoleillé", "windy": "Venteux", "windy-variant": "Venteux",
		},
	},
	"it": {
		days:        [7]string{"domenica", "lunedì", "martedì", "mercoledì", "giovedì", "venerdì", "sabato"},
		shortDays:   [7]string{"dom", "lun", "mar", "mer", "gio", "ven", "sab"},
		months:      [12]string{"gennaio", "febbraio", "marzo", "aprile", "maggio", "giugno", "luglio", "agosto", "settembre", "ottobre", "novembre", "dicembre"},
		shortMonths: [12]string{"gen", "feb", "mar", "apr", "mag", "giu", "lug", "ago", "set", "ott", "nov", "dic"},
		sky: map[string]string{
			"clear-night": "Sereno", "cloudy": "Nuvoloso", "exceptional": "Estremo", "fog": "Nebbia", "hail": "Grandine",
			"lightning": "Fulmini", "lightning-rainy": "Temporali", "partlycloudy": "Parzialmente nuvoloso",
			"pouring": "Pioggia forte", "rainy": "Pioggia", "snowy": "Neve", "snowy-rainy": "Nevischio",
			"sunny": "Soleggiato", "windy": "Ventoso", "windy-variant": "Ventoso",
		},
	},
	"nl": {
		days:        [7]string{"zondag", "maandag", "dinsdag", "woensdag", "donderdag", "vrijdag", "zaterdag"},
		shortDays:   [7]string{"zo", "ma", "di", "wo", "do", "vr", "za"},
		months:      [12]string{"januari", "februari", "maart", "april", "mei", "juni", "juli", "augustus", "september", "oktober", "november", "december"},
		shortMonths: [12]string{"jan", "feb", "mrt", "apr", "mei", "jun", "jul", "aug", "sep", "okt", "nov", "dec"},
		sky: map[string]string{
			"clear-night": "Helder", "cloudy": "Bewolkt", "exceptional": "Extreem", "fog": "Mist", "hail": "Hagel",
			"lightning": "Bliksem", "lightning-rainy": "Onweer", "partlycloudy": "Half bewolkt",
			"pouring": "Stortregen", "rainy": "Regen", "snowy": "Sneeuw", "snowy-rainy": "Natte sneeuw",
			"sunny": "Zonnig", "windy": "Winderig", "windy-variant": "Winderig",
		},
	},
}

// phrases are the few words written beside a date: today, as the forecast heads its first day, and
// the next alarm after the clock's date.
//
// And the words of the clock styles that list a day: the Agenda's (today, tomorrow, an event under way
// or all day, a calendar with nothing on it) and the World clock's (a place a day ahead or behind).
var phrases = map[string]map[string]string{
	"de": {"here": "Hier", "today": "Heute", "alarm": "Wecker", "snoozed": "Schlummern bis", "tomorrow": "Morgen", "yesterday": "Gestern",
		"now": "Jetzt", "allday": "Ganztägig", "nothing": "Keine Termine", "nothingtoday": "Heute nichts",
		"nothingelse": "Heute nichts mehr"},
	"es": {"here": "Aquí", "today": "Hoy", "alarm": "Alarma", "snoozed": "Pospuesta hasta", "tomorrow": "Mañana", "yesterday": "Ayer",
		"now": "Ahora", "allday": "Todo el día", "nothing": "Nada en el calendario", "nothingtoday": "Nada hoy",
		"nothingelse": "Nada más hoy"},
	"fr": {"here": "Ici", "today": "Aujourd'hui", "alarm": "Réveil", "snoozed": "Reporté à", "tomorrow": "Demain", "yesterday": "Hier",
		"now": "Maintenant", "allday": "Toute la journée", "nothing": "Rien au calendrier",
		"nothingtoday": "Rien aujourd'hui", "nothingelse": "Plus rien aujourd'hui"},
	"it": {"here": "Qui", "today": "Oggi", "alarm": "Sveglia", "snoozed": "Posticipata alle", "tomorrow": "Domani", "yesterday": "Ieri",
		"now": "Ora", "allday": "Tutto il giorno", "nothing": "Niente in calendario", "nothingtoday": "Niente oggi",
		"nothingelse": "Nient'altro oggi"},
	"nl": {"here": "Hier", "today": "Vandaag", "alarm": "Wekker", "snoozed": "Sluimeren tot", "tomorrow": "Morgen", "yesterday": "Gisteren",
		"now": "Nu", "allday": "Hele dag", "nothing": "Niets in de agenda", "nothingtoday": "Niets vandaag",
		"nothingelse": "Verder niets vandaag"},
}

// Here is the World clock's own place.
func Here(lang string) string { return phrase(lang, "here", "Here") }

// Tomorrow and Yesterday are the days either side of today, as a list or a place's clock names them.
func Tomorrow(lang string) string  { return phrase(lang, "tomorrow", "Tomorrow") }
func Yesterday(lang string) string { return phrase(lang, "yesterday", "Yesterday") }

// Now is an event under way; AllDay one that has no time.
func Now(lang string) string    { return phrase(lang, "now", "Now") }
func AllDay(lang string) string { return phrase(lang, "allday", "All day") }

// NothingOn is a calendar with nothing coming; NothingToday a day with nothing on it, and NothingElse
// one whose events are over.
func NothingOn(lang string) string    { return phrase(lang, "nothing", "Nothing on the calendar") }
func NothingToday(lang string) string { return phrase(lang, "nothingtoday", "Nothing today") }
func NothingElse(lang string) string  { return phrase(lang, "nothingelse", "Nothing else today") }

// Today is "Today", as the forecast heads its first day.
func Today(lang string) string { return phrase(lang, "today", "Today") }

// Alarm is "Alarm", and snoozed "Snoozed until", before the next alarm's time after the clock's date.
func Alarm(lang string, snoozed bool) string {
	if snoozed {
		return phrase(lang, "snoozed", "Snoozed until")
	}
	return phrase(lang, "alarm", "Alarm")
}

func phrase(lang, key, english string) string {
	if s, ok := phrases[lang][key]; ok {
		return s
	}
	return english
}

// english is the condition words where Home Assistant's own word will not do as it is.
var english = map[string]string{
	"clear-night": "Clear", "partlycloudy": "Partly cloudy", "lightning-rainy": "Thunderstorms",
	"snowy-rainy": "Sleet", "exceptional": "Severe", "windy-variant": "Windy",
}

// up gives a line its capital, as the start of a line on a screen has one in every language here.
func up(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if n == 0 {
		return s
	}
	return strings.ToUpper(string(r)) + s[n:]
}

func day(t time.Time) string { return strconv.Itoa(t.Day()) }

// LongDate is the clock's date: "Monday, January 2", "Montag, 2. Januar", "Lunes, 2 de enero",
// "Lundi 2 janvier", "Lunedì 2 gennaio", "Maandag 2 januari".
func LongDate(t time.Time, lang string) string {
	w, ok := langs[lang]
	if !ok {
		return t.Format("Monday, January 2")
	}
	d, m := w.days[t.Weekday()], w.months[t.Month()-1]
	switch lang {
	case "de":
		return up(d) + ", " + day(t) + ". " + m
	case "es":
		return up(d) + ", " + day(t) + " de " + m
	}
	return up(d) + " " + day(t) + " " + m
}

// ShortDate is the date where there is less room: "Mon, Jan 2", "Mo., 2. Jan.", "Lun, 2 ene".
func ShortDate(t time.Time, lang string) string {
	w, ok := langs[lang]
	if !ok {
		return t.Format("Mon, Jan 2")
	}
	d, m := w.shortDays[t.Weekday()], w.shortMonths[t.Month()-1]
	switch lang {
	case "de":
		return up(d) + "., " + day(t) + ". " + m
	case "es":
		return up(d) + ", " + day(t) + " " + m
	}
	return up(d) + " " + day(t) + " " + m
}

// Weekday is the day's name on its own: "Monday", "Montag", "Lunes".
func Weekday(t time.Time, lang string) string {
	if w, ok := langs[lang]; ok {
		return up(w.days[t.Weekday()])
	}
	return t.Format("Monday")
}

// ShortWeekday is the day's name where a forecast has a column for each: "Mon", "Mo", "Lun".
func ShortWeekday(t time.Time, lang string) string {
	if w, ok := langs[lang]; ok {
		return up(w.shortDays[t.Weekday()])
	}
	return t.Format("Mon")
}

// MonthDay is the date without the day's name: "January 2", "2. Januar", "2 de enero".
func MonthDay(t time.Time, lang string) string {
	w, ok := langs[lang]
	if !ok {
		return t.Format("January 2")
	}
	m := w.months[t.Month()-1]
	switch lang {
	case "de":
		return day(t) + ". " + m
	case "es":
		return day(t) + " de " + m
	}
	return day(t) + " " + m
}

// DayAndNumber is the shortest date, a day's name and its number: "Mon 2", "Mo 2", "Lun 2".
func DayAndNumber(t time.Time, lang string) string {
	return ShortWeekday(t, lang) + " " + day(t)
}

// Sky is Home Assistant's weather condition in words: "partlycloudy" is "Partly cloudy", or
// "Parzialmente nuvoloso". Nothing for no reading, and Home Assistant's own word, capitalized, for one
// these do not name.
func Sky(cond, lang string) string {
	switch cond {
	case "", "unknown", "unavailable":
		return ""
	case "partlycloudy-night":
		// The device's own night form of partly cloudy (home.PartlyCloudyNight): a moon behind the
		// cloud rather than a sun, and the same words.
		cond = "partlycloudy"
	}
	if w, ok := langs[lang]; ok {
		if s, ok := w.sky[cond]; ok {
			return s
		}
	}
	if s, ok := english[cond]; ok {
		return s
	}
	// "sunny", "cloudy", "rainy", "pouring", "fog", "hail", "snowy", "windy", "lightning"…
	return up(cond)
}
