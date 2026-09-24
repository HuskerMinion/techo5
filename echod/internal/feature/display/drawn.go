//go:build !dot && !spot

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
	if len(v.Blocks) == 0 {
		msg := v.Problem
		if msg == "" {
			msg = "Loading the dashboard…"
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
	visible := func(top, bottom int) bool { return bottom > 0 && top < r.h }

	for _, b := range v.Blocks {
		if b.Heading != "" {
			y += r.s(roomTop)
			base := y + r.s(roomTitleH) - r.s(14)
			if visible(y, base+r.s(10)) {
				name := b.Heading
				if b.Right != "" {
					name = r.fit(fc.header, name, right-left-r.width(fc.value, b.Right)-r.s(20))
					r.text(fc.value, b.Right, right-r.width(fc.value, b.Right), base, dim)
				} else {
					name = r.fit(fc.header, name, right-left)
				}
				r.text(fc.header, name, left, base, cream)
			}
			y += r.s(roomTitleH)
		} else {
			y += gap
		}
		for _, para := range b.Text {
			for _, line := range r.wrapLines(fc.label, para, right-left) {
				y += r.s(34)
				if visible(y-r.s(30), y+r.s(8)) {
					r.text(fc.label, line, left, y, cream)
				}
			}
			y += r.s(10)
		}
		for i, t := range b.Tiles {
			col := i % tileCols
			if col == 0 && i > 0 {
				y += th + gap
			}
			box := image.Rect(left+col*(tw+gap), y, left+col*(tw+gap)+tw, y+th)
			if visible(box.Min.Y, box.Max.Y) {
				r.tile(box, rad, t, s.dashAdjust)
				if t.Tap != nil || t.Adjust != nil {
					tiles = append(tiles, dashTile{r: box, action: t.Tap, adjust: t.Adjust})
				}
			}
		}
		if len(b.Tiles) > 0 {
			y += th
		}
	}
	r.dashTiles = tiles
	r.dashContent = y + s.dashScroll + r.s(roomTop)
}

// wrapLines breaks text into lines no wider than w.
func (r *renderer) wrapLines(face font.Face, text string, w int) []string {
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

// tile draws one thing: its icon on the left, lit in the accent when it is on, its name, and what it
// is doing underneath.
func (r *renderer) tile(b image.Rectangle, rad float64, t dashboard.Tile, adj dashAdjusting) {
	fc := r.faces()
	top, bottom := surface(3), surface(2)
	if t.On {
		top, bottom = shift(surface(3), 10), shift(surface(2), 10)
	}
	r.roundFill(b, rad, top, bottom)

	// A level: a bar along the foot of the tile showing where it is, which is also the sign that a
	// finger can slide it. While one does, the whole tile fills to the level instead.
	sliding := t.Adjust != nil && adj.entity == t.Adjust.Entity
	if a := t.Adjust; a != nil && a.Max > a.Min {
		v := a.Value
		if sliding {
			v = adj.value
		}
		frac := min(max((v-a.Min)/(a.Max-a.Min), 0), 1)
		if sliding {
			fill := image.Rect(b.Min.X, b.Min.Y, b.Min.X+int(float64(b.Dx())*frac), b.Max.Y)
			if fill.Dx() > 0 {
				r.roundFill(fill, rad, lerp(surface(3), amber, 0.35), lerp(surface(2), amber, 0.35))
			}
			t.Value = a.Label(v)
		} else if frac > 0 && t.On {
			inset := r.s(14)
			bar := image.Rect(b.Min.X+inset, b.Max.Y-r.s(7), b.Min.X+inset+int(float64(b.Dx()-2*inset)*frac), b.Max.Y-r.s(4))
			if bar.Dx() > 0 {
				r.roundFill(bar, r.sf(2), amber, amber)
			}
		}
	}
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
	r.text(fc.sub, r.fit(fc.sub, t.Value, room), x, b.Min.Y+b.Dy()/2+r.s(26), dim)
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
	for i := range d.r.dashTiles {
		if image.Pt(x, y).In(d.r.dashTiles[i].r) {
			t := d.r.dashTiles[i]
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
		most := max(d.r.dashContent-d.r.h, 0)
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
