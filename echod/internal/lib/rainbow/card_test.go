package rainbow

import (
	"fmt"
	"image"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/fonts"
)

// A long answer, about forty seconds of it said, many times the card's four lines.
const longReply = "Tomorrow starts cloudy with a light drizzle until about nine, then it clears from the west. " +
	"The high is fourteen degrees in the early afternoon, with a fresh breeze from the southwest of up to " +
	"thirty kilometers an hour. In the evening clouds come back and there is a chance of showers after " +
	"eight, about forty percent, and the night goes down to seven degrees. Thursday looks drier and a " +
	"little warmer, with sunshine for most of the day and a high of sixteen."

// The window goes down the answer with the voice, the line being said kept second in it, and rests on
// the last lines once it is all said.
func TestTheWindowFollowsTheVoice(t *testing.T) {
	chars := []int{10, 10, 10, 10, 10, 10} // six lines, two more than the window
	for _, c := range []struct{ said, want float64 }{
		{0, 0},
		{1.0 / 6, 0},     // on the second line: still the first four
		{0.25, 0.5},      // half through the second line's last half: half a line down
		{2.0 / 6, 1},     // on the third line
		{0.4, 1.4},       // part of the way through it
		{3.0 / 6, 2},     // on the fourth: the last four lines are in the window
		{0.75, 2},        // and stay there
		{1, 2},           // all said
		{1.5, 2},         // more than all of it, as no reckoning should say
		{-0.2, 0},        // nor less than nothing
		{0.1 / 6, 0},     // a moment in
		{1.25 / 6, 0.25}, // a quarter into the second line: a quarter of a line down
	} {
		if got := scrollFor(chars, 4, c.said); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("said %.3f: %.3f lines down, want %.3f", c.said, got, c.want)
		}
	}
	// It goes by characters, so a long line takes longer to get past than a short one.
	uneven := []int{40, 40, 5, 40, 40, 40}
	if a, b := scrollFor(uneven, 4, 50.0/205), scrollFor(uneven, 4, 82.5/205); !(a > 0.2 && a < 0.3 && b > 1.4 && b < 1.6) {
		t.Errorf("uneven lines: %.3f and %.3f lines down, want a quarter and one and a half", a, b)
	}
	// And only ever on, in small steps.
	prev := 0.0
	for i := 0; i <= 1000; i++ {
		got := scrollFor(uneven, 4, float64(i)/1000)
		if got < prev || got-prev > 0.05 {
			t.Fatalf("said %.3f: %.3f lines down after %.3f", float64(i)/1000, got, prev)
		}
		prev = got
	}
}

// An answer that fits stays put, however much of it has been said.
func TestAShortAnswerDoesNotScroll(t *testing.T) {
	for _, chars := range [][]int{nil, {12}, {30, 30, 30, 30}} {
		for _, said := range []float64{0, 0.5, 1} {
			if got := scrollFor(chars, 4, said); got != 0 {
				t.Errorf("%d lines, said %.1f: %.2f lines down", len(chars), said, got)
			}
		}
	}
	g := newGeometry(showW, showH)
	f := Faces{Heard: face(t, 35), Reply: face(t, 45)}
	w := newCardWords(g, f, "what's the weather", "Sunny and fourteen degrees.", true, "k")
	if w.scrolls() || w.offset() != 0 || w.chars != nil {
		t.Errorf("a one line answer scrolls: %+v", w)
	}
	if math.Abs(w.height-float64(w.img.Rect.Dy())/g.k) > 1 {
		t.Errorf("the card is %.1f tall for words %d pixels tall", w.height, w.img.Rect.Dy())
	}
	// Drawn whole, the same at any point of the voice.
	a, b := cardAt(t, "Sunny and fourteen degrees.", 0), cardAt(t, "Sunny and fourteen degrees.", 1)
	if !same(a, b) {
		t.Error("a short answer is drawn differently as it is said")
	}
}

