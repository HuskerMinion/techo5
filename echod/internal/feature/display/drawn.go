//go:build !dot

package display

import (
	"image"
	"log/slog"
	"strings"
	"sync"

	"golang.org/x/image/font"
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
}

// dashAdjusting is the level a finger is sliding, for the tile to draw it.
type dashAdjusting struct {
	entity string
	value  float64
}

var (
	iconFont  *opentype.Font
	iconOnce  sync.Once
	iconFaces sync.Map // size in pixels → font.Face
)

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
	d.mu.Unlock()
}

// slideStart is how far a finger moves before it counts as a scroll or a slide rather than a tap
// that wandered.
const slideStart = 14

// drawnMove is the finger moving: once it has gone far enough, up or down is a scroll that follows
// it, and along a tile with a level is that level following it, a tile's width from one end of its
// range to the other.
func (d *Display) drawnMove(x, y int) {
	_, content := d.r.dash()
	d.mu.Lock()
	defer d.mu.Unlock()
	dr := &d.dashDrag
	dx, dy := x-dr.at.X, y-dr.at.Y
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

// drawnRelease is the finger lifting: a slide sets its level.
func (d *Display) drawnRelease() {
	d.mu.Lock()
	dr := d.dashDrag
	d.dashDrag = drawnDrag{}
	d.dashAdjust = dashAdjusting{}
	d.mu.Unlock()
	if dr.sliding && dr.tile != nil && dr.tile.adjust != nil {
		slog.Info("dashboard slide", "entity", dr.tile.adjust.Entity, "to", dr.value)
		dashboard.Get().SetLevel(*dr.tile.adjust, dr.value)
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
