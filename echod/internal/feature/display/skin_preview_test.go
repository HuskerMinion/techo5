//go:build !dot && !spot

package display

import (
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// PREVIEW_SKIN draws every preview in that skin: its palette, font and backdrops. Its filter, which the
// panel lays over what is drawn, is laid over the saved previews by TestSkinFilterPreviews.
func init() {
	if v := os.Getenv("PREVIEW_SKIN"); v != "" {
		skinPreview = v
		applyTheme(current())
	}
}

// TestSkinFilterPreviews lays PREVIEW_SKIN's filter over every PNG in SKIN_FILTER_DIR, in place, as the
// panel would show them.
func TestSkinFilterPreviews(t *testing.T) {
	dir := os.Getenv("SKIN_FILTER_DIR")
	if dir == "" || skinPreview == "" {
		t.Skip("set SKIN_FILTER_DIR and PREVIEW_SKIN")
	}
	names, _ := filepath.Glob(filepath.Join(dir, "*.png"))
	for _, name := range names {
		if strings.HasSuffix(name, "-sheet.png") {
			continue
		}
		f, err := os.Open(name)
		if err != nil {
			t.Fatal(err)
		}
		src, err := png.Decode(f)
		f.Close()
		if err != nil {
			continue
		}
		b := src.Bounds()
		img := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
		draw.Draw(img, img.Rect, src, b.Min, draw.Src)
		filter := skinFilterFor(b.Dx(), b.Dy())
		for y := 0; filter != nil && y < b.Dy(); y++ {
			filter(img.Pix[y*img.Stride:y*img.Stride+b.Dx()*4], y)
		}
		out, err := os.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(out, img); err != nil {
			t.Fatal(err)
		}
		out.Close()
	}
}