// A very long answer, such as an AI assistant's of a few thousand characters, is laid out to
// cardMost, the last line ending in an ellipsis, so the card's image stays a few megabytes (about
// 4.5 MB on a Show 5, against 25 MB for 8000 characters uncut); it still scrolls. On the Show 5's screen, where a line holds the fewest words.
func TestAVeryLongAnswerIsCutAtTheMostLines(t *testing.T) {
	g := newGeometry(960, 480)
	f := Faces{Heard: face(t, 26), Reply: face(t, 34)}
	huge := strings.Repeat(longReply+" ", 20) // about 8000 characters
	if got := len(wrapLines(f.Reply, huge, int(cardW*g.k-2*cardPad*g.k), 0)); got <= cardMost {
		t.Fatalf("the test answer is only %d lines", got)
	}
	w := newCardWords(g, f, "tell me everything about the weather", huge, true, "k")
	if len(w.chars) != cardMost || !w.scrolls() {
		t.Fatalf("%d lines laid out to scroll, want %d", len(w.chars), cardMost)
	}
	if want := float64(cardMost-cardLines) * w.lineH; w.more != want {
		t.Errorf("%.0f pixels below the window, want %.0f", w.more, want)
	}
	last := wrapLines(f.Reply, huge, int(cardW*g.k-2*cardPad*g.k), cardMost)[cardMost-1]
	if !strings.HasSuffix(last, "…") {
		t.Errorf("the last line %q has no ellipsis", last)
	}
	// The image is the title and cardMost lines, not the whole answer.
	if h := w.img.Rect.Dy(); float64(h) > float64(cardMost+cardHeard+2)*w.lineH {
		t.Errorf("the image is %d pixels tall for %d lines of %.0f", h, cardMost, w.lineH)
	}
}

// A long answer is laid out whole, the card as tall as four lines of it, and the window scrolls down it
// with the voice to rest on its end; the edges fade where there is more. On the Show 8's screen and the
// Show 5's.
func TestALongAnswerScrollsWithTheVoice(t *testing.T) {
	t.Run("show8", func(t *testing.T) { longAnswer(t, "show8-", showW, showH, 35, 45) })
	t.Run("show5", func(t *testing.T) { longAnswer(t, "show5-", 960, 480, 26, 34) })
}

