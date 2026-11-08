//go:build !dot && !spot

package display

import (
	"log/slog"
	"sync"
)

// A cover over the screen: a tablet's keyboard cover, a laptop's lid, a case's flap, which a board
// reports through a switch of its own and registers from its own file (setCover, in an init). Closed,
// the screen goes out and nothing is drawn behind it; opened, it comes back as it was, if it was the
// cover that put it out. Nothing here knows any board: with none registered, coverTick does nothing.

// screenCover is a board's cover.
type screenCover interface {
	Here() bool            // the cover's switch is there on this device
	Closed() bool          // the cover is closed now
	Listen(changed func()) // has changed called on each change, from then on
}

var screenLid struct {
	c    screenCover
	once sync.Once
	dark bool // the screen was put out by the cover
}

// coverClosed is whether a cover is there and closed over the screen.
func coverClosed() bool {
	c := screenLid.c
	return c != nil && c.Here() && c.Closed()
}

// setCover registers the board's cover, before the display starts.
func setCover(c screenCover) { screenLid.c = c }

// coverTick puts the screen out while the cover is closed and lights it again when it opens, if it was
// the cover that put it out. It says whether it changed the screen. On each frame.
func (d *Display) coverTick(on bool) bool {
	c := screenLid.c
	if c == nil {
		return false
	}
	screenLid.once.Do(func() { c.Listen(d.wake) })
	if !c.Here() {
		return false
	}
	switch closed := c.Closed(); {
	case closed && on:
		slog.Info("screen: the cover closed, off")
		screenLid.dark = true
		d.apply(false, d.ceilingOrDefault(), false)
		return true
	case !closed && screenLid.dark:
		screenLid.dark = false
		if !on {
			slog.Info("screen: the cover opened, on again")
			d.apply(true, d.ceilingOrDefault(), false)
			return true
		}
	}
	return false
}
