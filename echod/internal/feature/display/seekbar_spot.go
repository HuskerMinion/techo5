//go:build spot

package display

import (
	"image"
	"time"
)

// The Spot's bar to seek on (seekbar.go): the video's only. Its music face has no bar, so nothing
// there is ever routed to Music Assistant.

// followWantedLocked is whether something besides a bar wants the touchscreen to follow every finger:
// the dashboard's face and the ring menu (openMenu). Called with d.mu held.
func (d *Display) followWantedLocked() bool { return d.dashFollow || d.menuOpen }

// seekTakesLocked is whether a finger on a bar is the bar's: the video face's, which is only drawn
// while nothing is over it. Called with d.mu held.
func (d *Display) seekTakesLocked() bool { return true }

// musicSeek is never asked for on the Spot (seekMusic is the Show's Now Playing).
func musicSeek(time.Duration) {}

// seekPullLocked is whether a finger coming down at at is where a pull starts: the Spot's bar is the
// video face's, which has no drawer to pull. Called with d.mu held.
func (d *Display) seekPullLocked(image.Point) bool { return false }