func longAnswer(t *testing.T, name string, sw, sh int, heard, reply float64) {
	g := newGeometry(sw, sh)
	f := Faces{Heard: face(t, heard), Reply: face(t, reply)}
	w := newCardWords(g, f, "what's the weather tomorrow", longReply, true, "k")
	lines := wrapLines(f.Reply, longReply, int(cardW*g.k-2*cardPad*g.k), 0)
	if len(lines) < 8 || !w.scrolls() || len(w.chars) != len(lines) {
		t.Fatalf("%d lines, %d counted, scrolls %v", len(lines), len(w.chars), w.scrolls())
	}
	if strings.Contains(strings.Join(lines, " "), "…") || strings.Join(lines, " ") != strings.Join(strings.Fields(longReply), " ") {
		t.Error("the answer is not laid out whole")
	}
	// As tall as the card for its first four lines alone.
	four := newCardWords(g, f, "what's the weather tomorrow", strings.Join(lines[:cardLines], " "), true, "k")
	if four.scrolls() || math.Abs(w.height-four.height) > 0.01 {
		t.Errorf("the card is %.1f tall, a four line answer's %.1f", w.height, four.height)
	}
	if want := float64(len(lines)-cardLines) * w.lineH; w.more != want {
		t.Errorf("%.0f pixels below the window, want %.0f", w.more, want)
	}

	// Said evenly over 40 s, then lingering: the window only goes down, the card stays its size, and
	// it rests on the end with nothing left to draw.
	dir := shots(t)
	bg := page(t, sw, sh)
	var s Screen
	start := time.Date(2026, 10, 21, 10, 42, 0, 0, time.UTC)
	dst := image.NewRGBA(bg.Rect)
	const speak, end = 40.0, 50.0
	prev, size, kept := -1, image.Point{}, 0
	var first, last *image.RGBA
	for n := 0; float64(n)*0.066 < end; n++ {
		at := float64(n) * 0.066
		tr := Turn{Phase: "replying", Level: 0.5, Heard: "what's the weather tomorrow", Reply: longReply, Said: clamp01((at - 2) / speak)}
		if at < 1.5 {
			tr = Turn{Phase: "thinking", Heard: tr.Heard}
		} else if at > speak+3 {
			tr.Phase, tr.Level = "lingering", 0
		}
		copy(dst.Pix, bg.Pix)
		cardAt := s.Draw(dst, start.Add(time.Duration(at*float64(time.Second))), tr, f)
		cw := s.card.cur
		if cw == nil || !cw.reply || at < 5 { // once it has found its height
			continue
		}
		if off := cw.offset(); off < prev {
			t.Fatalf("%.2f s: the window went back up, %d after %d", at, off, prev)
		} else {
			prev = off
		}
		if size == (image.Point{}) {
			size = cardAt.Size()
			first = crop(dst, cardAt)
			save(t, dst, dir, name+"scroll-start.png")
		} else if cardAt.Size() != size {
			t.Fatalf("%.2f s: the card went from %v to %v", at, size, cardAt.Size())
		}
		if n%150 == 0 {
			save(t, dst, dir, fmt.Sprintf("%sscroll-%02.0fs.png", name, at))
		}
		// What only the window's rows were painted again for is what painting all of it gives.
		if n%40 == 0 && s.card.prev == nil {
			if s.card.underOK {
				kept++
			}
			was := slices.Clone(s.card.img)
			s.card.look = cardLook{}
			s.card.draw(dst, g, start.Add(time.Duration(at*float64(time.Second))))
			if !slices.Equal(was, s.card.img) {
				t.Fatalf("%.2f s: the window painted alone differs from the whole card painted", at)
			}
		}
		last = crop(dst, cardAt)
	}
	save(t, dst, dir, name+"scroll-end.png")
	if kept == 0 {
		t.Error("the window was never painted again on its own")
	}
	if w := s.card.cur; w.scroll != w.scrollTo || w.offset() != int(w.more+0.5) {
		t.Errorf("at the end %.2f of %.2f lines down", w.scroll, float64(len(w.chars)-cardLines))
	}
	if s.Busy() {
		t.Error("still busy at rest on the end of the answer")
	}
	if same(first, last) {
		t.Error("the card shows the same at the end as at the start")
	}
}

// cardAt is the card alone for a turn replying with reply, said of it said, once it has settled.
func cardAt(t *testing.T, reply string, said float64) *image.RGBA {
	bg := page(t, showW, showH)
	f := Faces{Heard: face(t, 35), Reply: face(t, 45)}
	var s Screen
	start := time.Date(2026, 10, 21, 10, 42, 0, 0, time.UTC)
	dst := image.NewRGBA(bg.Rect)
	var at image.Rectangle
	for n := range 60 {
		copy(dst.Pix, bg.Pix)
		tr := Turn{Phase: "lingering", Heard: "what's the weather", Reply: reply, Said: said}
		if n < 30 {
			tr.Phase = "replying"
		}
		at = s.Draw(dst, start.Add(time.Duration(n)*66*time.Millisecond), tr, f)
	}
	return crop(dst, at)
}

// crop is a copy of r of img.
func crop(img *image.RGBA, r image.Rectangle) *image.RGBA {
	out := image.NewRGBA(r)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		copy(out.Pix[(y-r.Min.Y)*out.Stride:], img.Pix[img.PixOffset(r.Min.X, y):img.PixOffset(r.Max.X, y)])
	}
	return out
}

