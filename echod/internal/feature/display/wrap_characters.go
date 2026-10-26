//go:build !dot

package display

import (
	"unicode"

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
	start := 0
	for i, c := range word {
		if i > start && !unicode.IsMark(c) && r.width(face, word[start:i+len(string(c))]) > width {
			lines = append(lines, word[start:i])
			start = i
		}
	}
	return append(lines, word[start:])
}
