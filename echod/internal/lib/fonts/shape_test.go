package fonts

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"testing"

	"golang.org/x/image/font"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

// khmerFace is a Face with a Khmer font of one's own, from TECHO5_KHMER_FONT (Noto Sans Khmer, say:
// https://github.com/notofonts/khmer); the test is skipped without one, as no Khmer font ships here.
func khmerFace(t *testing.T, size float64) font.Face {
	t.Helper()
	path := os.Getenv("TECHO5_KHMER_FONT")
	if path == "" {
		t.Skip("TECHO5_KHMER_FONT is not set")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	dir := ownDir(t)
	write(t, dir, "1-khmer.ttf", b)
	Rescan()
	face, err := Face(families[0].Value, false, size)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := face.(*Stack); !ok {
		t.Fatalf("Face is %T, not a Stack with the Khmer font", face)
	}
	return face
}

const khmerWord = "ខ្លោងទ្វាមុខផ្ទះ" // "the front gate", as a camera is named

func TestLatinIsDrawnAsBefore(t *testing.T) {
	face := khmerFace(t, 24)
	for _, s := range []string{"", "Front gate 23:41", "Café ♪ Привет"} {
		if runs(face, s) != nil {
			t.Errorf("%q was split for shaping", s)
		}
		if got, want := MeasureString(face, s), font.MeasureString(face, s); got != want {
			t.Errorf("MeasureString(%q) = %v, font.MeasureString %v", s, got, want)
		}
	}
}

func TestKhmerIsShaped(t *testing.T) {
	face := khmerFace(t, 24)
	rs := runs(face, khmerWord)
	if len(rs) != 1 || rs[0].shape == nil {
		t.Fatalf("runs(%q) = %+v, want one shaped run", khmerWord, rs)
	}
	sh := rs[0].shape
	// Drawn a character at a time, each character is its cmap glyph. Shaped, the consonants after
	// the coeng sign become subscript forms and the coeng itself no glyph of its own, so the glyphs
	// are not that sequence.
	var buf sfnt.Buffer
	var naive []sfnt.GlyphIndex
	for _, r := range khmerWord {
		g, _ := sh.font.GlyphIndex(&buf, r)
		naive = append(naive, g)
	}
	same := len(naive) == len(sh.glyphs)
	for i := 0; same && i < len(naive); i++ {
		same = naive[i] == sfnt.GlyphIndex(sh.glyphs[i].GlyphID)
	}
	if same {
		t.Errorf("shaping %q gave the character-at-a-time glyphs %v", khmerWord, naive)
	}
	if MeasureString(face, khmerWord) <= 0 {
		t.Error("shaped text has no advance")
	}
	b, adv := BoundString(face, khmerWord)
	if b.Empty() || adv != MeasureString(face, khmerWord) {
		t.Errorf("BoundString = %v, %v", b, adv)
	}
}

func TestMixedRuns(t *testing.T) {
	face := khmerFace(t, 24)
	s := khmerWord + " Main"
	rs := runs(face, s)
	if len(rs) != 2 || rs[0].shape == nil || rs[1].shape != nil || rs[1].text != " Main" {
		t.Fatalf("runs(%q) = %+v", s, rs)
	}
	want := rs[0].shape.advance + font.MeasureString(face, " Main")
	if got := MeasureString(face, s); got != want {
		t.Errorf("MeasureString = %v, want %v", got, want)
	}
}

// TestDrawKhmer draws the word shaped and a character at a time, one above the other, to the PNG
// named by TECHO5_SHAPE_PNG for a person to look at.
func TestDrawKhmer(t *testing.T) {
	face := khmerFace(t, 40)
	img := image.NewRGBA(image.Rect(0, 0, 520, 140))
	draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)
	ink := image.NewUniform(color.Black)
	s := khmerWord + " Main"
	end := DrawString(img, ink, face, fixed.P(10, 50), s)
	if end.X <= fixed.I(10) {
		t.Errorf("DrawString advanced to %v", end)
	}
	(&font.Drawer{Dst: img, Src: ink, Face: face, Dot: fixed.P(10, 120)}).DrawString(s)
	var ink0 bool
	for _, p := range img.Pix[:len(img.Pix)/2] {
		if p != 0xff {
			ink0 = true
			break
		}
	}
	if !ink0 {
		t.Error("nothing was drawn")
	}
	if out := os.Getenv("TECHO5_SHAPE_PNG"); out != "" {
		f, err := os.Create(out)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
	}
}
