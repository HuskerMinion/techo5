//go:build spot

package display

import (
	"fmt"

	"github.com/HuskerMinion/techo5/echod/internal/feature/takes"
)

// The face for takes of a word: Start while the setup page's recording waits for somebody at the
// device, and Stop while it runs, where the face that lets a browser in has its answers
// (setupask_spot.go).

func (r *roundRenderer) takesFace(s roundScene) {
	c := s.takes
	fc := r.faces()
	right := func(label string) int { return center + (r.paint.width(fc.button, label)+44)/2 }
	if c.Phase == takes.Armed {
		if c.Run {
			r.centered(r.title, tr("record_wake_word"), 130, colText)
			r.centered(r.small, fmt.Sprintf(tr("ui_record_plan"), c.Of, int(c.Gap.Seconds())), 176, colDim)
		} else {
			r.centered(r.title, tr("record_takes_of_a_word"), 130, colText)
			r.centered(r.small, takesWhat(c, 14), 176, colDim)
		}
		r.centered(r.small, tr("ui_takes_setup_requested"), 206, colDim)
		r.pillButton(right(tr("not_now")), askNoY, tr("not_now"), btnSecondary)
		r.pillButton(right(tr("ui_start")), askYesY, tr("ui_start"), btnPrimary)
		return
	}
	if c.Run {
		r.centered(r.title, fmt.Sprintf(tr("ui_take_n_of_m"), c.Take, c.Of), 130, colText)
		r.centered(r.small, runLine(c), 176, colDim)
		r.centered(r.small, tr("record_wake_word"), 206, colDim)
		r.pillButton(right(tr("stop")), askYesY, tr("stop"), btnPrimary)
		return
	}
	r.centered(r.title, tr("ui_recording"), 130, colText)
	r.centered(r.small, takesWhat(c, 14), 176, colDim)
	r.centered(r.small, takesTime(c.Elapsed), 206, colDim)
	r.pillButton(right(tr("stop")), askYesY, tr("stop"), btnPrimary)
}
