package locale

import (
	"strings"
	"testing"
	"time"
)

// said is a SaidTime as one line, its empty parts left out.
func said(s SaidTime) string {
	var parts []string
	for _, p := range []string{s.Intro, s.Approx, s.Count, s.Link, s.Hour, s.Period} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, " ")
}

type sayCase struct {
	h, m int
	want string
}

func checkSaid(t *testing.T, lang string, o SayOptions, cases []sayCase) {
	t.Helper()
	for _, c := range cases {
		if got := said(SayTime(time.Date(2026, 9, 16, c.h, c.m, 0, 0, time.Local), lang, o)); got != c.want {
			t.Errorf("%q %+v %02d:%02d = %q, want %q", lang, o, c.h, c.m, got, c.want)
		}
	}
}

// English says every minute, and is what a language without its own rules says.
func TestSayTimeEnglish(t *testing.T) {
	cases := []sayCase{
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
		checkSaid(t, lang, SayOptions{}, cases)
		// English says every minute anyway and has no regional way: those two change nothing.
		checkSaid(t, lang, SayOptions{Regional: true, EveryMinute: true}, cases)
	}
}

// German rounds to five minutes and counts from the half hour; a minute or two off is "kurz", unless
// that would say "nach" or "vor" twice.
func TestSayTimeGerman(t *testing.T) {
	checkSaid(t, "de", SayOptions{}, []sayCase{
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
		{22, 30, "es ist halb elf nachts"},
		{4, 45, "es ist Viertel vor fünf morgens"},
		{23, 45, "es ist Viertel vor zwölf nachts"},
		{23, 58, "es ist kurz vor Mitternacht"},
		{0, 0, "es ist Mitternacht"},
		{0, 2, "es ist kurz nach Mitternacht"},
		{0, 15, "es ist Viertel nach zwölf nachts"},
	})
}

// The regional way: the quarters count to the next hour, and so does everything from them on.
func TestSayTimeGermanRegional(t *testing.T) {
	checkSaid(t, "de", SayOptions{Regional: true}, []sayCase{
		{7, 0, "es ist sieben Uhr morgens"},
		{7, 10, "es ist zehn nach sieben morgens"},
		{7, 15, "es ist viertel acht morgens"},
		{7, 20, "es ist zehn vor halb acht morgens"},
		{7, 25, "es ist fünf vor halb acht morgens"},
		{7, 30, "es ist halb acht morgens"},
		{7, 35, "es ist fünf nach halb acht morgens"},
		{7, 40, "es ist zehn nach halb acht morgens"},
		{7, 45, "es ist dreiviertel acht morgens"},
		{7, 50, "es ist zehn vor acht morgens"},
		// The hour changes at the quarter, and "kurz" goes either side of it.
		{7, 12, "es ist zehn nach sieben morgens"},
		{7, 13, "es ist kurz vor viertel acht morgens"},
		{7, 17, "es ist kurz nach viertel acht morgens"},
		{7, 22, "es ist kurz nach zehn vor halb acht morgens"},
		{7, 42, "es ist zehn nach halb acht morgens"}, // never "kurz nach zehn nach halb"
		{7, 47, "es ist kurz nach dreiviertel acht morgens"},
		{11, 45, "es ist dreiviertel zwölf vormittags"},
		{23, 15, "es ist viertel zwölf nachts"},
		{23, 58, "es ist kurz vor Mitternacht"},
	})
}

// Every minute: no rounding and no "kurz", each minute counted from the nearest five with a name.
func TestSayTimeGermanEveryMinute(t *testing.T) {
	checkSaid(t, "de", SayOptions{EveryMinute: true}, []sayCase{
		{11, 0, "es ist elf Uhr vormittags"},
		{11, 1, "es ist eine Minute nach elf vormittags"},
		{11, 13, "es ist dreizehn nach elf vormittags"},
		{11, 15, "es ist Viertel nach elf vormittags"},
		{11, 24, "es ist vierundzwanzig nach elf vormittags"},
		{11, 25, "es ist fünf vor halb zwölf vormittags"},
		{11, 27, "es ist drei vor halb zwölf vormittags"},
		{11, 29, "es ist eine Minute vor halb zwölf vormittags"},
		{11, 30, "es ist halb zwölf vormittags"},
		{11, 33, "es ist drei nach halb zwölf vormittags"},
		{11, 35, "es ist fünf nach halb zwölf vormittags"},
		{11, 36, "es ist vierundzwanzig vor zwölf vormittags"},
		{11, 45, "es ist Viertel vor zwölf vormittags"},
		{11, 59, "es ist eine Minute vor zwölf vormittags"},
		{12, 0, "es ist zwölf Uhr mittags"},
		{0, 1, "es ist eine Minute nach zwölf nachts"},
		{23, 59, "es ist eine Minute vor zwölf nachts"},
		{0, 0, "es ist Mitternacht"},
	})
	checkSaid(t, "de", SayOptions{EveryMinute: true, Regional: true}, []sayCase{
		{11, 13, "es ist dreizehn nach elf vormittags"},
		{11, 14, "es ist vierzehn nach elf vormittags"},
		{11, 15, "es ist viertel zwölf vormittags"},
		{11, 16, "es ist eine Minute nach viertel zwölf vormittags"},
		{11, 19, "es ist vier nach viertel zwölf vormittags"},
		{11, 22, "es ist acht vor halb zwölf vormittags"},
		{11, 38, "es ist acht nach halb zwölf vormittags"},
		{11, 40, "es ist zehn nach halb zwölf vormittags"},
		{11, 42, "es ist drei vor dreiviertel zwölf vormittags"},
		{11, 47, "es ist dreizehn vor zwölf vormittags"},
	})
}

// Without the part of the day, in every language; noon and midnight have none anyway.
func TestSayTimeNoPeriod(t *testing.T) {
	o := SayOptions{NoPeriod: true}
	checkSaid(t, "de", o, []sayCase{
		{18, 32, "es ist kurz nach halb sieben"},
		{12, 0, "es ist zwölf Uhr"},
		{0, 0, "es ist Mitternacht"},
	})
	checkSaid(t, "en", o, []sayCase{
		{19, 20, "it's twenty past seven"},
		{12, 0, "it's noon"},
	})
}
