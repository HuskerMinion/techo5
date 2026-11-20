package fonts

import (
	"testing"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

// Every font on offer is in the binary, reads, and has every character of the screen's own words
// (the clock, the date and the weather in the six languages it speaks, its punctuation), so the
// fallback is for what Home Assistant or a song might send, not for the screen's own text.
func TestEveryFamilyReadsAndHasTheScreensCharacters(t *testing.T) {
	const words = "0123456789:APMapm ABCDEFGHIJKLMNOPQRSTUVWXYZ abcdefghijklmnopqrstuvwxyz .,;!?'\"()-/%+&@#" +
		"°·…–—‘’“”‹›× ÉàáäéêìíñöûüÄÖÜßçèëïîôœÀÂÇÈÊËÎÏÔÙÛŸ ŽžŁłŚśŹźŻżĆćŃńĄąĘęČčŘřŠšŤťŮů"
	for _, fam := range Families() {
		for _, bold := range []bool{false, true} {
			f, err := Font(fam.Value, bold)
			if err != nil {
				t.Fatalf("%s: %v", fam.Name, err)
			}
			for _, r := range words {
				if i, err := f.GlyphIndex(nil, r); err != nil || i == 0 {
					t.Errorf("%s (bold %v) has no %q", fam.Name, bold, r)
				}
			}
		}
	}
}

// goAlone is whether f is Go's own face, watched for emoji only (emoji.go).
func goAlone(f font.Face) bool {
	w, ok := f.(watched)
	if !ok {
		return false
	}
	_, ok = w.Face.(*opentype.Face)
	return ok
}

// Go is Go's face alone, as the screen drew before there was a choice; a name not on offer is Go.
func TestGoIsGoAlone(t *testing.T) {
	for _, v := range []string{Go, "no such font"} {
		f, err := Face(v, true, 40)
		if err != nil {
			t.Fatal(err)
		}
		if !goAlone(f) {
			t.Errorf("%q made a %T, not Go's own face", v, f)
		}
	}
	if Index("no such font") != 0 || Known("no such font") || !Known("inter") || Names()[Index("inter")] != "Inter" {
		t.Error("the choices are not looked up right")
	}
}

// A character the chosen font has no glyph for is drawn in Go, at the size asked for; one it has is
// its own. Kerning stops at a change of font, and the lines are spaced as Go's are.
func TestMissingCharactersFallBackToGo(t *testing.T) {
	const size = 40
	own, err := Font("inter", false)
	if err != nil {
		t.Fatal(err)
	}
	if i, _ := own.GlyphIndex(nil, '♪'); i != 0 {
		t.Fatal("Inter has a ♪ now: pick another character this test can use")
	}
	face, err := Face("inter", false, size)
	if err != nil {
		t.Fatal(err)
	}
	goFont, _ := Font(Go, false)
	goFace, _ := opentype.NewFace(goFont, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
	interFace, _ := opentype.NewFace(own, &opentype.FaceOptions{Size: size * families[Index("inter")].scale, DPI: 72, Hinting: font.HintingFull})

	got, ok := face.GlyphAdvance('♪')
	want, _ := goFace.GlyphAdvance('♪')
	if !ok || got != want {
		t.Errorf("♪ is %v wide (ok %v); Go's is %v", got, ok, want)
	}
	if _, mask, _, _, ok := face.Glyph(fixed.P(10, 40), '♪'); !ok || mask == nil {
		t.Error("♪ drew nothing")
	}
	got, _ = face.GlyphAdvance('W')
	want, _ = interFace.GlyphAdvance('W')
	if got != want {
		t.Errorf("W is %v wide; Inter's own is %v", got, want)
	}
	if k := face.Kern('A', '♪'); k != 0 {
		t.Errorf("kerned %v across two fonts", k)
	}
	if k, w := face.Kern('A', 'V'), interFace.Kern('A', 'V'); k != w {
		t.Errorf("A V kerned %v; Inter kerns it %v", k, w)
	}
	if face.Metrics() != goFace.Metrics() {
		t.Errorf("line metrics %+v, not Go's %+v", face.Metrics(), goFace.Metrics())
	}
	if s, ok := face.(*Stack); !ok || !s.Has('♪') || !s.Has('é') {
		t.Error("the stack does not say it has what Go has")
	}
}

// A layer that breaks while the second character of a pair is looked up is dropped, and the first
// character's layer is still the one it was: kerning is not read from the layer that took its place.
func TestKernAfterALayerIsDropped(t *testing.T) {
	goFont, _ := Font(Go, false)
	inter, _ := Font("inter", false)
	a, _ := opentype.NewFace(inter, &opentype.FaceOptions{Size: 30, DPI: 72})
	b, _ := opentype.NewFace(goFont, &opentype.FaceOptions{Size: 30, DPI: 72})

	broken := &sfnt.Font{} // has no x, and breaks on ♪
	saved := glyphIndex
	t.Cleanup(func() { glyphIndex = saved })
	glyphIndex = func(f *sfnt.Font, buf *sfnt.Buffer, r rune) (sfnt.GlyphIndex, error) {
		if f != broken {
			return saved(f, buf, r)
		}
		if r == '♪' {
			panic("broken")
		}
		return 0, nil
	}
	// x is Inter's, ♪ Go's (Inter has none): two layers, so no kerning between them.
	s := NewStack(nil, Layer{Face: a, Font: broken}, Layer{Face: a, Font: inter}, Layer{Face: kernFace{b}, Font: goFont})
	if k := s.Kern('x', '♪'); k != 0 {
		t.Errorf("kerning %v between two layers' characters, read from the layer after the one dropped", k)
	}
	if len(s.layers) != 2 {
		t.Errorf("%d layers, want the broken one dropped", len(s.layers))
	}
}

// kernFace kerns every pair, so kerning read from it shows.
type kernFace struct{ font.Face }

func (kernFace) Kern(rune, rune) fixed.Int26_6 { return 99 }

// A stack takes each character from the first layer that has it, and a layer with no font has every
// one: the order is the choice, so another script's font can go in ahead of Go.
func TestStackOrder(t *testing.T) {
	goFont, _ := Font(Go, false)
	inter, _ := Font("inter", false)
	a, _ := opentype.NewFace(inter, &opentype.FaceOptions{Size: 30, DPI: 72})
	b, _ := opentype.NewFace(goFont, &opentype.FaceOptions{Size: 30, DPI: 72})
	last, _ := opentype.NewFace(goFont, &opentype.FaceOptions{Size: 60, DPI: 72})

	s := NewStack(nil, Layer{Face: a, Font: inter}, Layer{Face: b, Font: goFont}, Layer{Face: last})
	if g, _ := s.GlyphAdvance('x'); g != adv(a, 'x') {
		t.Error("x was not the first layer's")
	}
	if g, _ := s.GlyphAdvance('♪'); g != adv(b, '♪') {
		t.Error("♪ was not the second layer's")
	}
	// Neither font has it; the catch-all draws it.
	if g, _ := s.GlyphAdvance('\U0001F600'); g != adv(last, '\U0001F600') {
		t.Error("a character nobody has was not the catch-all's")
	}
	if s.Metrics() != a.Metrics() {
		t.Error("with no metrics face, the first layer's metrics are not the stack's")
	}

	// Without a catch-all, the first layer draws what nobody has, as it would alone.
	s = NewStack(nil, Layer{Face: a, Font: inter}, Layer{Face: b, Font: goFont})
	if s.Has('\U0001F600') {
		t.Error("says it has a character neither font has")
	}
	if g, _ := s.GlyphAdvance('\U0001F600'); g != adv(a, '\U0001F600') {
		t.Error("a character nobody has was not the first layer's")
	}
	if err := s.Close(); err != nil {
		t.Error(err)
	}
	if _, _, _, _, ok := NewStack(nil).Glyph(fixed.Point26_6{}, 'x'); ok {
		t.Error("an empty stack drew something")
	}
}

func adv(f font.Face, r rune) fixed.Int26_6 {
	a, _ := f.GlyphAdvance(r)
	return a
}

// Each font's scale keeps a line of words about as wide as Go draws it, so what fits on the screen in
// Go fits in any of them: no line wider than Go by more than a few percent (a short one like "PM" is
// at the mercy of its letters), and not much narrower on average (else the letters are small too).
func TestTheScaleKeepsLinesGosWidth(t *testing.T) {
	lines := []struct {
		bold bool
		size float64
		text string
	}{
		{true, 230, "10:08"},
		{true, 230, "12:45"},
		{true, 56, "PM"},
		{true, 48, "Settings"},
		{false, 42, "Wednesday, September 16"},
		{false, 34, "72° · Partly cloudy"},
		{false, 29, "Brightness, night, clock and photos"},
		{false, 26, "Mittwoch, 16. Oktober · Žluťoučký"},
	}
	for _, fam := range Families()[1:] {
		var sum float64
		for _, l := range lines {
			goFace, _ := Face(Go, l.bold, l.size)
			face, err := Face(fam.Value, l.bold, l.size)
			if err != nil {
				t.Fatal(err)
			}
			ratio := float64(font.MeasureString(face, l.text)) / float64(font.MeasureString(goFace, l.text))
			sum += ratio
			if ratio > 1.08 {
				t.Errorf("%s draws %q %.0f%% wider than Go", fam.Name, l.text, 100*(ratio-1))
			}
		}
		if mean := sum / float64(len(lines)); mean < 0.9 || mean > 1.03 {
			t.Errorf("%s's lines are %.2f of Go's width on average", fam.Name, mean)
		}
	}
}
