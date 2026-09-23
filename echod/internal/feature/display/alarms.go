//go:build !dot && !spot

package display

import (
	"log/slog"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/alarm"
	"github.com/HuskerMinion/techo5/echod/internal/feature/ring"
	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
)

// ringState is what is ringing, for the ringing page.
type ringState struct {
	timer     string // name of a ringing timer
	alarm     *alarm.Ring
	preview   bool
	snoozable bool

	// silenced is a ring a button press quieted, waiting to be told whether that meant snooze. The
	// page says so, because a silent ring with the same face on it looks like one that stopped, and
	// somebody who walks away from it will find it ringing again in a moment.
	silenced bool
}

func (r ringState) any() bool { return r.timer != "" || r.alarm != nil || r.preview }

// ringing reads what is sounding now.
func (d *Display) ringing(now time.Time) ringState {
	var st ringState
	if name, ok := timer.Get().RingingName(); ok {
		st.timer = name
	}
	st.alarm = alarm.Get().View(now).Ringing
	st.snoozable = st.alarm != nil
	st.silenced = ring.Offered()
	d.mu.Lock()
	if now.Before(d.ringPreview) && !st.any() {
		st.preview, st.snoozable = true, true
		st.alarm = &alarm.Ring{Label: "Wake up", At: now}
	}
	d.mu.Unlock()
	return st
}

// PreviewRing shows the ringing page for a while with nothing sounding, to look at it.
func (d *Display) PreviewRing(for_ time.Duration) {
	d.mu.Lock()
	d.ringPreview = time.Now().Add(for_)
	d.mu.Unlock()
	d.wake()
}

// ringTap is a finger on the ringing page: Stop on the left half of the buttons, Snooze on the right
// when there is one to snooze.
func (d *Display) ringTap(x, y int, st ringState) {
	if d.r == nil || !d.r.actionDecided(y) {
		return
	}
	d.mu.Lock()
	d.ringPreview = time.Time{}
	d.mu.Unlock()
	if st.snoozable && x >= d.r.w/2 {
		if !alarm.Get().Snooze() {
			slog.Debug("snooze with nothing ringing")
		}
		timer.Get().Stop()
		return
	}
	timer.Get().Stop()
	alarm.Get().Stop()
}
