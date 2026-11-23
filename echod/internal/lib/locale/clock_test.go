package locale

import (
	"strings"
	"testing"
	"time"
)

// said is a SaidTime as one line, its empty parts left out, in the order the language says them.
func said(s SaidTime) string {
	parts := []string{s.Intro, s.Count, s.Link, s.Hour, s.Period}
	if s.HourFirst {
		parts = []string{s.Intro, s.Hour, s.Count, s.Link, s.Period}
	}
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " ")
}

func checkSaid(t *testing.T, lang string, cases []struct {
	h, m int
	want string
}) {
	t.Helper()
	for _, c := range cases {
		if got := said(SayTime(time.Date(2026, 9, 16, c.h, c.m, 0, 0, time.Local), lang)); got != c.want {
			t.Errorf("%q %02d:%02d = %q, want %q", lang, c.h, c.m, got, c.want)
		}
	}
}

// English says every minute, and is what a language without its own rules says.
func TestSayTimeEnglish(t *testing.T) {
	cases := []struct {
		h, m int
		want string
	}{
		{14, 7, "it's seven past two in the afternoon"},
		{14, 0, "it's two o'clock in the afternoon"},
		{14, 15, "it's quarter past two in the afternoon"},
		{14, 30, "it's half past two in the afternoon"},
		{14, 45, "it's quarter to three in the afternoon"},
		{14, 37, "it's twenty-three to three in the afternoon"},
		{9, 1, "it's a minute past nine in the morning"},
		{11, 59, "it's a minute to twelve in the morning"},
		{12, 0, "it's noon"},
		{0, 0, "it's midnight"},
		{23, 50, "it's ten to twelve at night"},
		{19, 20, "it's twenty past seven in the evening"},
		{4, 45, "it's quarter to five in the morning"}, // the part of the day is the hour said
		{17, 45, "it's quarter to six in the evening"},
	}
	for _, lang := range []string{"", "en", "ja", "xx"} {
		checkSaid(t, lang, cases)
	}
}

