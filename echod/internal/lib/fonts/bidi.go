package fonts

import (
	"strings"
	"unicode"

	"github.com/go-text/typesetting/segmenter"
	"golang.org/x/text/unicode/bidi"
)

// hebrewVisualOrder converts a Hebrew-led display line to drawing order. The font face
// draws from left to right, so RTL runs are reversed by grapheme and LTR runs
// such as room names and numbers retain their internal order.
func hebrewVisualOrder(s string) string {
	if strings.IndexFunc(s, func(r rune) bool { return unicode.Is(unicode.Hebrew, r) }) < 0 || !hebrewLineIsRTL(s) {
		return s
	}

	var p bidi.Paragraph
	if n, err := p.SetString(s); err != nil || n != len(s) {
		return s
	}
	order, err := p.Order()
	if err != nil || order.NumRuns() == 0 {
		return s
	}

	var visual strings.Builder
	visual.Grow(len(s))
	for i := order.NumRuns() - 1; i >= 0; i-- {
		run := order.Run(i)
		if run.Direction() == bidi.RightToLeft {
			writeReversedGraphemes(&visual, run.String())
		} else {
			visual.WriteString(run.String())
		}
	}
	return visual.String()
}

func hebrewLineIsRTL(s string) bool {
	for _, r := range s {
		prop, _ := bidi.LookupRune(r)
		switch prop.Class() {
		case bidi.L:
			return false
		case bidi.R, bidi.AL:
			return true
		}
	}
	return false
}

func writeReversedGraphemes(dst *strings.Builder, text string) {
	var seg segmenter.Segmenter
	seg.Init([]rune(text))
	it := seg.GraphemeIterator()
	var clusters [][]rune
	for it.Next() {
		clusters = append(clusters, it.Grapheme().Text)
	}
	for i := len(clusters) - 1; i >= 0; i-- {
		cluster := clusters[i]
		dst.WriteString(bidi.ReverseString(string(cluster[:1])))
		dst.WriteString(string(cluster[1:]))
	}
}
