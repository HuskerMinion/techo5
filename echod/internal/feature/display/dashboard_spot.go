//go:build spot

package display

import (
	"image"
	"log/slog"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
)

// The dashboard on the Spot: the Dashboard item of the ring menu puts it up and takes it down, and it
// can stand in for the clock. Drawn, it is one column inside the circle, scrolled by a finger; a
// finger sliding along a tile sets its level, as on the Show. A finger held still, and let go, brings
// the ring menu up, which is the way back to everything else. Streamed, the page is the whole round
// face, the corners Home Assistant draws falling outside it.

// spotDashArea is where a drawn dashboard goes on the round panel: a column the circle has room for
// from top to bottom, clear of the rim.
var spotDashArea = image.Rect(76, 60, 404, 430)

const (
	// spotDashForget and spotDashAway are the Show's dashForget and dashAway: how long an opened
	// dashboard stays up untouched, and how long the clock stays after the idle one is put away.
	spotDashForget = 10 * time.Minute
	spotDashAway   = 2 * time.Minute

	// spotStill is how far a finger may wander on this panel and still be held still (the touch
	// screen's own tapMove), and spotHold how long it stays for the ring menu.
	spotStill = 40
	spotHold  = 450 * time.Millisecond
)

// toggleDashboard is the menu's Dashboard item: up if it is down, down if it is up.
func (d *Display) toggleDashboard() {
	d.mu.Lock()
	up := d.dashShowing
	d.closeMenu()
	if up {
		d.dash = false
		if dashboard.Get().Idle() {
			d.dashAwayUntil = time.Now().Add(spotDashAway)
		}
	} else {
		d.dash, d.dashTouched, d.dashAwayUntil = true, time.Now(), time.Time{}
	}
	d.mu.Unlock()
	slog.Info("dashboard", "up", !up)
	d.wake()
}

// dashSceneSpot decides whether the dashboard is the face, and fetches what it shows. A call, a
// ring, the settings, a camera and a turn come first; music only when the dashboard is standing in
// for the clock rather than asked for.
func (d *Display) dashSceneSpot(s *roundScene) {
	f := dashboard.Get()
	mode := f.Mode()
	d.mu.Lock()
	if d.dash && time.Since(d.dashTouched) > spotDashForget {
		d.dash = false
	}
	asked, away, menu := d.dash, time.Now().Before(d.dashAwayUntil), d.menuOpen
	d.mu.Unlock()

	want := mode != config.DashboardOff && s.phase == "idle" && !s.sheetOpen && !s.showCamera &&
		(asked || (f.Idle() && !away && !s.nowPlaying))
	s.showDash, s.dashMode = want, mode

	if want && mode == config.DashboardStreamed {
		s.dash = f.Stream(d.r.w, d.r.h)
	} else {
		f.Close()
	}
	if want && mode == config.DashboardDrawn {
		s.drawn = f.Drawn(spotDashArea.Dx())
		d.mu.Lock()
		if path := config.Get().Dashboard.Path; path != d.dashScrollFor {
			d.dashScroll, d.dashScrollFor = 0, path
		}
		if d.r.dashContent > 0 {
			d.dashScroll = min(d.dashScroll, max(d.r.dashContent-d.r.h, 0))
		}
		s.dashScroll, s.dashAdjust = d.dashScroll, d.dashAdjust
		d.mu.Unlock()
	} else {
		f.CloseDrawn()
	}

	d.mu.Lock()
	d.dashShowing = want
	was := d.dashFollow
	d.dashFollow = want && !menu
	d.mu.Unlock()
	// The menu sets the touch screen for its own ring and puts it back when it closes, so this is
	// said every frame the dashboard is up and the menu is not, rather than only when it changes.
	switch {
	case want && !menu:
		touch.Get().SetFollow(true)
	case was && !menu:
		touch.Get().SetFollow(false)
	}
}

// dashGestureSpot is a finger on the dashboard face.
func (d *Display) dashGestureSpot(g touch.Gesture) {
	d.mu.Lock()
	d.dashTouched = time.Now()
	d.mu.Unlock()
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
		d.mu.Lock()
		d.dashMoved, d.dashHoldAt, d.dashHoldPt = false, time.Now(), image.Pt(g.X, g.Y)
		d.dashDrag = drawnDrag{startScroll: d.dashScroll}
		d.mu.Unlock()
		if streamed {
			f.Touch("down", g.X, g.Y)
		} else {
			d.drawnHold(g.X, g.Y)
		}
	case touch.Drag:
		// This panel's finger wanders when it is held still, which the touch screen reports as moving;
		// it has to go further than that wander before it counts as moving at all.
		d.mu.Lock()
		if !d.dashMoved && (abs(g.X-d.dashHoldPt.X) > spotStill || abs(g.Y-d.dashHoldPt.Y) > spotStill) {
			d.dashMoved = true
		}
		moved := d.dashMoved
		d.mu.Unlock()
		if !moved {
			return
		}
		if streamed {
			f.Touch("move", g.X, g.Y)
		} else {
			d.drawnMove(g.X, g.Y)
		}
	case touch.Release:
		d.mu.Lock()
		still, long, at := !d.dashMoved, time.Since(d.dashHoldAt) >= spotHold, d.dashHoldPt
		d.mu.Unlock()
		switch {
		case still && long:
			// Held still: the ring menu, the way back to everything else.
			if streamed {
				f.Touch("up", at.X, at.Y)
			} else {
				d.drawnRelease()
			}
			d.mu.Lock()
			d.openMenu(modeMain, itemDashboard)
			d.mu.Unlock()
			d.wake()
		case still:
			// A tap that wandered a little on its way.
			if streamed {
				f.Touch("up", at.X, at.Y)
			} else {
				d.drawnRelease()
				d.drawnTap(at.X, at.Y)
			}
		default:
			if streamed {
				f.Touch("up", g.X, g.Y)
			} else {
				d.drawnRelease()
			}
		}
	}
}

// dashFace draws the dashboard over the round panel.
func (r *roundRenderer) dashFace(s roundScene) {
	if s.dashMode == config.DashboardDrawn {
		r.dashPage(s.drawn, s.dashScroll, s.dashAdjust, spotDashArea)
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
	lines := r.wrapLines(r.label, msg, spotDashArea.Dx()-40)
	y := r.h/2 - len(lines)*16
	if drawn {
		y = r.h - 90
	}
	for _, line := range lines {
		y += 32
		r.text(r.label, line, (r.w-r.width(r.label, line))/2, y, colDim)
	}
}
