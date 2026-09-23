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

// ringTap is a finger on the ringing page. Inside the buttons it is what they say: Stop on the left,
// Snooze on the right when there is one to snooze. Anywhere else on the panel it is Stop.
//
// The whole screen decides, the way the Spot's whole face already does. It used to be the buttons
// and a little above them and nothing else — on a Show 5 that left the top 65% of the panel dead,
// and on a Show 8 the same sizes scale to 72%, so most of a ringing alarm was a picture of two
// buttons that did not work where somebody pressed. A tap on a ringing alarm has one obvious
// meaning, and a screen that ignores it is worse than one that takes it.
func (d *Display) ringTap(x, y int, st ringState) {
	if d.r == nil {
		return
	}
	d.mu.Lock()
	d.ringPreview = time.Time{}
	d.mu.Unlock()

	if ringSnoozeAt(x, d.r.w, st.snoozable, d.r.actionDecided(y)) {
		// Accept snoozes what can be snoozed and stops the rest, which is a timer beside the alarm.
		if !ring.Accept() {
			slog.Debug("snooze with nothing ringing")
		}
		return
	}
	ring.End()
}

// stopRing ends whatever is sounding, and reports whether there was anything. It lives here so that
// display.go can stop a ring without importing this package's ring, which would be shadowed by the
// local ringState the frame loop calls the same thing.
func (d *Display) stopRing() bool { return ring.End() }

// ringSnoozeAt reports whether a finger means Snooze rather than Stop: only inside the buttons, and
// only on the Snooze one, and only when there is something that can be snoozed.
//
// Stop is the answer everywhere else, because it is the one that cannot be got wrong. An alarm
// stopped by mistake is over and the person is awake to notice; an alarm snoozed by mistake is
// silent and comes back in nine minutes, which is the failure that reads as the device ignoring
// somebody.
func ringSnoozeAt(x, w int, snoozable, inButtons bool) bool {
	return snoozable && inButtons && x >= w/2
}
