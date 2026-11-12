package fonts

import (
	"strings"

	"golang.org/x/text/unicode/bidi"
)

func isRTLRune(r rune) bool {
	return (r >= 0x0590 && r <= 0x08FF) || (r >= 0xFB1D && r <= 0xFDFF) || (r >= 0xFE70 && r <= 0xFEFF)
}

// VisualOrder turns logically ordered text into the left-to-right order it is drawn in, running the
// Unicode bidirectional algorithm on lines that hold right-to-left characters (Hebrew, Arabic).
// Text with none is returned as is.
func VisualOrder(s string) string {
	if !strings.ContainsFunc(s, isRTLRune) {
		return s
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

func visualOrderLine(s string) string {
	var p bidi.Paragraph
	if _, err := p.SetString(s); err != nil {
		return s
	}
	order, err := p.Order()
	if err != nil {
		return s
	}
	n := order.NumRuns()
	if n == 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	processRun := func(i int) {
		r := order.Run(i)
		if r.Direction() == bidi.RightToLeft {
			b.WriteString(bidi.ReverseString(r.String()))
		} else {
			b.WriteString(r.String())
		}
	}
	if p.IsLeftToRight() {
		for i := 0; i < n; i++ {
			processRun(i)
		}
	} else {
		for i := n - 1; i >= 0; i-- {
			processRun(i)
		}
	}
	return b.String()
}
