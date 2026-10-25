//go:build !dot && !spot && !panel

package display

import (
	"log/slog"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/android/setup"
	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The Show's processor puts its cores to sleep when they are not wanted (MediaTek's hotplug), down to
// the diagnostics' min_cores, and wakes them slowly: a frame of a turn screen or of a page following a
// finger is drawn on one or two of them, at half the pace it could be. Android asks for its cores
// while somebody is using the screen; so does the display here, through the same floor
// (setup.MinCores), while a turn is on the screen or a finger has just moved on it, and gives them
// back once neither has been so for boostLinger. A page that moves on its own (the rain map, a
// slideshow) does not ask: it can be up for hours.
const boostLinger = 1500 * time.Millisecond

type boost struct {
	on   bool
	last time.Time // the last frame that wanted the cores
}

// update is told, each frame, whether it wants the cores: a turn, or a finger just now.
func (b *boost) update(want bool) {
	now := time.Now()
	if want {
		b.last = now
		if !b.on {
			b.set(true)
		}
		return
	}
	if b.on && now.Sub(b.last) > boostLinger {
		b.set(false)
	}
}

// set asks for every core, or gives them back to min_cores.
func (b *boost) set(on bool) {
	n := max(1, config.Get().Diag.MinCores)
	if on {
		n = max(n, setup.Present())
	}
	if err := setup.MinCores(n); err != nil {
		slog.Warn("display: holding the cores", "cores", n, "err", err)
	}
	b.on = on
}
