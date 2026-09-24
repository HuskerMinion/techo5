//go:build !dot && !spot

package display

import (
	"image"
	"log/slog"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"

	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
	"github.com/HuskerMinion/techo5/echod/internal/lib/mdi"
)

// The drawn dashboard: the house room by room, each thing in it a tile with Home Assistant's icon,
// its name and what it is doing. A tap on a tile does what the tile says; vertical swipes scroll.

// Tile layout, in the sizes the rest of the package is written in (a Show 5 in landscape).
const (
	tileCols   = 3
	tileH      = 92
	tileGap    = 14
	roomTop    = 30 // above a room's name
	roomTitleH = 46 // the name's line, down to its tiles
)

// dashTile is a tile where the page last drew it, for a tap to find.
type dashTile struct {
	r      image.Rectangle
	entity string
}

var (
	iconFont  *opentype.Font
	iconOnce  sync.Once
	iconFaces sync.Map // size in pixels → font.Face
)

// iconFace is the icon font at size, made once.
func (r *renderer) iconFace(size int) font.Face {
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

func (r *renderer) drawnDashboard(s scene) {
	v := s.drawn
	fc := r.faces()
	if len(v.Rooms) == 0 {
		msg := v.Problem
		if msg == "" {
			msg = "Loading the rooms…"
		}
		r.text(r.small, msg, (r.w-r.width(r.small, msg))/2, r.h/2, dim)
		r.dashTiles, r.dashContent = nil, 0
		return
	}

	left, right := r.margin, r.w-r.margin
	gap := r.s(tileGap)
	tw := (right - left - gap*(tileCols-1)) / tileCols
	th := r.s(tileH)
	rad := r.sf(18)
	y := -s.dashScroll
	var tiles []dashTile

	for _, room := range v.Rooms {
		y += r.s(roomTop)
		base := y + r.s(roomTitleH) - r.s(14)
		if base > 0 && y < r.h {
			r.text(fc.header, room.Name, left, base, cream)
			if room.Climate != "" {
				r.text(fc.value, room.Climate, right-r.width(fc.value, room.Climate), base, dim)
			}
		}
		y += r.s(roomTitleH)
		for i, t := range room.Tiles {
			col := i % tileCols
			if col == 0 && i > 0 {
				y += th + gap
			}
			b := image.Rect(left+col*(tw+gap), y, left+col*(tw+gap)+tw, y+th)
			if b.Max.Y > 0 && b.Min.Y < r.h {
				r.tile(b, rad, t)
				if t.Tap {
					tiles = append(tiles, dashTile{r: b, entity: t.Entity})
				}
			}
		}
		if len(room.Tiles) > 0 {
			y += th
		}
	}
	r.dashTiles = tiles
	r.dashContent = y + s.dashScroll + r.s(roomTop)
}

// tile draws one thing: its icon on the left, lit in the accent when it is on, its name, and what it
// is doing underneath.
func (r *renderer) tile(b image.Rectangle, rad float64, t dashboard.Tile) {
	fc := r.faces()
	top, bottom := surface(3), surface(2)
	if t.On {
		top, bottom = shift(surface(3), 10), shift(surface(2), 10)
	}
	r.roundFill(b, rad, top, bottom)
	r.roundHighlight(b, rad)

	pad := r.s(18)
	iconSize := 40
	ic := dim
	if t.On {
		ic = amber
	}
	if t.Gone {
		ic = lerp(dim, walnut, 0.5)
	}
	if face := r.iconFace(iconSize); face != nil {
		if g, ok := mdi.Rune(t.Icon); ok {
			r.text(face, string(g), b.Min.X+pad, b.Min.Y+b.Dy()/2+r.s(iconSize)/2-r.s(2), ic)
		}
	}
	x := b.Min.X + pad + r.s(iconSize) + r.s(14)
	room := b.Max.X - pad - x
	r.text(fc.label, r.fit(fc.label, t.Name, room), x, b.Min.Y+b.Dy()/2-r.s(4), cream)
	value := t.Value
	if t.Busy {
		value = "…"
	}
	r.text(fc.sub, r.fit(fc.sub, value, room), x, b.Min.Y+b.Dy()/2+r.s(26), dim)
}

// fit is s cut to width w with an ellipsis, when it does not fit whole.
func (r *renderer) fit(face font.Face, s string, w int) string {
	if r.width(face, s) <= w {
		return s
	}
	runes := []rune(s)
	for len(runes) > 1 {
		runes = runes[:len(runes)-1]
		if cut := string(runes) + "…"; r.width(face, cut) <= w {
			return cut
		}
	}
	return "…"
}

// drawnTap is a tap on the drawn dashboard: on a tile, it does what the tile says.
func (d *Display) drawnTap(x, y int) {
	if d.r == nil {
		return
	}
	for _, t := range d.r.dashTiles {
		if image.Pt(x, y).In(t.r) {
			slog.Info("dashboard tap", "entity", t.entity)
			dashboard.Get().TapTile(t.entity)
			return
		}
	}
}

// drawnScroll moves the drawn dashboard by a swipe's notch.
func (d *Display) drawnScroll(g touch.Gesture) {
	if d.r == nil {
		return
	}
	by := notchPx
	if g.Kind == touch.SwipeDown {
		by = -notchPx
	}
	most := max(d.r.dashContent-d.r.h, 0)
	d.mu.Lock()
	d.dashScroll = min(max(d.dashScroll+by, 0), most)
	d.mu.Unlock()
}
