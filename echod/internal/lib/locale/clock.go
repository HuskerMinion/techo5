package locale

import (
	"strings"
	"time"
)

// SaidTime is the time as the words clock style says it, in parts so a face can lay them out:
// "it's" (Intro), "twenty" (Count), "past" (Link), "seven" (Hour), "in the evening" (Period). On the
// hour Count and Link are empty; at noon and midnight Period is too.
type SaidTime struct {
	Intro, Count, Link, Hour, Period string
}

// Lead is what comes before the hour: "twenty past", "fünf vor halb"; empty on the hour.
func (s SaidTime) Lead() string { return strings.TrimSpace(s.Count + " " + s.Link) }

// sayers are the languages that say the time their own way, each with its rules in its own file
// (clock_de.go). Any other language says it in English.
var sayers = map[string]func(time.Time) SaidTime{
	"de": sayGerman,
}

// SayTime is the time t as it is said in lang.
func SayTime(t time.Time, lang string) SaidTime {
	if say, ok := sayers[lang]; ok {
		return say(t)
	}
	return sayEnglish(t)
}

// englishNumbers are the numbers English says the time with.
var englishNumbers = []string{"", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten",
	"eleven", "twelve", "thirteen", "fourteen", "fifteen", "sixteen", "seventeen", "eighteen", "nineteen", "twenty"}

func englishNumber(n int) string {
	if n <= 20 {
		return englishNumbers[n]
	}
	return "twenty-" + englishNumbers[n-20]
}

// sayEnglish says every minute: "seven past", "quarter to", "a minute to"; the hour after half past
// is the next one's; noon and midnight by name.
func sayEnglish(t time.Time) SaidTime {
	s := SaidTime{Intro: "it's"}
	h, m := t.Hour(), t.Minute()
	switch {
	case m == 0:
	case m == 1:
		s.Count, s.Link = "a minute", "past"
	case m == 15:
		s.Count, s.Link = "quarter", "past"
	case m == 30:
		s.Count, s.Link = "half", "past"
	case m == 45:
		s.Count, s.Link = "quarter", "to"
	case m == 59:
		s.Count, s.Link = "a minute", "to"
	case m < 30:
		s.Count, s.Link = englishNumber(m), "past"
	default:
		s.Count, s.Link = englishNumber(60-m), "to"
	}
	said := h
	if m > 30 {
		said = (h + 1) % 24
	}
	switch {
	case said == 0 && m == 0:
		s.Hour = "midnight"
		return s
	case said == 12 && m == 0:
		s.Hour = "noon"
		return s
	}
	s.Hour = englishNumbers[(said+11)%12+1]
	if m == 0 {
		s.Hour += " o'clock"
	}
	switch ph := partHour(h, said); {
	case ph >= 5 && ph < 12:
		s.Period = "in the morning"
	case ph >= 12 && ph < 18:
		s.Period = "in the afternoon"
	case ph >= 18 && ph < 22:
		s.Period = "in the evening"
	default:
		s.Period = "at night"
	}
	return s
}

// partHour is the hour whose part of the day goes with the time: the said hour's ("quarter to five in
// the morning", not "at night"), except for noon and midnight, which belong to the hour before them.
func partHour(h, said int) int {
	if said == 12 || said == 0 {
		return h
	}
	return said
}
