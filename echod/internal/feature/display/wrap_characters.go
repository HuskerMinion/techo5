//go:build !dot

package display

import (
	"strings"
	"unicode"

	"github.com/HuskerMinion/techo5/echod/internal/lib/fonts"
	"golang.org/x/image/font"
)

// characterLines supplies break opportunities for an oversized Japanese or Khmer token.
// Latin words keep their existing space/hyphen wrapping. Combining marks stay
// with their base character, including decomposed Japanese dakuten; Khmer breaks
// only between syllables (fonts.LineBreaks).
func (r *paint) characterLines(face font.Face, word string, width int) []string {
	if fonts.HasLineBreaks(word) {
		return fonts.SplitToWidth(word, width, func(s string) int { return r.width(face, s) })
	}
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
	start, prev := 0, 0
	for i, c := range word {
		if i > start && !unicode.IsMark(c) && r.width(face, word[start:i+len(string(c))]) > width {
			// A closing mark (。、」) never starts a line: the character before it goes down with it.
			cut := i
			if strings.ContainsRune(noLineStart, c) && prev > start {
				cut = prev
			}
			lines = append(lines, word[start:cut])
			start = cut
		}
		if !unicode.IsMark(c) {
			prev = i
		}
	}
	return append(lines, word[start:])
}

// noLineStart is the punctuation Japanese text keeps off the start of a line.
const noLineStart = "。、，．）」』】！？・ー"
