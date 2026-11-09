//go:build !dot && !spot

package display

import (
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/lib/fonts"
)

// A Khmer reply has no spaces between its words: it still wraps inside the screen, at word
// boundaries, instead of running off the right edge. With SHOW_PREVIEW set, the page is written there.
func TestKhmerReplyWraps(t *testing.T) {
	fonts.Rescan()
	heard := "សីតុណ្ហភាពក្នុងផ្ទះប៉ុន្មាន?"
	// As a voice assistant writes it: whole sentences with no space at all, each wider than the screen.
	reply := "សីតុណ្ហភាពក្នុងបន្ទប់គឺ26.1អង្សាសេនិងសំណើម59ភាគរយហើយឡានរបស់បងនៅសល់ថ្ម96ភាគរយហើយកំពុងសាកនៅផ្ទះ។ ម៉ាស៊ីនត្រជាក់បានបើកហើយកំដៅ25អង្សា។"
	img := image.NewRGBA(image.Rect(0, 0, showWide, showHigh))
	r := newRenderer(img)
	draw.Draw(img, img.Bounds(), image.NewUniform(walnut), image.Point{}, draw.Src)
	maxW := r.w - 2*r.margin
	for _, face := range []struct {
		name  string
		lines []string
		w     func(string) int
	}{
		{"body", r.wrap(r.body, reply, maxW), func(s string) int { return r.width(r.body, s) }},
		{"small", r.wrap(r.small, reply, maxW), func(s string) int { return r.width(r.small, s) }},
	} {
		if len(face.lines) < 2 {
			t.Errorf("%s: reply on %d line(s), want it wrapped", face.name, len(face.lines))
		}
		for _, l := range face.lines {
			if w := face.w(l); w > maxW {
				t.Errorf("%s: line %q is %d px, wider than %d", face.name, l, w, maxW)
			}
		}
	}
	r.words(heard, reply, 0, r.s(40))
	if dir := os.Getenv("SHOW_PREVIEW"); dir != "" {
		f, err := os.Create(filepath.Join(dir, "reply-khmer.png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
	}
}
