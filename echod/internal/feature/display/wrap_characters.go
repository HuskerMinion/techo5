//go:build !dot

package display

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/image/font"
)

// characterLines supplies break opportunities for an oversized Japanese token.
// Latin words keep their existing space/hyphen wrapping. Combining marks stay
// with their base character, including decomposed Japanese dakuten.
func (r *paint) characterLines(face font.Face, word string, width int) []string {
	japanese := false
	for _, c := range word {
		if unicode.In(c, unicode.Han, unicode.Hiragana, unicode.Katakana) {
			japanese = true
			break
		}
	}
	if !japanese || r.width(face, word) <= width {
		return nil
	}
	var lines []string
	start, prev, ok := 0, 0, 0 // ok: the last cut between Japanese characters, or at either end of a run
	var before rune
	for i, c := range word {
		if i > start && !unicode.IsMark(c) && r.width(face, word[start:i+len(string(c))]) > width {
			// A closing mark (。、」) never starts a line: the character before it goes down with it.
			cut := i
			if strings.ContainsRune(noLineStart, c) && prev > start {
				cut = prev
			}
			// Nor is a Latin word or a number (6053, Assistant) cut in two, where there is a cut before it.
			if l, _ := utf8.DecodeLastRuneInString(word[:cut]); ok > start && ok < cut && !japaneseRune(l) {
				if f, _ := utf8.DecodeRuneInString(word[cut:]); !japaneseRune(f) {
					cut = ok
				}
			}
			lines = append(lines, word[start:cut])
			start = cut
		}
		if i > 0 && !unicode.IsMark(c) && (japaneseRune(before) || japaneseRune(c)) && !strings.ContainsRune(noLineStart, c) {
			ok = i
		}
		if !unicode.IsMark(c) {
			prev = i
		}
		before = c
	}
	return append(lines, word[start:])
}

// japaneseRune is whether c is Japanese: kana, kanji, Japanese punctuation or a full-width form.
func japaneseRune(c rune) bool {
	return unicode.In(c, unicode.Han, unicode.Hiragana, unicode.Katakana) ||
		(c >= 0x3000 && c <= 0x303f) || (c >= 0xff00 && c <= 0xffef)
}

// runOn is how many bytes of word, a Japanese run too wide for a line of its own, still fit after
// line and a space: cut only where a Japanese character begins or ends, never inside a Latin word,
// before a combining mark or before a closing mark. 0 when none does.
func (r *paint) runOn(face font.Face, line, word string, width int) int {
	best := 0
	var prev rune
	for i, c := range word {
		if i > 0 && (japaneseRune(prev) || japaneseRune(c)) && !unicode.IsMark(c) && !strings.ContainsRune(noLineStart, c) {
			if r.width(face, line+" "+word[:i]) > width {
				break
			}
			best = i
		}
		prev = c
	}
	return best
}

// noLineStart is the punctuation Japanese text keeps off the start of a line.
const noLineStart = "。、，．）」』】！？・ー…"
