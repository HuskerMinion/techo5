package main

import (
	"image"
	"image/color"
	"testing"
)

func frame(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = 40
	}
	return img
}

// The first frame goes whole; an unchanged one sends nothing; a change sends only its own tiles.
func TestChangesAreOnlyWhatChanged(t *testing.T) {
	d := &differ{}
	a := frame(320, 160)
	if got := d.changes(a); len(got) != 1 || got[0].img.Bounds().Dx() != 320 {
		t.Fatalf("first frame: %d patches, want the whole frame", len(got))
	}
	if got := d.changes(frame(320, 160)); len(got) != 0 {
		t.Fatalf("an unchanged frame sent %d patches", len(got))
	}
	b := frame(320, 160)
	b.Set(5, 5, color.RGBA{255, 0, 0, 255})
	got := d.changes(b)
	if len(got) != 1 {
		t.Fatalf("one changed pixel: %d patches", len(got))
	}
	if r := got[0].img.Bounds(); r != image.Rect(0, 0, tile, tile) {
		t.Errorf("one changed pixel sent %v, want its tile", r)
	}
}

// Changes far apart go as separate pictures, near ones as one.
func TestChangesFarApartStayApart(t *testing.T) {
	d := &differ{}
	d.changes(frame(320, 160))
	b := frame(320, 160)
	b.Set(2, 2, color.RGBA{255, 0, 0, 255})
	b.Set(300, 150, color.RGBA{255, 0, 0, 255})
	b.Set(20, 2, color.RGBA{255, 0, 0, 255})
	if got := d.changes(b); len(got) != 2 {
		t.Fatalf("changes at two corners: %d patches, want 2", len(got))
	}
}

func TestShrinkAverages(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{0, 0, 0, 255})
	img.Set(1, 0, color.RGBA{100, 0, 0, 255})
	img.Set(0, 1, color.RGBA{100, 0, 0, 255})
	img.Set(1, 1, color.RGBA{200, 0, 0, 255})
	out := shrink(img)
	if out.Bounds().Dx() != 1 || out.RGBAAt(0, 0).R != 100 {
		t.Errorf("shrink = %v %v, want one pixel of 100", out.Bounds(), out.RGBAAt(0, 0))
	}
}

// A path is its panel, and nothing that could walk anywhere else.
func TestFirstPart(t *testing.T) {
	for path, want := range map[string]string{
		"/lovelace/0": "lovelace", "/home-refresh/lights": "home-refresh", "energy": "energy",
		"/config/dashboard": "config", "/../config": "", "//evil.example/x": "", "/": "", `\config`: "",
		"/lovelace/%2e%2e/config": "", "/lovelace/.	./config": "", "/lovelace/./x": "", "/lights?edit=1": "",
		"/lovelace/ 0": "", "": "",
	} {
		got, ok := firstPart(path)
		if got != want || ok != (want != "") {
			t.Errorf("firstPart(%q) = %q %v, want %q", path, got, ok, want)
		}
	}
}

