package voice

import (
	"slices"

	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
)

// What "call Mom" is made of. Only these shapes, and only a name in the phone book, exactly: a sentence
// with anything else in it is something else and is left to Home Assistant. Nothing is guessed, since
// a guess dials somebody: "call Tom" is not "Mom", and "call Mom's cell" is not "Mom".
//
//	[please | hey | can you | could you | would you]... call|phone|ring|dial [up] [to] [the] NAME [please | now | for me | on the phone]...
//	[please | hey | can you | could you | would you]... give [the] NAME a call [please | now | for me | on the phone]...
//
// "the" goes before a name only when the name does not start with it already.

// callPolite are the words that may come before the request, alone or after each other.
var callPolite = [][]string{{"please"}, {"hey"}, {"can", "you"}, {"could", "you"}, {"would", "you"}}

// callEnds are the words that may come after the name.
var callEnds = [][]string{{"please"}, {"now"}, {"for", "me"}, {"on", "the", "phone"}}

// callVerbs are the words that ask for a call.
var callVerbs = []string{"call", "phone", "ring", "dial"}

// callAsked reports which contact a transcript asks to call, if it is a request to call one of them.
func callAsked(text string, book []phone.Contact) (phone.Contact, bool) {
	words := phone.NameWords(text)
	words = skipAll(words, callPolite)
	if len(words) == 0 {
		return phone.Contact{}, false
	}
	var rest, tail []string
	switch {
	case slices.Contains(callVerbs, words[0]):
		rest = skip(words[1:], []string{"up"})
		rest = skip(rest, []string{"to"})
	case words[0] == "give":
		rest, tail = words[1:], []string{"a", "call"}
	default:
		return phone.Contact{}, false
	}
	for _, c := range book {
		for _, name := range c.Names() {
			if callsName(rest, phone.NameWords(name), tail) {
				return c, true
			}
		}
	}
	return phone.Contact{}, false
}

// callsName reports whether words are the name, with "the" before it if the name does not start with
// it, then tail, then only callEnds.
func callsName(words, name, tail []string) bool {
	if len(name) == 0 {
		return false
	}
	if name[0] != "the" {
		words = skip(words, []string{"the"})
	}
	if !hasPrefix(words, name) {
		return false
	}
	words = words[len(name):]
	if !hasPrefix(words, tail) {
		return false
	}
	return len(skipAll(words[len(tail):], callEnds)) == 0
}

// skipAll takes any of the phrases off the front of words, as many times as they come.
func skipAll(words []string, phrases [][]string) []string {
	for {
		before := len(words)
		for _, p := range phrases {
			words = skip(words, p)
		}
		if len(words) == before {
			return words
		}
	}
}

// skip takes phrase off the front of words, if it is there.
func skip(words, phrase []string) []string {
	if hasPrefix(words, phrase) {
		return words[len(phrase):]
	}
	return words
}

func hasPrefix(words, prefix []string) bool {
	return len(words) >= len(prefix) && slices.Equal(words[:len(prefix)], prefix)
}
