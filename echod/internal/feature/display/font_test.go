//go:build !dot

package display

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/fonts"
)

// Choosing a font saves it, tells Home Assistant, and is what the setting row and the picker show; an
// index past the list changes nothing, and a font this build does not have reads as Go.
func TestChoosingTheFont(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	d := &Display{}
	d.fontSel = fontSelect(d)
	if screenFont() != fonts.Go || fontRow().value != "Go" {
		t.Fatalf("a new device draws in %q (%q)", screenFont(), fontRow().value)
	}
	i := fonts.Index("comicneue")
	d.setFont(i)
	if got := config.Get().Screen.Font; got != "comicneue" {
		t.Errorf("saved %q", got)
	}
	if fontRow().value != "Comic Neue" {
		t.Errorf("the row says %q", fontRow().value)
	}
	if p, ok := fontPicker(); !ok || p.cur != i || len(p.opts) != len(fonts.Families()) {
		t.Errorf("the picker is %+v", p)
	}
	d.setFont(len(fonts.Families()))
	if screenFont() != "comicneue" {
		t.Error("an index past the list changed the font")
	}
	// From Home Assistant, by the option's name.
	d.fontSel.OnCommand("Open Sans")
	if screenFont() != "opensans" {
		t.Errorf("Home Assistant's choice left %q", screenFont())
	}
	if err := config.Set().Screen().Font("from-a-newer-build"); err != nil {
		t.Fatal(err)
	}
	if screenFont() != fonts.Go || fontIndex() != 0 {
		t.Error("an unknown font is not Go")
	}
}

// The settings screen's faces are kept per font and per generation of one's own fonts: a change of
// either is a new set, and asking again is the same set.
func TestSheetFacesPerFont(t *testing.T) {
	a := facesFor(fontKey{fonts.Go, 0, 100})
	b := facesFor(fontKey{"inter", 0, 100})
	if a.label == b.label {
		t.Error("Inter's settings faces are Go's")
	}
	if facesFor(fontKey{"inter", 0, 100}).label != b.label {
		t.Error("asking again made the faces again")
	}
	if facesFor(fontKey{"inter", 1, 100}).label == b.label {
		t.Error("a new generation kept the old faces")
	}
}

// ownFontDir points the fonts folder at a fresh one holding Comic Neue's regular cut, for a test.
func ownFontDir(t *testing.T) {
	t.Helper()
	old := fonts.Dir
	fonts.Dir = filepath.Join(t.TempDir(), "fonts")
	t.Cleanup(func() { fonts.Dir = old; fonts.Rescan() })
	if err := os.MkdirAll(fonts.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join("..", "..", "lib", "fonts", "files", "comicneue", "Regular.ttf"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := fonts.AddOwn("extra.ttf", f); err != nil {
		t.Fatal(err)
	}
}

// previewFont draws a preview test's pages in the font PREVIEW_FONT names (lib/fonts' value, such as
// "inter"), with the fonts of one's own in the folder PREVIEW_FONTS_DIR names, when they are set, so
// each font's pages can be looked at as SHOW_PREVIEW and SPOT_PREVIEW write them.
func previewFont(t *testing.T) {
	t.Helper()
	v, dir := os.Getenv("PREVIEW_FONT"), os.Getenv("PREVIEW_FONTS_DIR")
	if v == "" && dir == "" {
		return
	}
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Screen().Font(v); err != nil {
		t.Fatal(err)
	}
	if dir != "" {
		old := fonts.Dir
		fonts.Dir = dir
		t.Cleanup(func() { fonts.Dir = old; fonts.Rescan() })
	}
	fonts.Rescan()
}

// The big dashboard tiles' faces of a font no longer in use are let go when the new font's are made,
// so a font file of one's own removed or replaced is not kept open by them.
func TestTileFacesOfAnOldFontGo(t *testing.T) {
	p := &paint{sNum: 1, sDen: 1}
	p.font, p.fontGen = "inter", 7
	if p.textFace(false, 40) == nil || p.textFace(true, 50) == nil {
		t.Fatal("no face")
	}
	p.fontGen = 8
	p.textFace(false, 40)
	textFaces.Range(func(k, _ any) bool {
		if k.(textKey).font != (fontKey{"inter", 8, 0}) {
			t.Errorf("a face of %+v is still kept", k)
		}
		return true
	})
}
