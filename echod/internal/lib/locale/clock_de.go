package locale

import (
	"strings"
	"time"
)

// germanHours are the hours as German counts them on the clock, "eins" to "zwölf"; on the hour, one is
// "ein Uhr".
var germanHours = [13]string{"", "eins", "zwei", "drei", "vier", "fünf", "sechs", "sieben", "acht", "neun", "zehn", "elf", "zwölf"}

// germanFives are what German says at each five minutes past the hour: the count and the link before
// the hour. From "fünf vor halb" on, the hour said is the next one.
var germanFives = [12][2]string{
	{"", ""}, {"fünf", "nach"}, {"zehn", "nach"}, {"Viertel", "nach"}, {"zwanzig", "nach"},
	{"fünf", "vor halb"}, {"", "halb"}, {"fünf", "nach halb"},
	{"zwanzig", "vor"}, {"Viertel", "vor"}, {"zehn", "vor"}, {"fünf", "vor"},
}

// sayGerman says the time to the nearest five minutes, counted from the hour and the half hour:
// "Viertel nach sechs", "fünf vor halb sieben", "halb sieben". A minute or two either side is
// "kurz vor" or "kurz nach" the five ("kurz nach halb sieben", "kurz vor sieben Uhr"), unless that
// says the same word twice ("kurz nach fünf nach sieben"), where the five alone does. Midnight is
// "Mitternacht", noon "zwölf Uhr mittags".
func sayGerman(t time.Time, _ Sun) SaidTime {
	s := SaidTime{Intro: "es ist"}
	h, m := t.Hour(), t.Minute()
	five, off := m/5, m%5
	kurz := ""
	switch off {
	case 1, 2:
		kurz = "nach"
	case 3, 4:
		kurz = "vor"
		five++
	}
	if five == 12 {
		five, h = 0, (h+1)%24
	}
	s.Count, s.Link = germanFives[five][0], germanFives[five][1]
	if kurz != "" && strings.HasPrefix(s.Link+" ", kurz+" ") {
		kurz = ""
	}
	switch {
	case kurz != "" && five == 0:
		s.Count, s.Link = "kurz", kurz
	case kurz != "":
		s.Count = strings.TrimSpace("kurz " + kurz + " " + s.Count)
	}
	said := h
	if five >= 5 {
		said = (h + 1) % 24
	}
	// A minute or two around midnight is "kurz vor Mitternacht", "kurz nach Mitternacht".
	if said == 0 && five == 0 {
		s.Hour = "Mitternacht"
		return s
	}
	s.Hour = germanHours[(said+11)%12+1]
	// Around the full hour it is "Uhr", "ein Uhr" for one: "kurz vor sieben Uhr", "fünf nach eins".
	switch {
	case five == 0 && s.Hour == "eins":
		s.Hour = "ein Uhr"
	case five == 0:
		s.Hour += " Uhr"
	}
	// The evening lasts until the clock says eleven: "halb elf abends", "kurz vor elf Uhr abends", though
	// the hour said is already eleven; "elf Uhr nachts" from 23:00.
	ph := partHour(h, said)
	if ph == 23 && t.Hour() == 22 {
		ph = 22
	}
	switch {
	case ph >= 5 && ph < 10:
		s.Period = "morgens"
	case ph >= 10 && ph < 12:
		s.Period = "vormittags"
	case ph >= 12 && ph < 14:
		s.Period = "mittags"
	case ph >= 14 && ph < 18:
		s.Period = "nachmittags"
	case ph >= 18 && ph < 23:
		s.Period = "abends"
	default:
		s.Period = "nachts"
	}
	return s
}
