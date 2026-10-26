//go:build spot

package display

import "github.com/HuskerMinion/techo5/echod/internal/feature/takes"

// The face for takes of a word: Start while the setup page's recording waits for somebody at the
// device, and Stop while it runs, where the face that lets a browser in has its answers
// (setupask_spot.go).

func (r *roundRenderer) takesFace(s roundScene) {
	c := s.takes
	fc := r.faces()
	right := func(label string) int { return center + (r.paint.width(fc.button, label)+44)/2 }
	if c.Phase == takes.Armed {
		r.centered(r.title, "Record takes", 130, colText)
		r.centered(r.small, takesWhat(c, 14), 176, colDim)
		r.centered(r.small, "the setup page asked", 206, colDim)
		r.pillButton(right(tr("not_now")), askNoY, tr("not_now"), btnSecondary)
		r.pillButton(right("Start"), askYesY, "Start", btnPrimary)
		return
	}
	r.centered(r.title, "Recording", 130, colText)
	r.centered(r.small, takesWhat(c, 14), 176, colDim)
	r.centered(r.small, takesTime(c.Elapsed), 206, colDim)
	r.pillButton(right("Stop"), askYesY, "Stop", btnPrimary)
}
