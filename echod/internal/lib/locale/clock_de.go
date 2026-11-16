package locale

import (
	"strings"
	"time"
)

// germanHours are the hours as German counts them on the clock, "eins" to "zwölf"; on the hour, one is
// "ein Uhr".
var germanHours = [13]string{"", "eins", "zwei", "drei", "vier", "fünf", "sechs", "sieben", "acht", "neun", "zehn", "elf", "zwölf"}

// germanMinutes are the minutes German counts the time with when it says every minute; one is
// "eine Minute".
var germanMinutes = [25]string{"", "eine Minute", "zwei", "drei", "vier", "fünf", "sechs", "sieben", "acht", "neun", "zehn",
	"elf", "zwölf", "dreizehn", "vierzehn", "fünfzehn", "sechzehn", "siebzehn", "achtzehn", "neunzehn", "zwanzig",
	"einundzwanzig", "zweiundzwanzig", "dreiundzwanzig", "vierundzwanzig"}

// germanWay is one way of saying the time in German: what is said at each five minutes past the hour,
// the count and the link before the hour, and the first minute that says the next hour.
type germanWay struct {
	fives    [12][2]string
	nextFrom int
	// between is the count and link for a minute that is not a five, when every minute is said.
	between func(m int) (count, link string)
}

// germanCommon is the way most of Germany says it: "Viertel nach zwei", "zwanzig nach zwei", "fünf vor
// halb drei", "zwanzig vor drei", "Viertel vor drei".
var germanCommon = germanWay{
	fives: [12][2]string{
		{"", ""}, {"fünf", "nach"}, {"zehn", "nach"}, {"Viertel", "nach"}, {"zwanzig", "nach"},
		{"fünf", "vor halb"}, {"", "halb"}, {"fünf", "nach halb"},
		{"zwanzig", "vor"}, {"Viertel", "vor"}, {"zehn", "vor"}, {"fünf", "vor"},
	},
	nextFrom: 25,
	between: func(m int) (string, string) {
		switch {
		case m < 25:
			return germanMinutes[m], "nach"
		case m < 30:
			return germanMinutes[30-m], "vor halb"
		case m < 36:
			return germanMinutes[m-30], "nach halb"
		}
		return germanMinutes[60-m], "vor"
	},
}

// germanRegional is the way of the east and south: "viertel drei", "zehn vor halb drei", "zehn nach
// halb drei", "dreiviertel drei"; from the quarter on, the next hour is said.
var germanRegional = germanWay{
	fives: [12][2]string{
		{"", ""}, {"fünf", "nach"}, {"zehn", "nach"}, {"viertel", ""}, {"zehn", "vor halb"},
		{"fünf", "vor halb"}, {"", "halb"}, {"fünf", "nach halb"},
		{"zehn", "nach halb"}, {"dreiviertel", ""}, {"zehn", "vor"}, {"fünf", "vor"},
	},
	nextFrom: 15,
	between: func(m int) (string, string) {
		switch {
		case m < 15:
			return germanMinutes[m], "nach"
		case m < 20:
			return germanMinutes[m-15], "nach viertel"
		case m < 30:
			return germanMinutes[30-m], "vor halb"
		case m < 41:
			return germanMinutes[m-30], "nach halb"
		case m < 45:
			return germanMinutes[45-m], "vor dreiviertel"
		}
		return germanMinutes[60-m], "vor"
	},
}

// sayGerman says the time to the nearest five minutes, counted from the hour and the half hour:
// "Viertel nach sechs", "fünf vor halb sieben", "halb sieben". A minute or two either side is
// "kurz vor" or "kurz nach" the five ("kurz nach halb sieben", "kurz vor sieben Uhr"), unless that
// says the same word twice ("kurz nach fünf nach sieben"), where the five alone does. Midnight is
// "Mitternacht", noon "zwölf Uhr mittags".
//
// Regional is the east's and south's quarters (germanRegional); EveryMinute says each minute from the
// nearest five that has a name ("dreizehn nach elf", "drei vor halb zwölf"), with no "kurz".
func sayGerman(t time.Time, o SayOptions) SaidTime {
	way := germanCommon
	if o.Regional {
		way = germanRegional
	}
	s := SaidTime{Intro: "es ist"}
	h, m := t.Hour(), t.Minute()
	// said is the minute the words name: m itself when every minute is said, else the nearest five.
	said := m
	if o.EveryMinute {
		if m%5 == 0 {
			s.Count, s.Link = way.fives[m/5][0], way.fives[m/5][1]
		} else {
			s.Count, s.Link = way.between(m)
		}
	} else {
		five, kurz := m/5, ""
		switch m % 5 {
		case 1, 2:
			kurz = "nach"
		case 3, 4:
			kurz = "vor"
			five++
		}
		if five == 12 {
			five, h = 0, (h+1)%24
		}
		said = five * 5
		s.Count, s.Link = way.fives[five][0], way.fives[five][1]
		if kurz != "" && strings.HasPrefix(s.Link+" ", kurz+" ") {
			kurz = ""
		}
		if kurz != "" {
			s.Approx = "kurz " + kurz
		}
	}
	hour := h
	if said >= way.nextFrom {
		hour = (h + 1) % 24
	}
	// A minute or two around midnight is "kurz vor Mitternacht", "kurz nach Mitternacht".
	if hour == 0 && said == 0 {
		s.Hour = "Mitternacht"
		return s
	}
	s.Hour = germanHours[(hour+11)%12+1]
	// Around the full hour it is "Uhr", "ein Uhr" for one: "kurz vor sieben Uhr", "fünf nach eins".
	switch {
	case said == 0 && s.Hour == "eins":
		s.Hour = "ein Uhr"
	case said == 0:
		s.Hour += " Uhr"
	}
	switch ph := partHour(h, hour); {
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
