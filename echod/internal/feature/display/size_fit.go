//go:build !dot

package display

import (
	"strings"
	"sync"

	"golang.org/x/image/font"

	"github.com/HuskerMinion/techo5/echod/internal/feature/notify"
)

// Words drawn as large as a box allows, for a pop-up read across a room (notify's size) and for a lone
// card on a drawn dashboard. The size is found by trying faces from the largest down and taking the
// first whose wrapped words fit the box in both directions; the faces follow the screen's font and
// text size like every other (textFace), so what is found is in the panel's own pixels.

// popupSizes are the sizes tried for a notification's message, in the layout's pixels, largest first.
// Large stops where it is clearly bigger than the usual 48; huge starts where a short message fills
// most of a Show's width.
func popupSizes(s notify.Size) []int {
	switch s {
	case notify.SizeLarge:
		return stepsDown(84, 52, 4)
	case notify.SizeHuge:
		return stepsDown(260, 40, 8)
	}
	return nil
}

func stepsDown(from, to, step int) []int {
	var out []int
	for v := from; v >= to; v -= step {
		out = append(out, v)
	}
	return out
}

// fitWords is text wrapped in the largest of sizes (bold or not) whose lines all fit w across and whose
// lines together fit h down, with the face and the distance from one baseline to the next. When none
// does, it is the smallest, with the lines that do not fit left out and the last one cut short by an
// ellipsis, so the words never run out of the box.
func (r *paint) fitWords(bold bool, text string, w, h int, sizes []int) ([]string, font.Face, int) {
	return r.fitParagraphs(bold, []string{text}, w, h, sizes)
}

// fitParagraphs is fitWords for words in paragraphs, each wrapped on its own and none run into the next.
func (r *paint) fitParagraphs(bold bool, paras []string, w, h int, sizes []int) ([]string, font.Face, int) {
	if len(sizes) == 0 {
		return nil, nil, 0
	}
	key := wordsKey{r.faceKey(), r.s(100), bold, strings.Join(paras, "\n"), w, h, sizes[0], sizes[len(sizes)-1], len(sizes)}
	fitMu.Lock()
	hit, ok := fitCache[key]
	fitMu.Unlock()
	if ok {
		return hit.lines, hit.face, hit.lineH
	}
	lines, face, lineH := r.fitUncached(bold, paras, w, h, sizes)
	fitMu.Lock()
	if len(fitCache) >= fitKept {
		clear(fitCache)
	}
	fitCache[key] = wordsResult{lines, face, lineH}
	fitMu.Unlock()
	return lines, face, lineH
}

// wordsKey is what a fit depends on: the font and text size (faceKey), the panel's scale, the words, the
// box and the sizes tried.
type wordsKey struct {
	face                 fontKey
	scale                int
	bold                 bool
	text                 string
	w, h, first, last, n int
}

type wordsResult struct {
	lines []string
	face  font.Face
	lineH int
}

// fitKept is how many fits are kept before they start again: a pop-up or warning page redrawn every
// frame fits once.
const fitKept = 16

var (
	fitMu    sync.Mutex
	fitCache = map[wordsKey]wordsResult{}
)

func (r *paint) fitUncached(bold bool, paras []string, w, h int, sizes []int) ([]string, font.Face, int) {
	var face font.Face
	var lines []string
	var lineH int
	for _, px := range sizes {
		f := r.textFace(bold, px)
		if f == nil {
			continue
		}
		face, lineH = f, f.Metrics().Height.Ceil()
		lines = lines[:0:0]
		for _, p := range paras {
			if p == "" {
				continue
			}
			lines = append(lines, r.wrap(f, p, w)...)
		}
		if len(lines)*lineH <= h && r.widest(f, lines) <= w {
			return lines, face, lineH
		}
	}
	if face == nil {
		return nil, nil, 0
	}
	if most := max(1, h/lineH); len(lines) > most {
		lines = append(lines[:most-1], strings.Join(lines[most-1:], " "))
	}
	for i, l := range lines {
		lines[i] = r.fit(face, l, w)
	}
	return lines, face, lineH
}

// widest is the width of the longest of lines in face.
func (r *paint) widest(face font.Face, lines []string) int {
	most := 0
	for _, l := range lines {
		most = max(most, r.width(face, l))
	}
	return most
}
