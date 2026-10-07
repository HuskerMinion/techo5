//go:build !dot

package display

import (
	"image"
	"log/slog"
	"strings"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"

	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
	"github.com/HuskerMinion/techo5/echod/internal/lib/mdi"
)

// The drawn dashboard's touch: a tap on a tile or a row does what it says, a finger moving up or down
// scrolls, and one moving along a tile with a level slides the level. Its drawing is drawn_cards.go.

// dashTile is a tile where the page last drew it, for a tap to find.
type dashTile struct {
	r      image.Rectangle
	action *dashboard.Action
	adjust *dashboard.Adjust
	entity string // the entity it shows, for a long press to open its sheet
}

// drawnDrag is a finger moving on the drawn dashboard: undecided until it has moved far enough to
// say, then a scroll (up or down) or a slide (along a tile with a level).
type drawnDrag struct {
	at          image.Point
	startScroll int
	tile        *dashTile
	sliding     bool
	scrolling   bool
	value       float64 // the slid level, while sliding
	// down is a finger that came down on the page; moved is one that has gone further than a hold
	// allows, so lifting it is not a long press.
	down, moved bool
	// onSheet is a finger that came down on the color sheet: only one of those that moves chooses
	// where it lifts, so a drag from the page under the sheet does not set what it ends over.
	onSheet bool
	// slider is the sheet's slider the finger came down on, if it did: it follows the finger along,
	// and is set where the finger lifts.
	slider *sheetSlider
}

// colorZone is a part of the color sheet where it was drawn (color_sheet.go): Done, the band of whites
// from lo to hi kelvin, or a color whose hue is value.
type colorZone struct {
	r             image.Rectangle
	kind          int
	value, lo, hi float64
}

// mediaZone is a part of the media sheet where it was drawn (media_sheet.go); index is the favorite's
// or the speaker's.
type mediaZone struct {
	r     image.Rectangle
	kind  int
	index int
}

// sheetSlider is a slider on a sheet where it was drawn: the color sheet's band of whites or of
// colors, the media sheet's volume, or a grouped speaker's volume along its button, from lo at its
// left end to hi at its right.
type sheetSlider struct {
	kind   int // colorPartWhite, colorPartHue, mediaPartVolume or mediaPartSpeaker
	r      image.Rectangle
	lo, hi float64
	entity string // the speaker, for mediaPartSpeaker
	// from and base are, on a speaker's button, where the finger came down and the volume it had: the
	// button is no bar to aim at, so the volume moves by as much as the finger does, as a tile's level.
	from int
	base float64
}

// at is the value under x, kept to the slider's ends when the finger is past them.
func (s sheetSlider) at(x int) float64 {
	f := float64(x-s.r.Min.X) / float64(max(s.r.Dx()-1, 1))
	return s.lo + min(max(f, 0), 1)*(s.hi-s.lo)
}

// holdStill is how far a finger may wander and still be a long press when it lifts: less than the
// touchscreen's followMove, so a finger it began to follow never is.
const holdStill = 10

// dashAdjusting is the level a finger is sliding, for the tile to draw it.
type dashAdjusting struct {
	entity string
	value  float64
}

var (
	iconFont  *opentype.Font
	iconOnce  sync.Once
	iconFaces sync.Map // size in pixels → font.Face

	textFonts     [2]*opentype.Font // regular, bold
	textFontsOnce sync.Once
	textFaces     sync.Map // textKey → font.Face
)

// textKey is a text face made for a big dashboard tile: bold or not, and its size in pixels.
type textKey struct {
	bold bool
	px   int
}

