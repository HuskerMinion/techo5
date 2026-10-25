//go:build !dot

package display

import (
	"io"
	"log/slog"
	"sync"

	esphome "github.com/ygelfand/go-esphome-device"
	"golang.org/x/image/font"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/setup"
	"github.com/HuskerMinion/techo5/echod/internal/lib/fonts"
)

// The screen's font (#116): every word the screen draws, in Go, the font it has always used, or one
// of the others lib/fonts offers. Icons keep their own font. Chosen under Display on the screen, as
// Screen font in Home Assistant, or on the setup page, and shown at once: each renderer looks at the
// choice as it starts a frame and makes its faces again when it has changed (useFont), and the faces
// kept outside a renderer are kept per font.

// screenFont is the font in force, as config keeps it; one this build does not offer is Go.
func screenFont() string {
	v := config.Get().Screen.Font
	if !fonts.Known(v) {
		return fonts.Go
	}
	return v
}

// fontIndex is the font in force among fonts.Families.
func fontIndex() int { return fonts.Index(config.Get().Screen.Font) }

// fontFace is the screen font named at px pixels, regular or bold. A font that cannot be made is
// logged and drawn in Go instead; nil only if Go cannot be made either, which draws nothing, as a face
// that failed always has here.
func fontFace(name string, bold bool, px float64) font.Face {
	f, err := fonts.Face(name, bold, px)
	if err == nil {
		return f
	}
	slog.Error("making a font face failed", "font", name, "size", px, "err", err)
	if name == fonts.Go {
		return nil
	}
	return fontFace(fonts.Go, bold, px)
}

// fontRow is the setting on the screen, under Display. A skin with a font of its own draws in that, and
// the row says the one chosen waits for the skin to come off.
func fontRow() settingRow {
	sub := "For all the screen's words"
	if skins[skinIndex()].font != "" {
		sub = skinKeeps
	}
	return settingRow{id: "font", label: "Font", sub: sub, kind: ctlChoice, value: fonts.Names()[fontIndex()]}
}

func fontPicker() (pickerView, bool) {
	return pickerView{title: "Font", opts: fonts.Names(), cur: fontIndex()}, true
}

// setFont saves the screen's font and shows it at once, so it can be chosen while looking.
func (d *Display) setFont(i int) {
	all := fonts.Families()
	if i < 0 || i >= len(all) {
		return
	}
	if err := config.Set().Screen().Font(all[i].Value); err != nil {
		slog.Error("saving the screen font failed", "err", err)
		return
	}
	slog.Info("screen: font", "font", all[i].Name)
	if d.fontSel != nil {
		d.fontSel.Set(all[i].Name)
	}
	d.wake()
}

// fontSelect is Screen font in Home Assistant.
func fontSelect(d *Display) *esphome.Select {
	s := &esphome.Select{
		Base: esphome.Base{
			ObjectID: "screen_font",
			Name:     "Screen font",
			Icon:     "mdi:format-font",
			Category: esphome.CategoryConfig,
		},
		Options: fonts.Names(),
	}
	s.OnCommand = func(v string) {
		for i, n := range fonts.Names() {
			if n == v {
				d.setFont(i)
				return
			}
		}
	}
	return s
}

// ownFonts is the fonts folder as the setup page lists it.
func ownFonts() []setup.OwnFont {
	var out []setup.OwnFont
	for _, o := range fonts.Own() {
		out = append(out, setup.OwnFont{File: o.File, Size: o.Size, Name: o.Name, Problem: o.Problem})
	}
	return out
}

// addOwnFont keeps a font file sent from the setup page and draws with it at once.
func (d *Display) addOwnFont(name string, r io.Reader) error {
	if err := fonts.AddOwn(name, r); err != nil {
		return err
	}
	d.wake()
	return nil
}

// removeOwnFont deletes one and draws without it at once.
func (d *Display) removeOwnFont(file string) error {
	if err := fonts.RemoveOwn(file); err != nil {
		return err
	}
	d.wake()
	return nil
}

// fontKey is a set of faces' fonts, for keeping them: the screen font's name, and the generation of
// the owner's own fonts (lib/fonts.Generation) they were made with.
type fontKey struct {
	name string
	gen  uint64
}

// keptFonts is how many fonts' faces the caches below keep before starting again.
const keptFonts = 4

// sheetFaceCache is facesFor's faces, per font.
var sheetFaceCache struct {
	mu    sync.Mutex
	faces map[fontKey]sheetFaces
}

// facesFor is the settings screen's faces at the sizes the layout was drawn for, in the font k names,
// made once per font.
func facesFor(k fontKey) sheetFaces {
	sheetFaceCache.mu.Lock()
	defer sheetFaceCache.mu.Unlock()
	if f, ok := sheetFaceCache.faces[k]; ok {
		return f
	}
	f := sheetFacesAt(k.name, func(n int) int { return n })
	sheetFaceCache.faces = roomFor(sheetFaceCache.faces, k)
	sheetFaceCache.faces[k] = f
	return f
}

// roomFor makes room in a cache of faces for k's: faces made with the owner's own fonts as they were
// before (another generation) go at once, so a font of one's own that was removed or replaced is not
// kept open by them, and the rest once keptFonts fonts' faces are kept.
func roomFor[V any](m map[fontKey]V, k fontKey) map[fontKey]V {
	if m == nil || len(m) >= keptFonts {
		return map[fontKey]V{}
	}
	for old := range m {
		if old.gen != k.gen {
			delete(m, old)
		}
	}
	return m
}

// fontChanged is whether the screen font, or the owner's own fonts, have changed since a paint's
// faces were made, looking at the fonts folder now and then (lib/fonts.Refresh).
func (p *paint) fontChanged() (fontKey, bool) {
	fonts.Refresh()
	k := fontKey{drawnFont(), fonts.Generation()}
	return k, k != fontKey{p.font, p.fontGen}
}
