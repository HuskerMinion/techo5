//go:build spot

package display

import (
	"image/color"
	"log/slog"
	"math"
	"sync/atomic"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// setMusicStrip saves the choice; the Spot has no strip of its own, and hides the row.
func (d *Display) setMusicStrip(i int) { setStrip(nil, i) }

// The star on the round face, mirroring Done on the other side of play and pause.
const starX, starY, starR = center + 88.0, 420.0, 25.0

// spotFaved is the track the star was last pressed for, so the star shows it was saved.
var spotFaved atomic.Value // string

// onStar reports whether a tap at x, y is on the star.
func onStar(x, y int) bool {
	dx, dy := float64(x)-starX, float64(y)-starY
	return dx*dx+dy*dy <= (starR+8)*(starR+8)
}

// favoriteSpot saves what is playing to favorites, and fills the star in once it is saved.
func (d *Display) favoriteSpot() {
	rd := home.Get().Radio()
	if err := home.Get().FavoriteNow(); err != nil {
		slog.Warn("saving to favorites failed", "err", err)
		return
	}
	spotFaved.Store(rd.Title + "\x00" + rd.Now)
	d.wake()
}

// spotStarFilled is whether the star was pressed for this track.
func spotStarFilled(rd home.Radio) bool {
	k, _ := spotFaved.Load().(string)
	return k != "" && k == rd.Title+"\x00"+rd.Now
}

// starMark fills a five-pointed star as a fan of triangles from its middle.
func (r *roundRenderer) starMark(cx, cy, rad float64, c color.RGBA) {
	var px, py [10]float64
	for i := range px {
		a := -math.Pi/2 + float64(i)*math.Pi/5
		rr := rad
		if i%2 == 1 {
			rr *= 0.45
		}
		px[i], py[i] = cx+rr*math.Cos(a), cy+rr*math.Sin(a)
	}
	for i := range px {
		j := (i + 1) % len(px)
		r.triangle(cx, cy, px[i], py[i], px[j], py[j], c)
	}
}
