//go:build !dot && !spot

package display

import (
	"image"
	"image/draw"
	"log/slog"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/announce"
	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
	"github.com/HuskerMinion/techo5/echod/internal/feature/remind"
	"github.com/HuskerMinion/techo5/echod/internal/feature/setup"
	"github.com/HuskerMinion/techo5/echod/internal/feature/video"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
)

// The dashboard page: a Home Assistant dashboard over the whole screen, drawn here or streamed from a
// dashcast server. A swipe in from the left edge brings it up from the clock, and the same swipe takes
// it away again; "go home" does too. The screen's own edges keep working on it: down from the top is
// the settings, in from the right the drawer, so a dashboard that is the home page does not lock
// anybody out of the rest.
const (
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

// dashForgotten is whether a dashboard opened by hand and last touched at touched has been left long
// enough for the clock to come back: the Dashboard returns setting, ten minutes unless somebody chose.
// Not when it is also the idle page, which dashAway looks after.
func dashForgotten(touched time.Time) bool {
	after, ok := config.Get().Dashboard.Return()
	return ok && time.Since(touched) > after
}

// openDashboard puts the dashboard up, if there is one to put up.
func (d *Display) openDashboard() bool {
	if dashboard.Get().Mode() == config.DashboardOff {
		return false
	}
	d.mu.Lock()
	d.dash, d.dashHeld, d.dashTouched = true, false, time.Now()
	d.drawer, d.sheet, d.deckUp = false, false, false
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

// dashboardAsked is Home Assistant's dashboard_show and dashboard_hide. Shown, it stays until it is
// hidden or put away by hand, rather than the ten minutes one opened by a finger stays. Whatever else
// has the screen - the settings, the drawer, a camera - keeps it until it is done, the dashboard
// waiting behind it.
func (d *Display) dashboardAsked(up bool) {
	d.mu.Lock()
	if up {
		d.dash, d.dashHeld, d.dashTouched, d.dashAwayUntil = true, true, time.Now(), time.Time{}
		d.deckUp = false
		d.mu.Unlock()
		d.wake()
		return
	}
	showing := d.dash || d.dashShowing
	d.mu.Unlock()
	if showing {
		d.closeDashboard()
	}
}

// dashScene decides whether the dashboard is the page, and fetches what it shows. Everything else
// that takes the screen - a sheet, the drawer, a camera, the weather, a turn - comes first; music
// comes first only when the dashboard is standing in for the clock rather than asked for.
func (d *Display) dashScene(s *scene, sheetOrDrawer bool) {
	f := dashboard.Get()
	mode := f.Mode()
	d.mu.Lock()
	if d.dash && !d.dashHeld && dashForgotten(d.dashTouched) {
		d.dash = false
	}
	asked := d.dash
	away := time.Now().Before(d.dashAwayUntil)
	d.mu.Unlock()

	want := mode != config.DashboardOff && s.phase == "idle" && !sheetOrDrawer &&
		!s.showCamera && !s.showWeather && !s.showRadar && !s.showCalendar && !s.showDeck && !s.showWifi && !s.bt.Pairing &&
		(asked || (f.Idle() && !away && !s.nowPlaying))
	s.showDash, s.dashMode = want, mode

	streamed := want && mode == config.DashboardStreamed
	if streamed && d.r != nil {
		s.dash = f.Stream(d.r.w, d.r.h)
	}
	if want && mode == config.DashboardDrawn {
		s.drawn = f.Drawn(d.r.w)
		s.dashTiles = config.Get().Dashboard.Tiles
		d.mu.Lock()
		// A different dashboard starts at its top, and none is scrolled past its end: a short one
		// chosen after a long one scrolled down would otherwise be all above the screen.
		if path := config.Get().Dashboard.Path; path != d.dashScrollFor {
			d.dashScroll, d.dashScrollFor = 0, path
		}
		if _, content := d.r.dash(); content > 0 {
			d.dashScroll = min(d.dashScroll, max(content-d.r.h, 0))
		}
		s.dashScroll, s.dashAdjust, s.dashColor = d.dashScroll, d.dashAdjust, d.dashColor
		d.mu.Unlock()
	}

	over := d.overDashboard(s)
	d.mu.Lock()
	d.dashShowing = want
	// Either way the page wants every finger as it moves: streamed, to scroll the page under it;
	// drawn, to scroll and to slide a tile's level. The rest of the screen wants swipes.
	follow := want
	changed := follow != d.dashFollow
	d.dashFollow = follow
	// Drawn, a finger held still on a light is a long press for its colors (color_sheet.go), which
	// needs holds reported; the color sheet goes with the page. Not while something that takes only a
	// tap is drawn over the page (overDashboard): with holds on, a slow press on it would be a hold
	// and a release, which it does not answer, and which the page under it would.
	holds := want && mode == config.DashboardDrawn && !over
	holdsChanged := holds != d.dashHolds
	d.dashHolds = holds
	if !(want && mode == config.DashboardDrawn) {
		d.dashColor = nil
	}
	d.mu.Unlock()
	if changed {
		touch.Get().SetFollow(follow)
	}
	if holdsChanged {
		d.applyHolds()
	}
}

// overDashboard is whether something that acts on a tap alone is drawn over the dashboard: a
// reminder's card, an event's pop-up, the PIN pad, the setup page's Allow and Deny, an announcement
// arriving or being recorded, a DLNA video asking to be shown, or the video page.
func (d *Display) overDashboard(s *scene) bool {
	_, announcing := announce.Get().Showing()
	_, reminding := remind.Get().Showing()
	_, _, _, videoAsking := video.Get().Asking()
	return s.pin.open || setup.Get().Waiting() || announce.Get().Recording() || announcing || reminding ||
		d.popupUp() != nil || videoAsking || d.videoUp()
}

// applyHolds tells the touchscreen whether to report a finger held still: for the night light's way up
// (gesture) or the drawn dashboard's long press. What is wanted is read and set in one step under
// holdsMu, so of two changes at once the later one is what the touchscreen is left with.
func (d *Display) applyHolds() {
	d.holdsMu.Lock()
	defer d.holdsMu.Unlock()
	d.mu.Lock()
	holds := d.holdsWanted()
	d.mu.Unlock()
	touch.Get().SetHolds(holds)
}

// holdsWanted is whether a finger held still is to be reported: for the night light, and for the
// drawn dashboard only while the panel is lit. Dark, the dashboard's own wish is left over from before
// (dashScene does not run while the panel is off), and the dark panel wakes on a tap: with holds on, a
// slow press there - a half-asleep hand on a night alarm - would be a hold and a release, and nothing.
// Called with mu held.
func (d *Display) holdsWanted() bool { return d.nightGlow || (d.dashHolds && d.on) }

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
				d.drawnRelease(g.X, g.Y)
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
	r.dashPage(s.drawn, s.dashScroll, s.dashAdjust, r.dst.Rect, s.dashTiles)
	r.colorSheet(s.dashColor, s.drawn.Theme)
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
