//go:build !dot

package display

import (
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// saverSwipe is a swipe left (+1) or right (-1) across the screensaver's photos: the next photo or the
// one before, which then stays up a while (#502). It takes the swipe whenever the screensaver is up,
// even with nowhere to step to, so that it never falls through to the clock under it.
func (d *Display) saverSwipe(dir int) bool {
	d.mu.Lock()
	up, idle := d.saverUp, pageIdle(d.view.Phase)
	d.mu.Unlock()
	if !up || !idle || d.answerUp(time.Now()) {
		return false
	}
	home.Get().StepSlideshow(dir)
	return true
}

// saverDown says the screensaver is not up, for a frame that returns before it would be looked for: a
// dark panel, or first run. Otherwise saverUp kept the last drawn frame's value, and the first swipe on a
// relit screen turned a photo nobody could see.
func (d *Display) saverDown() {
	d.mu.Lock()
	d.saverUp = false
	d.mu.Unlock()
}