// German rounds to five minutes and counts from the half hour; a minute or two off is "kurz", unless
// that would say "nach" or "vor" twice.
func TestSayTimeGerman(t *testing.T) {
	checkSaid(t, "de", []struct {
		h, m int
		want string
	}{
		// On the hour, and one o'clock is "ein Uhr" but "fünf nach eins".
		{7, 0, "es ist sieben Uhr morgens"},
		{1, 0, "es ist ein Uhr nachts"},
		{13, 0, "es ist ein Uhr mittags"},
		{13, 5, "es ist fünf nach eins mittags"},
		{13, 58, "es ist kurz vor zwei Uhr nachmittags"},
		{0, 58, "es ist kurz vor ein Uhr nachts"},
		// Every five minutes; from "fünf vor halb" on, the next hour.
		{7, 5, "es ist fünf nach sieben morgens"},
		{7, 10, "es ist zehn nach sieben morgens"},
		{7, 15, "es ist Viertel nach sieben morgens"},
		{7, 20, "es ist zwanzig nach sieben morgens"},
		{7, 25, "es ist fünf vor halb acht morgens"},
		{7, 30, "es ist halb acht morgens"},
		{7, 35, "es ist fünf nach halb acht morgens"},
		{7, 40, "es ist zwanzig vor acht morgens"},
		{7, 45, "es ist Viertel vor acht morgens"},
		{7, 50, "es ist zehn vor acht morgens"},
		{7, 55, "es ist fünf vor acht morgens"},
		// The rounding: two minutes go back, three forward.
		{7, 2, "es ist kurz nach sieben Uhr morgens"},
		{7, 3, "es ist kurz vor fünf nach sieben morgens"},
		{6, 58, "es ist kurz vor sieben Uhr morgens"},
		{7, 13, "es ist kurz vor Viertel nach sieben morgens"},
		{7, 26, "es ist kurz nach fünf vor halb acht morgens"},
		{7, 28, "es ist kurz vor halb acht morgens"},
		{7, 32, "es ist kurz nach halb acht morgens"},
		{7, 33, "es ist kurz vor fünf nach halb acht morgens"},
		{7, 47, "es ist kurz nach Viertel vor acht morgens"},
		// Never the same word twice: the five alone.
		{22, 7, "es ist fünf nach zehn abends"},
		{7, 17, "es ist Viertel nach sieben morgens"},
		{7, 22, "es ist zwanzig nach sieben morgens"},
		{7, 23, "es ist fünf vor halb acht morgens"},
		{7, 37, "es ist fünf nach halb acht morgens"},
		{7, 43, "es ist Viertel vor acht morgens"},
		{7, 53, "es ist fünf vor acht morgens"},
		// The hour changes at "fünf vor halb", not at "halb".
		{14, 22, "es ist zwanzig nach zwei nachmittags"},
		{14, 23, "es ist fünf vor halb drei nachmittags"},
		// The part of the day is the hour said; noon and midnight go with the hour before them.
		{10, 10, "es ist zehn nach zehn vormittags"},
		{11, 45, "es ist Viertel vor zwölf vormittags"},
		{11, 58, "es ist kurz vor zwölf Uhr mittags"},
		{12, 0, "es ist zwölf Uhr mittags"},
		{12, 2, "es ist kurz nach zwölf Uhr mittags"},
		{12, 30, "es ist halb eins mittags"},
		{14, 40, "es ist zwanzig vor drei nachmittags"},
		{18, 0, "es ist sechs Uhr abends"},
		{18, 32, "es ist kurz nach halb sieben abends"},
		{19, 55, "es ist fünf vor acht abends"},
		{22, 22, "es ist zwanzig nach zehn abends"},
		{22, 23, "es ist fünf vor halb elf abends"}, // the evening lasts until 23:00 on the clock
		{22, 30, "es ist halb elf abends"},
		{22, 45, "es ist Viertel vor elf abends"},
		{22, 58, "es ist kurz vor elf Uhr abends"},
		{23, 0, "es ist elf Uhr nachts"},
		{23, 2, "es ist kurz nach elf Uhr nachts"},
		{4, 45, "es ist Viertel vor fünf morgens"},
		{23, 45, "es ist Viertel vor zwölf nachts"},
		{23, 58, "es ist kurz vor Mitternacht"},
		{0, 0, "es ist Mitternacht"},
		{0, 2, "es ist kurz nach Mitternacht"},
		{0, 15, "es ist Viertel nach zwölf nachts"},
	})
}

// Spanish says every minute, the hour first, and keeps "menos" for the round minutes near the next
// hour only: nobody says "las ocho menos veintitrés". Without the sun to go by, its parts of the
// day start at six and end at eight.
func TestSayTimeSpanish(t *testing.T) {
	checkSaid(t, "es", []struct {
		h, m int
		want string
	}{
		{14, 7, "son las dos y siete de la tarde"},
		{14, 0, "son las dos en punto de la tarde"},
		{14, 15, "son las dos y cuarto de la tarde"},
		{14, 30, "son las dos y media de la tarde"},
		{14, 37, "son las dos y treinta y siete de la tarde"}, // not "menos veintitrés"
		{14, 40, "son las tres menos veinte de la tarde"},
		{14, 43, "son las dos y cuarenta y tres de la tarde"}, // not "menos diecisiete"
		{14, 45, "son las tres menos cuarto de la tarde"},
		{14, 50, "son las tres menos diez de la tarde"},
		{14, 55, "son las tres menos cinco de la tarde"},
		{14, 56, "son las dos y cincuenta y seis de la tarde"}, // not "menos cuatro"
		{14, 59, "son las dos y cincuenta y nueve de la tarde"},
		{13, 1, "es la una y uno de la tarde"},
		{1, 30, "es la una y media de la madrugada"},
		{0, 10, "son las doce y diez de la madrugada"},
		{4, 45, "son las cinco menos cuarto de la madrugada"},
		{6, 0, "son las seis en punto de la mañana"},
		{11, 45, "son las doce menos cuarto de la mañana"},
		{12, 0, "es mediodía"}, // the exact hour only, with the verb, and no part of the day
		{12, 10, "son las doce y diez"},
		{12, 40, "es la una menos veinte de la tarde"}, // said as one, the afternoon's already
		{0, 0, "es medianoche"},
		{19, 20, "son las siete y veinte de la tarde"},
		{20, 10, "son las ocho y diez de la noche"},
		{23, 50, "son las doce menos diez de la noche"},
	})
}

