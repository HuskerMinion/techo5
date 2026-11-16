//go:build !dot

package display

import (
	"image/color"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/locale"
)

// wordsLinkMix is how far the link ("nach", "vor halb", "past") sits from the accent towards the
// text: between the count and the plain words, so the eye goes count, hour, then the link.
const wordsLinkMix = 0.5

// wordsPart is a piece of the Words style's line before the hour, with the color its role has.
type wordsPart struct {
	text string
	col  color.RGBA
}

// wordsRoles colors the line before the hour by role, from the palette's quiet, accent and text
// colors: how far off the time is ("kurz vor") quiet like "it's" and the part of the day, the count
// ("Viertel", "twenty") in the accent like the hour, and the link between the accent and the text.
// Empty parts are left out.
func wordsRoles(s locale.SaidTime, quiet, accent, text color.RGBA) []wordsPart {
	if !config.Get().Screen.WordsColors {
		// As it always was: the whole line in the text color.
		if l := s.Lead(); l != "" {
			return []wordsPart{{l, text}}
		}
		return nil
	}
	var out []wordsPart
	for _, p := range []wordsPart{
		{s.Approx, quiet},
		{s.Count, accent},
		{s.Link, lerpRGB(accent, text, wordsLinkMix)},
	} {
		if p.text != "" {
			out = append(out, p)
		}
	}
	return out
}
