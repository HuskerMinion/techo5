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
// it away again; "go home" does too. The screen's own edges keep working on it: down from the top is
// the settings, in from the right the drawer, so a dashboard that is the home page does not lock
// anybody out of the rest.
const (
	// dashForget is how long an opened dashboard stays up untouched before the clock comes back,
	// when it is not also the idle page.
	dashForget = 10 * time.Minute

	// dashAway is how long the clock stays up when the dashboard is the idle page and somebody put
	// it away.
	dashAway = 2 * time.Minute
)

// Where a finger on a streamed dashboard started, when that was one of the screen's own edges.
const (
	edgeNone = iota
	edgeLeft
	edgeTop
	edgeRight
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

// closeDashboard takes the dashboard down: back to the clock, and when the dashboard is the idle page,
// the clock for a while.
func (d *Display) closeDashboard() {
	d.mu.Lock()
	d.dash, d.dashEdge = false, edgeNone
	if dashboard.Get().Idle() {
		d.dashAwayUntil = time.Now().Add(dashAway)
	}
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
	away := time.Now().Before(d.dashAwayUntil)
	d.mu.Unlock()

	want := mode != config.DashboardOff && s.phase == "idle" && !sheetOrDrawer &&
		!s.showCamera && !s.showWeather && !s.showRadar && !s.showWifi && !s.bt.Pairing &&
		(asked || (f.Idle() && !away && !s.nowPlaying))
	s.showDash, s.dashMode = want, mode

	streamed := want && mode == config.DashboardStreamed
	if streamed && d.r != nil {
		s.dash = f.Stream(d.r.w, d.r.h)
	} else {
		f.Close()
	}
	if want && mode == config.DashboardDrawn {
		s.drawn = f.Drawn(d.r.w)
		d.mu.Lock()
		// A different dashboard starts at its top, and none is scrolled past its end: a short one
		// chosen after a long one scrolled down would otherwise be all above the screen.
		if path := config.Get().Dashboard.Path; path != d.dashScrollFor {
			d.dashScroll, d.dashScrollFor = 0, path
		}
		if d.r != nil && d.r.dashContent > 0 {
			d.dashScroll = min(d.dashScroll, max(d.r.dashContent-d.r.h, 0))
		}
		s.dashScroll, s.dashAdjust = d.dashScroll, d.dashAdjust
		d.mu.Unlock()
	} else {
		f.CloseDrawn()
	}

	d.mu.Lock()
	d.dashShowing = want
	// Either way the page wants every finger as it moves: streamed, to scroll the page under it;
	// drawn, to scroll and to slide a tile's level. The rest of the screen wants swipes.
	follow := want
	changed := follow != d.dashFollow
	d.dashFollow = follow
	d.mu.Unlock()
	if changed {
		touch.Get().SetFollow(follow)
	}
}

// dashGesture is a finger on the dashboard. It goes to the page as it moves, except a finger that
// starts at one of the screen's edges: the left takes the dashboard away, the top brings the
// settings down, the right the drawer in. Streamed, the page is the browser's; drawn, a finger
// moving up or down scrolls it and one moving along a tile with a level slides the level.
func (d *Display) dashGesture(g touch.Gesture) {
	if d.r == nil {
		return
	}
	d.mu.Lock()
	d.dashTouched = time.Now()
	d.mu.Unlock()
	edge := d.r.drawerEdge()
	f := dashboard.Get()
	streamed := f.Mode() == config.DashboardStreamed

	switch g.Kind {
	case touch.Tap:
		if streamed {
			f.Touch("tap", g.X, g.Y)
		} else {
			d.drawnTap(g.X, g.Y)
		}
	case touch.Hold:
		from := edgeNone
		// On the dashboard the edges are the strips beside the page, not the clock's wider bands, and
		// a finger that lands on a tile is the tile's even there: sliding a light on the right-hand
		// side is not opening the drawer.
		edge = min(edge, d.r.margin)
		onTile := !streamed && d.tileAt(g.X, g.Y) != nil
		switch {
		case onTile:
		case g.X < edge:
			from = edgeLeft
		case g.X >= d.r.w-edge:
			from = edgeRight
		case g.Y < topEdge/3:
			// A thinner band than the clock's: the top of a dashboard is where its own tabs are.
			from = edgeTop
		}
		d.mu.Lock()
		d.dashEdge, d.dashEdgeAt = from, image.Pt(g.X, g.Y)
		d.dashDrag = drawnDrag{startScroll: d.dashScroll}
		d.mu.Unlock()
		if from == edgeNone {
			if streamed {
				f.Touch("down", g.X, g.Y)
			} else {
				d.drawnHold(g.X, g.Y)
			}
		}
	case touch.Drag:
		d.mu.Lock()
		from := d.dashEdge
		d.mu.Unlock()
		if from != edgeNone {
			return
		}
		if streamed {
			f.Touch("move", g.X, g.Y)
		} else {
			d.drawnMove(g.X, g.Y)
		}
	case touch.Release:
		d.mu.Lock()
		from, start := d.dashEdge, d.dashEdgeAt
		d.dashEdge = edgeNone
		d.mu.Unlock()
		far := d.r.s(80)
		switch from {
		case edgeNone:
			if streamed {
				f.Touch("up", g.X, g.Y)
			} else {
				d.drawnRelease()
			}
		case edgeLeft:
			if g.X-start.X > far {
				d.closeDashboard()
			}
		case edgeRight:
			if start.X-g.X > far {
				d.openDrawerOver()
			}
		case edgeTop:
			if g.Y-start.Y > far {
				d.showSheet(true)
			}
		}
	}
}

// openDrawerOver brings the drawer in over the dashboard, on the tab it was last on.
func (d *Display) openDrawerOver() {
	d.mu.Lock()
	tab := d.drawerTab
	d.mu.Unlock()
	d.openDrawer(tab)
}

// drawnDashboard is the drawn dashboard over the whole panel.
func (r *renderer) drawnDashboard(s scene) {
	r.dashPage(s.drawn, s.dashScroll, s.dashAdjust, r.dst.Rect)
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
