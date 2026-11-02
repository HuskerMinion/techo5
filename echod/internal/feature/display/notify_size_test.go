//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
	"github.com/HuskerMinion/techo5/echod/internal/feature/notify"
)

var bigPanels = []struct {
	name       string
	wide, high int
}{{"960x480", showWide, showHigh}, {"1280x800", show8Wide, show8High}}

var (
	colorRed   = color.RGBA{0xf4, 0x43, 0x36, 255}
	colorAmber = color.RGBA{0xff, 0xc1, 0x07, 255}
)

func writePNG(t *testing.T, dir, name string, img image.Image) {
	t.Helper()
	if dir == "" {
		return
	}
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

// A short message at huge fills most of the screen's width; every size keeps its words inside its card,
// however long they are, and large and huge cards are bigger than the usual one. With SHOW_PREVIEW set,
// a huge pop-up is written there.
func TestPopupSizes(t *testing.T) {
	dir := os.Getenv("SHOW_PREVIEW")
	at := time.Date(2026, 10, 4, 14, 7, 0, 0, time.Local)
	long := "The home server is not answering and the backups have not run since Tuesday night, please check it"
	for _, panel := range bigPanels {
		r := newRenderer(image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high)))
		r.draw(scene{now: at, phase: "idle"}) // sets the faces up
		usual := r.notifyBox()
		prev := usual.Dx() * usual.Dy()
		for _, size := range []notify.Size{notify.SizeLarge, notify.SizeHuge} {
			box := r.sizedBox(size)
			if box.Dx()*box.Dy() <= prev || !box.In(image.Rect(0, 0, panel.wide, panel.high)) {
				t.Errorf("%s %q: card %v is not bigger than %v or leaves the screen", panel.name, size, box, usual)
			}
			prev = box.Dx() * box.Dy()
			in := box.Dx() - 2*r.rowIn()
			for _, text := range []string{"Disk full", long, "Supercalifragilisticexpialidocious_and_more_without_a_break"} {
				lines, face, lineH := r.fitWords(true, text, in, box.Dy()/2, popupSizes(size))
				if face == nil || len(lines) == 0 {
					t.Fatalf("%s %q %q: nothing fits", panel.name, size, text)
				}
				if len(lines)*lineH > box.Dy()/2 || r.widest(face, lines) > in {
					t.Errorf("%s %q %q: %d lines of %d px, widest %d, overflow %v", panel.name, size, text, len(lines), lineH, r.widest(face, lines), box)
				}
			}
		}
		huge := r.sizedBox(notify.SizeHuge)
		lines, face, _ := r.fitWords(true, "Disk full", huge.Dx()-2*r.rowIn(), huge.Dy()/2, popupSizes(notify.SizeHuge))
		if len(lines) != 1 || r.width(face, lines[0])*100/panel.wide < 55 {
			t.Errorf("%s: a short huge message is %d%% of the width on %d lines", panel.name, r.width(face, lines[0])*100/panel.wide, len(lines))
		}

		var imgs [3]*image.RGBA
		for i, size := range []notify.Size{notify.SizeNormal, notify.SizeLarge, notify.SizeHuge} {
			imgs[i] = image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
			newRenderer(imgs[i]).draw(scene{now: at, phase: "idle", showNotification: true,
				notification: notify.Notification{Kind: notify.Text, Title: "Server", Message: "Disk full", Size: size}})
		}
		if same(imgs[0].Pix, imgs[1].Pix) || same(imgs[1].Pix, imgs[2].Pix) {
			t.Errorf("%s: the three sizes look alike", panel.name)
		}
		writePNG(t, dir, "popup-huge-"+panel.name+".png", imgs[2])
		writePNG(t, dir, "popup-large-"+panel.name+".png", imgs[1])
	}
}

// With Screen text size changed, a normal pop-up changes with it: it already follows the setting.
func TestNormalPopupFollowsTextSize(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	at := time.Date(2026, 10, 4, 14, 7, 0, 0, time.Local)
	draw := func() *image.RGBA {
		img := image.NewRGBA(image.Rect(0, 0, showWide, showHigh))
		newRenderer(img).draw(scene{now: at, phase: "idle", showNotification: true,
			notification: notify.Notification{Kind: notify.Text, Title: "Server", Message: "Disk full"}})
		return img
	}
	a := draw()
	if err := config.Set().Screen().TextSize(125); err != nil {
		t.Fatal(err)
	}
	if b := draw(); same(a.Pix, b.Pix) {
		t.Error("the pop-up looks the same at 125%")
	}
}

func warningView(c dashboard.Block) dashboard.Drawn {
	return dashboard.Drawn{Sections: []dashboard.Section{{Blocks: []dashboard.Block{c}}}}
}