// textFace is the screen's own font at size, regular or bold, made once: for the dashboard's bigger
// tiles, whose words grow with them past the fixed sizes the rest of the page uses.
func (r *paint) textFace(bold bool, size int) font.Face {
	textFontsOnce.Do(func() {
		textFonts[0], _ = opentype.Parse(goregular.TTF)
		textFonts[1], _ = opentype.Parse(gobold.TTF)
	})
	f := textFonts[0]
	if bold {
		f = textFonts[1]
	}
	if f == nil {
		return nil
	}
	key := textKey{bold, r.s(size)}
	if fc, ok := textFaces.Load(key); ok {
		return fc.(font.Face)
	}
	fc, err := opentype.NewFace(f, &opentype.FaceOptions{Size: float64(key.px), DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return nil
	}
	textFaces.Store(key, fc)
	return fc
}

// iconFace is the icon font at size, made once.
func (r *paint) iconFace(size int) font.Face {
	iconOnce.Do(func() {
		f, err := opentype.Parse(mdi.Font)
		if err != nil {
			slog.Error("parsing the icon font failed", "err", err)
			return
		}
		iconFont = f
	})
	if iconFont == nil {
		return nil
	}
	px := r.s(size)
	if fc, ok := iconFaces.Load(px); ok {
		return fc.(font.Face)
	}
	fc, err := opentype.NewFace(iconFont, &opentype.FaceOptions{Size: float64(px), DPI: 72, Hinting: font.HintingNone})
	if err != nil {
		return nil
	}
	iconFaces.Store(px, fc)
	return fc
}

// wrapLines breaks text into lines no wider than w.
func (r *paint) wrapLines(face font.Face, text string, w int) []string {
	var lines []string
	line := ""
	for _, word := range strings.Fields(text) {
		try := word
		if line != "" {
			try = line + " " + word
		}
		if line != "" && r.width(face, try) > w {
			lines = append(lines, line)
			line = word
			continue
		}
		line = try
	}
	if line != "" {
		lines = append(lines, r.fit(face, line, w))
	}
	return lines
}

// drawnTap is a tap on the drawn dashboard: on a tile, it does what the tile says.
func (d *Display) drawnTap(x, y int) {
	if d.colorTap(x, y) {
		return
	}
	if t := d.tileAt(x, y); t != nil && t.action != nil {
		slog.Info("dashboard tap", "entity", t.action.Entity, "service", t.action.Service, "view", t.action.View)
		dashboard.Get().Tap(*t.action)
	}
}

// tileAt is the tile under a point, as the page last drew it.
func (d *Display) tileAt(x, y int) *dashTile {
	if d.r == nil {
		return nil
	}
	tiles, _ := d.r.dash()
	for i := range tiles {
		if image.Pt(x, y).In(tiles[i].r) {
			t := tiles[i]
			return &t
		}
	}
	return nil
}

// drawnHold is a finger coming down on the drawn dashboard; what it is doing is decided as it moves.
func (d *Display) drawnHold(x, y int) {
	t := d.tileAt(x, y)
	d.mu.Lock()
	d.dashDrag.at = image.Pt(x, y)
	d.dashDrag.tile = t
	d.dashDrag.down = true
	d.mu.Unlock()
	onSheet := d.onColorSheet(x, y)
	var slider *sheetSlider
	if onSheet {
		if s, ok := d.sliderAt(x, y); ok {
			slider = &s
		}
	}
	d.mu.Lock()
	d.dashDrag.onSheet, d.dashDrag.slider = onSheet, slider
	d.mu.Unlock()
	if slider != nil {
		d.sliderBegins(*slider)
	}
}

// slideStart is how far a finger moves before it counts as a scroll or a slide rather than a tap
// that wandered.
const slideStart = 14

// drawnMove is the finger moving: once it has gone far enough, up or down is a scroll that follows
// it, and along a tile with a level is that level following it, a tile's width from one end of its
// range to the other.
func (d *Display) drawnMove(x, y int) {
	sheet := d.colorOpen()
	_, content := d.r.dash()
	d.mu.Lock()
	dr := &d.dashDrag
	dx, dy := x-dr.at.X, y-dr.at.Y
	if abs(dx) > holdStill || abs(dy) > holdStill {
		dr.moved = true
	}
	if sheet {
		// The sheet is over the page: nothing under it scrolls or slides. That the finger moved is
		// kept, for where it lifts (drawnRelease), and a slider it came down on follows it.
		slider, moved := dr.slider, dr.moved
		d.mu.Unlock()
		if slider != nil && slider.slides(moved) {
			d.slideSheet(*slider, x, false)
		}
		return
	}
	defer d.mu.Unlock()
	if !dr.sliding && !dr.scrolling {
		switch {
		case abs(dx) > d.r.s(slideStart) && abs(dx) > abs(dy) && dr.tile != nil && dr.tile.adjust != nil:
			dr.sliding, dr.value = true, dr.tile.adjust.Value
			dr.at.X = x // measured from here, so the level does not jump by the slack
			dx = 0
		case abs(dy) > d.r.s(slideStart):
			dr.scrolling = true
		}
	}
	switch {
	case dr.scrolling:
		most := max(content-d.r.h, 0)
		d.dashScroll = min(max(dr.startScroll-dy, 0), most)
	case dr.sliding:
		a := dr.tile.adjust
		span := float64(dr.tile.r.Dx())
		dr.value = a.Snap(a.Value + float64(dx)/span*(a.Max-a.Min))
		d.dashAdjust = dashAdjusting{entity: a.Entity, value: dr.value}
	}
}

// drawnRelease is the finger lifting at x, y: a slide sets its level, and a finger that stayed put
// until it was a hold is a long press (longPress). With the color sheet up, it is the sheet's.
func (d *Display) drawnRelease(x, y int) {
	d.mu.Lock()
	dr := d.dashDrag
	d.dashDrag = drawnDrag{}
	d.dashAdjust = dashAdjusting{}
	d.mu.Unlock()
	if dr.moved && !dr.onSheet && d.colorOpen() {
		return // a drag from the page under the sheet: nothing on the sheet was chosen
	}
	if dr.moved && dr.onSheet && dr.slider == nil && d.pageSwipeOnSheet(dr.at, x, y) {
		return
	}
	if dr.moved && dr.onSheet && dr.slider == nil && !d.samePartOnSheet(dr.at, image.Pt(x, y)) {
		return // a drag that lifted on another part of the sheet: neither was tapped
	}
	if dr.slider != nil && d.colorOpen() && dr.slider.slides(dr.moved) {
		d.slideSheet(*dr.slider, x, true) // where it lifted, wherever it went on the way
		return
	}
	if d.colorTap(x, y) {
		return
	}
	switch {
	case dr.sliding && dr.tile != nil && dr.tile.adjust != nil:
		slog.Info("dashboard slide", "entity", dr.tile.adjust.Entity, "to", dr.value)
		dashboard.Get().SetLevel(*dr.tile.adjust, dr.value)
	case dr.down && !dr.moved && !dr.scrolling && dr.tile != nil:
		d.longPress(dr.tile)
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
