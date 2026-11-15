package fonts

import (
	"strings"
	"sync/atomic"
	"unicode"

	"github.com/go-text/typesetting/bidi"
)

// sawHebrew is set the first time text with a Hebrew letter is put in visual order.
var sawHebrew atomic.Bool

// SawHebrew is whether Hebrew text has been drawn or measured since the daemon started: the screen
// needs a font that draws it (feature/cjkfont fetches one).
func SawHebrew() bool { return sawHebrew.Load() }

func isRTLRune(r rune) bool {
	return (r >= 0x0590 && r <= 0x08FF) || (r >= 0xFB1D && r <= 0xFDFF) || (r >= 0xFE70 && r <= 0xFEFF)
}

func isHebrewRune(r rune) bool { return (r >= 0x0590 && r <= 0x05FF) || (r >= 0xFB1D && r <= 0xFB4F) }

// The marks KeepDirection starts a line with: zero width, they set its direction and are not drawn.
const (
	lrm = "\u200e"
	rlm = "\u200f"
)

// VisualOrder turns logically ordered text into the left-to-right order it is drawn in, running the
// Unicode bidirectional algorithm on lines that hold right-to-left characters (Hebrew, Arabic).
// Text with none is returned as is.
func VisualOrder(s string) string {
	if !strings.ContainsFunc(s, isRTLRune) {
		if strings.ContainsAny(s, lrm+rlm) {
			return dropMarks(s)
		}
		return s
	}
	if !sawHebrew.Load() && strings.ContainsFunc(s, isHebrewRune) {
		sawHebrew.Store(true)
	}
	if strings.ContainsRune(s, '\n') {
		lines := strings.Split(s, "\n")
		for i, l := range lines {
			lines[i] = visualOrderLine(l)
		}
		return strings.Join(lines, "\n")
	}
	return visualOrderLine(s)
}

func dropMarks(s string) string {
	return strings.NewReplacer(lrm, "", rlm, "").Replace(s)
}

// rightToLeft is whether a paragraph reads right to left: its first letter is a right-to-left one
// (UAX #9 rules P2 and P3, as browsers do).
func rightToLeft(s string) bool {
	for _, r := range s {
		if isRTLRune(r) && !unicode.In(r, unicode.Mn, unicode.Me) {
			return true
		}
		if unicode.IsLetter(r) {
			return false
		}
	}
	return false
}

// KeepDirection marks the lines s was wrapped into with s's own direction. A paragraph's direction
// holds for all its lines (UAX #9), but each line is put in visual order on its own when it is drawn:
// a line of a Hebrew paragraph that starts with an English word would be laid out left to right. Each
// line gets a zero-width mark, LRM or RLM, which VisualOrder reads and drops.
func KeepDirection(s string, lines []string) []string {
	if len(lines) < 2 || !strings.ContainsFunc(s, isRTLRune) {
		return lines
	}
	mark := lrm
	if rightToLeft(s) {
		mark = rlm
	}
	for i, l := range lines {
		if l != "" {
			lines[i] = mark + l
		}
	}
	return lines
}

// cluster is a character with the marks that follow it (nikud, cantillation), kept together when a
// stretch is reversed so the marks stay on their letter.
type cluster struct {
	start, end int // runes[start:end]
	level      bidi.Level
}

// visualOrderLine reorders one line: per-character embedding levels from the bidi algorithm, then
// UAX #9 rule L2 (from the highest level down to the lowest odd one, reverse every stretch at that
// level or above), then L4 (brackets at odd levels are mirrored).
func visualOrderLine(s string) string {
	rs := []rune(s)
	if len(rs) == 0 {
		return s
	}
	var p bidi.Paragraph
	runs := p.Segment(rs, bidi.Neutral)
	levels := make([]bidi.Level, len(rs))
	for i := 0; i < runs.NumRuns(); i++ {
		r := runs.Run(i)
		for j := r.Start; j < r.End; j++ {
			levels[j] = r.Level
		}
	}

	cs := make([]cluster, 0, len(rs))
	for i, r := range rs {
		if len(cs) > 0 && unicode.In(r, unicode.Mn, unicode.Me) {
			cs[len(cs)-1].end = i + 1
			continue
		}
		cs = append(cs, cluster{i, i + 1, levels[i]})
	}

	var high, lowOdd bidi.Level = 0, 127
	for _, c := range cs {
		high = max(high, c.level)
		if c.level%2 == 1 {
			lowOdd = min(lowOdd, c.level)
		}
	}
	for k := high; k >= lowOdd && k > 0; k-- {
		for i := 0; i < len(cs); {
			if cs[i].level < k {
				i++
				continue
			}
			j := i
			for j < len(cs) && cs[j].level >= k {
				j++
			}
			for a, b := i, j-1; a < b; a, b = a+1, b-1 {
				cs[a], cs[b] = cs[b], cs[a]
			}
			i = j
		}
	}

	var b strings.Builder
	b.Grow(len(s))
	for _, c := range cs {
		for i := c.start; i < c.end; i++ {
			r := rs[i]
			if r == 0x200E || r == 0x200F {
				continue
			}
			if i == c.start && c.level%2 == 1 {
				r = mirror(r)
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}

// mirror is the mirrored form of a bracket or other paired character, drawn in right-to-left text.
func mirror(r rune) rune {
	switch r {
	case '(':
		return ')'
	case ')':
		return '('
	case '[':
		return ']'
	case ']':
		return '['
	case '{':
		return '}'
	case '}':
		return '{'
	case '<':
		return '>'
	case '>':
		return '<'
	case '«':
		return '»'
	case '»':
		return '«'
	case '‹':
		return '›'
	case '›':
		return '‹'
	}
	return r
}
