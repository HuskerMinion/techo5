package ring

import (
	"strconv"
	"strings"
	"unicode"
)

// A snooze asked for in words: "snooze", "snooze the alarm", "snooze for ten minutes", "snooze it
// another 5 minutes please". Read here, on the device, for the same reason a spoken stop is: Home
// Assistant keeps no alarm of this device's, so it has nothing to snooze.

// snoozeWords are the words besides a length that a request to snooze is made of. A sentence with any
// other word in it is something else and is left to Home Assistant.
var snoozeWords = map[string]bool{
	"snooze": true, "it": true, "that": true, "the": true, "alarm": true, "for": true, "another": true,
	"more": true, "please": true, "ok": true, "okay": true, "just": true, "me": true, "give": true,
}

// numberWords are the numbers a length is said with. Tens and ones are added, so "twenty five" is 25,
// and "a hundred" or "one hundred" is read in readNumber, as the hundred is said after the number.
var numberWords = map[string]int{
	"a": 1, "an": 1, "one": 1, "two": 2, "three": 3, "four": 4, "five": 5, "six": 6, "seven": 7,
	"eight": 8, "nine": 9, "ten": 10, "eleven": 11, "twelve": 12, "thirteen": 13, "fourteen": 14,
	"fifteen": 15, "sixteen": 16, "seventeen": 17, "eighteen": 18, "nineteen": 19, "twenty": 20,
	"thirty": 30, "forty": 40, "fifty": 50, "sixty": 60, "seventy": 70, "eighty": 80, "ninety": 90,
}

// minuteWords are the units a length is said in.
var minuteWords = map[string]bool{"minute": true, "minutes": true, "min": true, "mins": true}

// SnoozeAsked reports whether text asks for a ringing alarm to be snoozed, and for how many minutes: 0
// when no length was said, which is the length set on the device. Only "snooze" and the words above,
// with at most one length ("ten minutes", "5 minutes", "half an hour"), so "snooze my phone" or "set
// a timer for ten minutes" are not taken for it. The length is not held to the device's limits here.
func SnoozeAsked(text string) (minutes int, ok bool) {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})

	asked, said, number := false, false, 0
	for i := 0; i < len(words); i++ {
		w := words[i]
		switch {
		case w == "snooze":
			asked = true
		case w == "half" && i+2 < len(words) && (words[i+1] == "an" || words[i+1] == "a") && words[i+2] == "hour":
			if said {
				return 0, false
			}
			minutes, said = 30, true
			i += 2
		// "Snooze for half hour", as it is said too, without the "an" (#734).
		case w == "half" && i+1 < len(words) && words[i+1] == "hour":
			if said {
				return 0, false
			}
			minutes, said = 30, true
			i++
		case isNumber(w):
			// A number is a length only with its unit after it: "snooze for ten" is not one. "Ten more
			// minutes" is the same length.
			n, j := readNumber(words, i)
			if j < len(words) && (words[j] == "more" || words[j] == "extra") {
				j++
			}
			if said || j >= len(words) || !minuteWords[words[j]] {
				return 0, false
			}
			number, said = n, true
			i = j
		case snoozeWords[w]:
		default:
			return 0, false
		}
	}
	if !asked {
		return 0, false
	}
	if number > 0 {
		minutes = number
	}
	return minutes, true
}

// isNumber is whether w starts a number: digits or a number word.
func isNumber(w string) bool {
	if _, err := strconv.Atoi(w); err == nil {
		return true
	}
	_, ok := numberWords[w]
	return ok
}

// NumberAt reads the number starting at words[i], lowercased words: digits, or number words as a
// snooze takes them ("ten", "twenty five", "a"). It returns the number and the index of the word after
// it, and false when words[i] starts no number. The timers read a length in words with it (#734), so
// what a snooze understands and what a timer does are the same numbers.
func NumberAt(words []string, i int) (n, next int, ok bool) {
	if i >= len(words) || !isNumber(words[i]) {
		return 0, i, false
	}
	n, next = readNumber(words, i)
	return n, next, true
}

// readNumber reads the number starting at words[i], a tens word and a ones word added together, and
// returns it with the index of the word after it. "A hundred" and "one hundred" up to "nine hundred"
// are read too, with what follows them added ("a hundred twenty"): a timer is at most a day, which is
// fourteen hundred and forty minutes, and nobody says that, so hundreds of minutes or seconds are where
// it ends.
func readNumber(words []string, i int) (n, next int) {
	if v, err := strconv.Atoi(words[i]); err == nil {
		return v, i + 1
	}
	n, next = numberWords[words[i]], i+1
	if n < 10 && next < len(words) && words[next] == "hundred" {
		n, next = n*100, next+1
		// "A hundred and twenty" as well as "a hundred twenty". What follows is added only when it is
		// a number of its own: "a" or "an" there starts the next length, and a second hundred is not
		// one number with the first.
		j := next
		if j < len(words) && words[j] == "and" {
			j++
		}
		if j < len(words) && words[j] != "a" && words[j] != "an" && (j+1 >= len(words) || words[j+1] != "hundred") {
			if _, ok := numberWords[words[j]]; ok {
				more, after := readNumber(words, j)
				return n + more, after
			}
		}
		return n, next
	}
	if n >= 20 && n%10 == 0 && next < len(words) {
		if ones, ok := numberWords[words[next]]; ok && ones < 10 && words[next] != "a" && words[next] != "an" {
			return n + ones, next + 1
		}
	}
	return n, next
}