// The card painted again for a scroll of a long answer alone, as it is on most frames while one is
// said: the rows of its window. BenchmarkCardPaint3 is the whole card, for comparison.
func BenchmarkCardScroll3(b *testing.B) {
	was := Workers
	Workers = 3
	defer func() { Workers = was }()
	bg := page(b, showW, showH)
	f := Faces{Heard: face(b, 35), Reply: face(b, 45)}
	var s Screen
	start := time.Now()
	dst := image.NewRGBA(bg.Rect)
	var when time.Time
	for n := 0; float64(n)*0.066 < 10; n++ {
		at := float64(n) * 0.066
		when = start.Add(time.Duration(at * float64(time.Second)))
		copy(dst.Pix, bg.Pix)
		s.Draw(dst, when, Turn{Phase: "replying", Level: 0.5, Heard: "what's the weather tomorrow", Reply: longReply, Said: at / 40}, f)
	}
	if !s.card.cur.scrolls() {
		b.Fatal("the answer does not scroll")
	}
	g := s.geometryFor(showW, showH)
	b.ResetTimer()
	for range b.N {
		s.card.offset = -1
		s.card.draw(dst, g, when)
	}
}

// An answer far past cardMost lines is cut in bounded work, however long it is: the last line was cut
// from the whole rest of the answer, a rune at a time, which took seconds on a device for an AI
// assistant's few thousand characters. A single word too long for the line is cut quickly as well.
func TestAHugeAnswerIsCutQuickly(t *testing.T) {
	g := newGeometry(960, 480)
	f := Faces{Heard: face(t, 26), Reply: face(t, 34)}
	w := int(cardW*g.k - 2*cardPad*g.k)
	huge := strings.Repeat(longReply+" ", 400) // about 160,000 characters
	start := time.Now()
	lines := wrapLines(f.Reply, huge, w, cardMost)
	link := clipLine(f.Reply, "https://"+strings.Repeat("x", 100000), w)
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("cutting took %v", took)
	}
	if len(lines) != cardMost || !strings.HasSuffix(lines[cardMost-1], "…") {
		t.Fatalf("%d lines, the last %q", len(lines), lines[len(lines)-1])
	}
	for _, l := range append(lines, link) {
		if got := fonts.MeasureString(f.Reply, l).Ceil(); got > w {
			t.Errorf("%q is %d wide, more than %d", l, got, w)
		}
	}
	if !strings.HasSuffix(link, "…") || len(link) < 10 {
		t.Errorf("the long word was cut to %q", link)
	}
}

// Said is a share of the whole answer, and the window goes over the lines shown of it: for an answer
// cut at cardMost lines the window reaches the last of them when the voice does, not when the voice
// is done with everything after them as well.
func TestTheWindowKeepsUpWithTheVoiceOnACutAnswer(t *testing.T) {
	g := newGeometry(960, 480)
	f := Faces{Heard: face(t, 26), Reply: face(t, 34)}
	huge := strings.Repeat(longReply+" ", 20)
	w := newCardWords(g, f, "tell me everything about the weather", huge, true, "k")
	shown := len(w.chars) - 1
	for _, c := range w.chars {
		shown += c
	}
	all := len([]rune(strings.Join(strings.Fields(huge), " ")))
	if want := float64(all) / float64(shown); math.Abs(w.whole-want) > 1e-9 || w.whole < 2 {
		t.Fatalf("the answer is %.2f times what is shown, want %.2f", w.whole, want)
	}
	end := float64(len(w.chars) - cardLines)
	if got := scrollFor(w.chars, cardLines, float64(shown)/float64(all)*w.whole); math.Abs(got-end) > 1e-6 {
		t.Errorf("the voice at the end of the last line shown: %.2f lines down, want %.0f", got, end)
	}
	if got := scrollFor(w.chars, cardLines, 0.5*float64(shown)/float64(all)*w.whole); got < end/3 || got > end*2/3 {
		t.Errorf("the voice half through what is shown: %.2f lines down of %.0f", got, end)
	}
	// An answer laid out whole goes by its share as it is.
	if whole := newCardWords(g, f, "what's the weather tomorrow", longReply, true, "k").whole; whole != 1 {
		t.Errorf("an answer laid out whole is %.3f times what is shown", whole)
	}
}
