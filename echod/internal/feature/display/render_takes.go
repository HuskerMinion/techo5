//go:build !dot && !spot

package display

import (
	"image"

	"github.com/HuskerMinion/techo5/echod/internal/feature/takes"
)

// The page for takes of a word: Start while the setup page's recording waits for somebody at the
// device, and Stop while it runs. Laid out as the page that lets a browser in (render_setupask.go), the
// refusal on the left where the eye lands first.

func (r *renderer) takesPage(s scene) {
	c := s.takes
	if c.Phase == takes.Armed {
		r.text(r.title, "Record takes", r.margin, r.s(96), amber)
		r.text(r.body, takesWhat(c, 28), r.margin, r.s(176), cream)
		r.text(r.small, "The setup page asked. Up to two minutes from these microphones.", r.margin, r.s(224), dim)

		no, yes := r.actionHalves()
		rad := float64(r.s(actionRadius))
		mid := (no.Min.Y + no.Max.Y) / 2
		fg := r.buttonFace(no, rad, btnSecondary)
		r.text(r.body, tr("not_now"), no.Min.X+(no.Dx()-r.width(r.body, tr("not_now")))/2, mid+r.s(14), fg)
		fg = r.buttonFace(yes, rad, btnPrimary)
		r.text(r.title, "Start", yes.Min.X+(yes.Dx()-r.width(r.title, "Start"))/2, mid+r.s(16), fg)
		return
	}

	r.text(r.title, "Recording takes", r.margin, r.s(96), amber)
	r.text(r.body, takesWhat(c, 28)+"   "+takesTime(c.Elapsed), r.margin, r.s(176), cream)
	r.text(r.small, "Say it, wait a second, and say it again.", r.margin, r.s(224), dim)

	y0, y1 := r.actionBand()
	stop := image.Rect(r.margin, y0, r.w-r.margin, y1)
	fg := r.buttonFace(stop, float64(r.s(actionRadius)), btnPrimary)
	r.text(r.title, tr("stop"), stop.Min.X+(stop.Dx()-r.width(r.title, tr("stop")))/2, (y0+y1)/2+r.s(16), fg)
}