// A lone heading, markdown card or tile fills the screen with its color and large words when tiles are
// set to Fill the screen; with anything else on the page, or another size, it is laid out as usual. With
// SHOW_PREVIEW set, red warning pages are written there.
func TestLoneCardFillsTheScreen(t *testing.T) {
	dir := os.Getenv("SHOW_PREVIEW")
	at := time.Date(2026, 10, 4, 14, 7, 0, 0, time.Local)
	head := dashboard.Block{Heading: "HOME SERVER DOWN", Color: colorRed}
	tile := dashboard.Block{Tiles: []dashboard.Tile{{Name: "Server", Icon: "server-off", Value: "Offline", Color: colorRed,
		Tap: &dashboard.Action{Service: "light.toggle", Entity: "light.x"}}}}
	md := dashboard.Block{Title: "Server", Text: []string{"Disk full", "Call for help"}, Color: colorRed}
	for _, panel := range bigPanels {
		for name, blk := range map[string]dashboard.Block{"heading": head, "tile": tile, "markdown": md} {
			img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
			r := newRenderer(img)
			r.draw(scene{now: at, phase: "idle", showDash: true, dashMode: config.DashboardDrawn, drawn: warningView(blk), dashTiles: "fill"})
			for _, p := range []image.Point{{2, 2}, {panel.wide - 3, 2}, {2, panel.high - 3}, {panel.wide - 3, panel.high - 3}} {
				got := img.RGBAAt(p.X, p.Y)
				if got.R != colorRed.R || got.G != colorRed.G || got.B != colorRed.B {
					t.Fatalf("%s %s: corner %v is %v, not red", name, panel.name, p, got)
				}
			}
			minX, maxX := panel.wide, 0
			for y := 0; y < panel.high; y++ {
				for x := 0; x < panel.wide; x++ {
					if c := img.RGBAAt(x, y); c.G > 240 && c.B > 240 {
						minX, maxX = min(minX, x), max(maxX, x)
					}
				}
			}
			if maxX < minX || (name == "heading" && (maxX-minX)*100/panel.wide < 55) {
				t.Errorf("%s %s: the words span %d..%d of %d", name, panel.name, minX, maxX, panel.wide)
			}
			if tiles, _ := r.dash(); name == "tile" && len(tiles) != 1 {
				t.Errorf("the lone tile has %d taps, want 1", len(tiles))
			}
			writePNG(t, dir, "warning-"+name+"-"+panel.name+".png", img)
		}
		// Not Fill the screen: laid out as usual, so the corner is not red.
		img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
		newRenderer(img).draw(scene{now: at, phase: "idle", showDash: true, dashMode: config.DashboardDrawn, drawn: warningView(head)})
		if c := img.RGBAAt(2, panel.high-3); c.R == colorRed.R && c.G == colorRed.G {
			t.Errorf("%s: a heading filled the screen without Fill the screen", panel.name)
		}
		two := dashboard.Drawn{Sections: []dashboard.Section{{Blocks: []dashboard.Block{head, md}}}}
		if _, ok := loneCard(two); ok {
			t.Error("two cards counted as a lone card")
		}
	}
}

func TestOnColorReads(t *testing.T) {
	if c := onColor(colorRed); c.R != 255 {
		t.Errorf("on red: %v, want white", c)
	}
	if c := onColor(colorAmber); c.R > 30 {
		t.Errorf("on amber: %v, want near black", c)
	}
}

// A lone tile with a slider or a switch keeps the usual full-screen tile, with its slider.
func TestLoneLightKeepsItsSlider(t *testing.T) {
	at := time.Date(2026, 10, 4, 14, 7, 0, 0, time.Local)
	light := dashboard.Block{Tiles: []dashboard.Tile{{Entity: "light.x", Name: "Lamp", Icon: "lightbulb", Value: "On", On: true,
		Tap:    &dashboard.Action{Service: "light.toggle", Entity: "light.x"},
		Adjust: &dashboard.Adjust{Entity: "light.x", Kind: "brightness", Value: 50, Min: 0, Max: 100}}}}
	if _, ok := loneCard(warningView(light)); ok {
		t.Fatal("a tile with a slider counted as a lone card")
	}
	light.Tiles[0].Adjust, light.Tiles[0].Switch = nil, true
	if _, ok := loneCard(warningView(light)); ok {
		t.Fatal("a tile with a switch counted as a lone card")
	}
	light.Tiles[0].Switch = false
	if _, ok := loneCard(warningView(light)); !ok {
		t.Fatal("a plain tile is not a lone card")
	}
	light.Tiles[0].Adjust = &dashboard.Adjust{Entity: "light.x", Kind: "brightness", Value: 50, Min: 0, Max: 100}
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	r.draw(scene{now: at, phase: "idle", showDash: true, dashMode: config.DashboardDrawn, drawn: warningView(light), dashTiles: "fill"})
	if tiles, _ := r.dash(); len(tiles) != 1 || tiles[0].adjust == nil {
		t.Errorf("the lone light has %d zones, slider kept: %v", len(tiles), len(tiles) == 1 && tiles[0].adjust != nil)
	}
}
