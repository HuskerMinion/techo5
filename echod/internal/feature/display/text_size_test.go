//go:build !dot

package display

import (
	"path/filepath"
	"testing"

	"golang.org/x/image/font"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/fonts"
)

// A new device draws at the board's own size; choosing a step saves it, tells Home Assistant, and is
// what the setting row and the picker show; an index past the list changes nothing; and a size from
// elsewhere is shown as the step nearest it while drawn as it is.
func TestChoosingTheTextSize(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	d := &Display{}
	d.textSizeSel = textSizeSelect(d)
	if textSize() != layout.DefaultTextSize() || textSteps[textSizeIndex()] != layout.DefaultTextSize() {
		t.Fatalf("a new device draws at %d%% (step %d), want the board's %d%%", textSize(), textSizeIndex(), layout.DefaultTextSize())
	}
	if len(textSizeOptions()) != len(textSteps) || textSizeOptions()[0] != "70%" {
		t.Errorf("the options are %q", textSizeOptions())
	}
	last := len(textSteps) - 1
	d.setTextSize(last)
	if got := config.Get().Screen.TextSize; got != 125 {
		t.Errorf("saved %d", got)
	}
	if textSizeRow().value != "125%" || textSizeRow().id != "textsize" {
		t.Errorf("the row is %+v", textSizeRow())
	}
	if p, ok := textSizePicker(); !ok || p.cur != last || len(p.opts) != len(textSteps) {
		t.Errorf("the picker is %+v", p)
	}
	d.setTextSize(len(textSteps))
	if textSize() != 125 {
		t.Error("an index past the list changed the text size")
	}
	// From Home Assistant, by the option's name.
	d.textSizeSel.OnCommand("80%")
	if textSize() != 80 {
		t.Errorf("Home Assistant's choice left %d%%", textSize())
	}
	if err := config.Set().Screen().TextSize(84); err != nil {
		t.Fatal(err)
	}
	if textSize() != 84 || textSizeOptions()[textSizeIndex()] != "80%" {
		t.Errorf("a size between the steps draws at %d%% and shows %q", textSize(), textSizeOptions()[textSizeIndex()])
	}
}

// Every face for the screen's words is made at the text size: the renderer's own, the settings
// screen's, the dashboard tiles' and the clock styles'. Icons are not words and keep theirs.
func TestEveryFaceFollowsTheTextSize(t *testing.T) {
	height := func(f font.Face) int { return f.Metrics().Height.Round() }
	at := func(size int) *paint {
		p := &paint{sNum: 1, sDen: 1}
		p.font, p.textSize = fonts.Go, size
		return p
	}
	small, big := at(80), at(125)
	if a, b := height(small.textFace(false, 40)), height(big.textFace(false, 40)); a >= b {
		t.Errorf("a tile's face is %d tall at 80%% and %d at 125%%", a, b)
	}
	if a, b := height(facesFor(small.faceKey()).label), height(facesFor(big.faceKey()).label); a >= b {
		t.Errorf("the settings screen's label is %d tall at 80%% and %d at 125%%", a, b)
	}
	if a, b := height(small.iconFace(40)), height(big.iconFace(40)); a != b {
		t.Errorf("an icon is %d tall at 80%% and %d at 125%%", a, b)
	}
	if a, b := height(fontKey{fonts.Go, 0, 100}.face(false, 40)), height(fontKey{fonts.Go, 0, 0}.face(false, 40)); a != b {
		t.Errorf("a key with no text size draws at %d, not 100%%'s %d", b, a)
	}
	if got := big.atTextSize(40); got != 50 {
		t.Errorf("40 at 125%% is %d", got)
	}
}