// Spanish counts its parts of the day by the sun where home's place is known, the way the RAE has
// them, but the sun only moves them so far: the morning starts when it rises, never later than
// six, and the afternoon ends when it sets, never before eight and never later than nine.
func TestSayTimeSpanishBySun(t *testing.T) {
	madridDecember := Sun{ // the sun up at half past eight, down at twenty to six
		Rise: time.Date(2026, 12, 21, 8, 34, 0, 0, time.Local),
		Set:  time.Date(2026, 12, 21, 17, 51, 0, 0, time.Local),
		OK:   true,
	}
	madridJune := Sun{ // up at twenty to seven, down at twenty to ten
		Rise: time.Date(2026, 6, 21, 6, 44, 0, 0, time.Local),
		Set:  time.Date(2026, 6, 21, 21, 48, 0, 0, time.Local),
		OK:   true,
	}
	londonJune := Sun{ // up at quarter to five: half past five is the morning's already
		Rise: time.Date(2026, 6, 21, 4, 43, 0, 0, time.Local),
		Set:  time.Date(2026, 6, 21, 21, 21, 0, 0, time.Local),
		OK:   true,
	}
	puntaArenasDecember := Sun{ // up at twelve past five, down at ten past ten
		Rise: time.Date(2026, 12, 21, 5, 12, 0, 0, time.Local),
		Set:  time.Date(2026, 12, 21, 22, 10, 0, 0, time.Local),
		OK:   true,
	}
	for _, c := range []struct {
		sun  Sun
		h, m int
		want string
	}{
		{madridDecember, 5, 30, "son las cinco y media de la madrugada"},
		{madridDecember, 6, 0, "son las seis en punto de la mañana"},  // six is the cap; the sun sleeps
		{madridDecember, 8, 15, "son las ocho y cuarto de la mañana"}, // the December dark is the morning's
		{madridDecember, 19, 0, "son las siete en punto de la tarde"}, // and the December night is the afternoon's
		{madridDecember, 20, 15, "son las ocho y cuarto de la noche"},
		{madridJune, 6, 15, "son las seis y cuarto de la mañana"},     // six is the cap; the sun has not risen
		{madridJune, 21, 13, "son las nueve y trece de la noche"},     // nine is the cap; the sun is still up
		{londonJune, 4, 30, "son las cuatro y media de la madrugada"}, // this sun rose at quarter to five
		{londonJune, 5, 30, "son las cinco y media de la mañana"},
		{londonJune, 20, 30, "son las ocho y media de la tarde"},
		{puntaArenasDecember, 4, 50, "son las cinco menos diez de la madrugada"},
		{puntaArenasDecember, 5, 30, "son las cinco y media de la mañana"}, // the Chilean December dawn
		{puntaArenasDecember, 20, 30, "son las ocho y media de la tarde"},
		{puntaArenasDecember, 21, 30, "son las nueve y media de la noche"}, // nine is the cap, sunset or no sunset
	} {
		rise := c.sun.Rise // each city's sun on its own day, so the clock's day and the sun's agree
		tt := time.Date(rise.Year(), rise.Month(), rise.Day(), c.h, c.m, 0, 0, time.Local)
		got := said(SayTimeSun(tt, "es", c.sun))
		if got != c.want {
			t.Errorf("%02d:%02d = %q, want %q", c.h, c.m, got, c.want)
		}
	}
}
