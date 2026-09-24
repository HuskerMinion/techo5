//go:build !dot && !spot

package display

import (
	"image"
	"image/draw"
	"log/slog"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
)

// The dashboard page: a Home Assistant dashboard over the whole screen, drawn here or streamed from a
// dashcast server. A swipe in from the left edge brings it up from the clock, and the same swipe takes
// it away again; "go home" does too.
const (
	// dashForget is how long an opened dashboard stays up untouched before the clock comes back,
	// when it is not also the idle page.
	dashForget = 10 * time.Minute
)

// openDashboard puts the dashboard up, if there is one to put up.
func (d *Display) openDashboard() bool {
	if dashboard.Get().Mode() == config.DashboardOff {
		return false
	}
	d.mu.Lock()
	d.dash, d.dashTouched = true, time.Now()
	d.drawer, d.sheet = false, false
	d.mu.Unlock()
	slog.Info("dashboard up", "mode", dashboard.Get().Mode())
	d.wake()
	return true
}

func (d *Display) closeDashboard() {
	d.mu.Lock()
	d.dash, d.dashEdge = false, false
	d.mu.Unlock()
	d.wake()
}

// dashScene decides whether the dashboard is the page, and fetches what it shows. Everything else
// that takes the screen - a sheet, the drawer, a camera, the weather, a turn - comes first; music
// comes first only when the dashboard is standing in for the clock rather than asked for.
func (d *Display) dashScene(s *scene, sheetOrDrawer bool) {
	f := dashboard.Get()
	mode := f.Mode()
	d.mu.Lock()
	if d.dash && time.Since(d.dashTouched) > dashForget {
		d.dash = false
	}
	asked := d.dash
	d.mu.Unlock()

	want := mode != config.DashboardOff && s.phase == "idle" && !sheetOrDrawer &&
		!s.showCamera && !s.showWeather && !s.showRadar && !s.showWifi && !s.bt.Pairing &&
		(asked || (f.Idle() && !s.nowPlaying))
	s.showDash, s.dashMode = want, mode

	streamed := want && mode == config.DashboardStreamed
	if streamed && d.r != nil {
		s.dash = f.Stream(d.r.w, d.r.h)
	} else {
		f.Close()
	}

	d.mu.Lock()
	d.dashShowing = want
	follow := streamed
	changed := follow != d.dashFollow
	d.dashFollow = follow
	d.mu.Unlock()
	if changed {
		// A streamed dashboard wants every finger as it moves, to scroll the page under it; the
		// rest of the screen wants swipes.
		touch.Get().SetFollow(follow)
	}
}

// dashGesture is a finger on the dashboard. Streamed, it goes to the page as it moves, except a
// finger that starts at the left edge, which is the way back to the clock. Drawn, taps land on the
// cards.
func (d *Display) dashGesture(g touch.Gesture) {
	if d.r == nil {
		return
	}
	d.mu.Lock()
	d.dashTouched = time.Now()
	d.mu.Unlock()
	edge := d.r.drawerEdge()
	f := dashboard.Get()

	if f.Mode() != config.DashboardStreamed {
		switch g.Kind {
		case touch.SwipeRight:
			if g.X < edge {
				d.closeDashboard()
			}
		case touch.Tap:
			d.drawnTap(g.X, g.Y)
		}
		return
	}

	switch g.Kind {
	case touch.Tap:
		f.Touch("tap", g.X, g.Y)
	case touch.Hold:
		if g.X < edge {
			d.mu.Lock()
			d.dashEdge, d.dashEdgeX = true, g.X
			d.mu.Unlock()
			return
		}
		f.Touch("down", g.X, g.Y)
	case touch.Drag:
		d.mu.Lock()
		fromEdge := d.dashEdge
		d.mu.Unlock()
		if !fromEdge {
			f.Touch("move", g.X, g.Y)
		}
	case touch.Release:
		d.mu.Lock()
		fromEdge, startX := d.dashEdge, d.dashEdgeX
		d.dashEdge = false
		d.mu.Unlock()
		if fromEdge {
			if g.X-startX > d.r.s(80) {
				d.closeDashboard()
			}
			return
		}
		f.Touch("up", g.X, g.Y)
	}
}

// dashboardPage draws the dashboard over the whole panel.
func (r *renderer) dashboardPage(s scene) {
	if s.dashMode == config.DashboardDrawn {
		r.drawnDashboard(s)
		return
	}
	v := s.dash
	drawn := v.Ready && dashboard.Get().DrawStream(r.dst)
	msg := v.Problem
	if msg == "" && !drawn {
		msg = "Connecting to the dashboard…"
	}
	if msg == "" {
		return
	}
	if !drawn {
		r.text(r.small, msg, (r.w-r.width(r.small, msg))/2, r.h/2, dim)
		return
	}
	// Over the picture: the last one stays up, with what went wrong along the foot.
	draw.Draw(r.dst, image.Rect(0, r.h-r.s(36), r.w, r.h), image.NewUniform(shade), image.Point{}, draw.Over)
	r.text(r.tiny, msg, r.margin, r.h-r.s(11), dim)
}
