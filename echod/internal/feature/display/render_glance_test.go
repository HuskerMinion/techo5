//go:build !dot && !spot

package display

import (
	"bytes"
	"image"
	"path/filepath"
	"slices"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

func TestFitChipsSkipsOnlyTheOneThatDoesNotFit(t *testing.T) {
	for _, c := range []struct {
		name      string
		widths    []int
		room      int
		keep      []int
		wantTotal int
	}{
		{"all fit", []int{200, 200}, 744, []int{0, 1}, 412},
		{"a long one in the middle is skipped, the next still shows", []int{300, 900, 200}, 744, []int{0, 2}, 512},
		{"a long first one does not end the strip", []int{900, 200, 200}, 744, []int{1, 2}, 412},
		{"nothing fits", []int{900}, 744, nil, 0},
	} {
		keep, total := fitChips(c.widths, 12, c.room)
		if !slices.Equal(keep, c.keep) || total != c.wantTotal {
			t.Errorf("%s: kept %v (%d wide), want %v (%d)", c.name, keep, total, c.keep, c.wantTotal)
		}
	}
}

// Glance chip size keeps the strip to one face: Automatic is the small one while every chip fits in
// it, and the tiny one when that fits more; Small never goes tiny, so a chip that does not fit drops
// off; Tiny is tiny even when everything would fit in small.
func TestGlanceChipSize(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	previewFont(t)
	t.Cleanup(func() { _ = config.Set().Screen().GlanceChipSize("") })
	draw := func(size string, chips []home.Chip) *image.RGBA {
		t.Helper()
		if err := config.Set().Screen().GlanceChipSize(size); err != nil {
			t.Fatal(err)
		}
		img := image.NewRGBA(image.Rect(0, 0, showWide, showHigh))
		newRenderer(img).glanceStrip(chips, false)
		return img
	}
	same := func(a, b *image.RGBA) bool { return bytes.Equal(a.Pix, b.Pix) }

	few := []home.Chip{{Icon: "mdi:door", Text: "Back door open"}}
	small := draw("small", few)
	if !same(draw("", few), small) {
		t.Error("Automatic with room to spare is not the small face")
	}
	if same(draw("tiny", few), small) {
		t.Error("Tiny drew the small face when everything fit in it")
	}

	// Three chips, their text lengthened until the small face no longer holds them all but the tiny one
	// does.
	var many []home.Chip
	var tiny *image.RGBA
	const text = "Upstairs windows open"
	for n := 8; n <= len(text) && tiny == nil; n++ {
		many = nil
		for range 3 {
			many = append(many, home.Chip{Icon: "mdi:door", Text: text[:n]})
		}
		if !same(draw("", many), draw("small", many)) {
			tiny = draw("tiny", many)
		}
	}
	if tiny == nil {
		t.Fatal("Automatic never went tiny to fit more chips")
	}
	if !same(draw("", many), tiny) {
		t.Error("Automatic is not the tiny face when that fits more")
	}
	if same(draw("small", many), tiny) {
		t.Error("Small went tiny to fit more chips")
	}
}
