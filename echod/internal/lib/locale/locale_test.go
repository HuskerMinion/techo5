package locale

import (
	"testing"
	"time"
)

// A Monday in January, in every language, and English for anything else.
func TestDates(t *testing.T) {
	mon := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)
	for _, c := range []struct{ lang, long, short, month, day string }{
		{"", "Monday, January 5", "Mon, Jan 5", "January 5", "Mon 5"},
		{"en", "Monday, January 5", "Mon, Jan 5", "January 5", "Mon 5"},
		{"xx", "Monday, January 5", "Mon, Jan 5", "January 5", "Mon 5"},
		{"de", "Montag, 5. Januar", "Mo., 5. Jan.", "5. Januar", "Mo 5"},
		{"es", "Lunes, 5 de enero", "Lun, 5 ene", "5 de enero", "Lun 5"},
		{"fr", "Lundi 5 janvier", "Lun. 5 janv.", "5 janvier", "Lun. 5"},
		{"it", "Lunedì 5 gennaio", "Lun 5 gen", "5 gennaio", "Lun 5"},
		{"nl", "Maandag 5 januari", "Ma 5 jan", "5 januari", "Ma 5"},
		{"pt", "Segunda-feira, 5 de janeiro", "Seg, 5 jan", "5 de janeiro", "Seg 5"},
		{"ru", "Понедельник, 5 января", "Пн, 5 янв", "5 января", "Пн 5"},
	} {
		if got := LongDate(mon, c.lang); got != c.long {
			t.Errorf("%q long: %q, want %q", c.lang, got, c.long)
		}
		if got := ShortDate(mon, c.lang); got != c.short {
			t.Errorf("%q short: %q, want %q", c.lang, got, c.short)
		}
		if got := MonthDay(mon, c.lang); got != c.month {
			t.Errorf("%q month and day: %q, want %q", c.lang, got, c.month)
		}
		if got := DayAndNumber(mon, c.lang); got != c.day {
			t.Errorf("%q day and number: %q, want %q", c.lang, got, c.day)
		}
	}
	if Today("") != "Today" || Today("nl") != "Vandaag" || Alarm("de", false) != "Wecker" || Alarm("", true) != "Snoozed until" {
		t.Errorf("phrases: %q %q %q %q", Today(""), Today("nl"), Alarm("de", false), Alarm("", true))
	}
	if got := Weekday(mon.AddDate(0, 0, 2), "it"); got != "Mercoledì" {
		t.Errorf("Italian Wednesday: %q", got)
	}
}

// Every condition Home Assistant has, in every language, is words; English keeps the names it always
// had, and a condition nobody named is Home Assistant's own word, capitalized.
func TestSky(t *testing.T) {
	conds := []string{"clear-night", "cloudy", "exceptional", "fog", "hail", "lightning", "lightning-rainy",
		"partlycloudy", "pouring", "rainy", "snowy", "snowy-rainy", "sunny", "windy", "windy-variant"}
	for lang, w := range langs {
		for _, c := range conds {
			if w.sky[c] == "" {
				t.Errorf("%s has no word for %s", lang, c)
			}
		}
	}
	for c, want := range map[string]string{"partlycloudy": "Partly cloudy", "clear-night": "Clear", "sunny": "Sunny",
		"lightning-rainy": "Thunderstorms", "": "", "unavailable": ""} {
		if got := Sky(c, ""); got != want {
			t.Errorf("%q: %q, want %q", c, got, want)
		}
	}
	if got := Sky("partlycloudy", "it"); got != "Parzialmente nuvoloso" {
		t.Errorf("Italian partly cloudy: %q", got)
	}
	if got := Sky("tornado", "de"); got != "Tornado" {
		t.Errorf("an unnamed condition: %q", got)
	}
}

// The clock styles' day words in each screen language, English for one with none.
func TestTheClockStylesWords(t *testing.T) {
	for lang, want := range map[string][3]string{
		"": {"Tomorrow", "All day", "Nothing on the calendar"}, "de": {"Morgen", "Ganztägig", "Keine Termine"},
		"es": {"Mañana", "Todo el día", "Nada en el calendario"}, "fr": {"Demain", "Toute la journée", "Rien au calendrier"},
		"it": {"Domani", "Tutto il giorno", "Niente in calendario"}, "nl": {"Morgen", "Hele dag", "Niets in de agenda"},
		"pt": {"Amanhã", "Dia inteiro", "Nada na agenda"},
		"ru": {"Завтра", "Весь день", "В календаре пусто"},
		"xx": {"Tomorrow", "All day", "Nothing on the calendar"},
	} {
		if got := [3]string{Tomorrow(lang), AllDay(lang), NothingOn(lang)}; got != want {
			t.Errorf("%q: %q, want %q", lang, got, want)
		}
		for _, w := range []string{Here(lang), Yesterday(lang), Now(lang), NothingToday(lang), NothingElse(lang)} {
			if w == "" {
				t.Errorf("%q: an empty word", lang)
			}
		}
	}
}

// The Dashboard's heading and its empty list in every screen language, never left in English.
func TestTheDashboardWords(t *testing.T) {
	for lang := range langs {
		if Upcoming(lang) == Upcoming("") || NothingSoon(lang) == NothingSoon("") {
			t.Errorf("%s: the Dashboard's words are in English: %q, %q", lang, Upcoming(lang), NothingSoon(lang))
		}
	}
	if got := [2]string{Upcoming("xx"), NothingSoon("xx")}; got != [2]string{"NEXT", "Nothing coming up"} {
		t.Errorf("a language with none: %q", got)
	}
}

func TestPhotoDate(t *testing.T) {
	d := time.Date(2019, 1, 2, 12, 0, 0, 0, time.UTC)
	for lang, want := range map[string]string{
		"en": "January 2, 2019",
		"":   "January 2, 2019",
		"de": "2. Januar 2019",
		"es": "2 de enero de 2019",
	} {
		if got := PhotoDate(d, lang); got != want {
			t.Errorf("PhotoDate(%q) = %q, want %q", lang, got, want)
		}
	}
}
