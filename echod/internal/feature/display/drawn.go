//go:build !dot && !spot

package display

// The drawn dashboard: Home Assistant's cards, drawn by the device itself.

func (r *renderer) drawnDashboard(s scene) {
	msg := "The drawn dashboard is on its way."
	r.text(r.small, msg, (r.w-r.width(r.small, msg))/2, r.h/2, dim)
}

// drawnTap is a tap on the drawn dashboard.
func (d *Display) drawnTap(x, y int) {}
