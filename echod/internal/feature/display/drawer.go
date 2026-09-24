//go:build !dot && !spot

package display

import (
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
)

// openDrawer brings the drawer in on a tab.
func (d *Display) openDrawer(tab int) {
	d.mu.Lock()
	d.drawer, d.drawerTab, d.drawerScroll, d.drawerPick, d.pickScroll = true, tab, 0, "", 0
	d.weatherUntil, d.radar = time.Time{}, false
	d.mu.Unlock()
	if tab == drawerCameras {
		go home.Get().Prewarm()
	}
	slog.Info("drawer", "open", drawerTabs[tab])
	d.wake()
}

// closeDrawer puts it away. It says so, the way openDrawer does: whether a pick closed the drawer or left
// it over what it had opened is the whole of one of the things the hardware testing found, and a screen
// nobody can see is no place to check it.
func (d *Display) closeDrawer() {
	d.mu.Lock()
	d.drawer, d.drawerPick = false, ""
	d.mu.Unlock()
	slog.Info("drawer closed")
	d.wake()
}

// drawerGesture is a finger on the drawer: taps on what it shows, vertical swipes scroll it, and a
// swipe back to the right puts it away.
func (d *Display) drawerGesture(g touch.Gesture) {
	if d.r == nil {
		return
	}
	switch g.Kind {
	case touch.SwipeRight:
		d.closeDrawer()
	case touch.SwipeUp, touch.SwipeDown:
		by := notchPx
		if g.Kind == touch.SwipeDown {
			by = -notchPx
		}
		cardMax, pickMax := d.r.scrollLimits()
		d.mu.Lock()
		if d.drawerPick != "" {
			d.pickScroll = min(max(d.pickScroll+by, 0), pickMax)
		} else {
			d.drawerScroll = min(max(d.drawerScroll+by, 0), cardMax)
		}
		d.mu.Unlock()
	case touch.Tap:
		z, ok := d.r.zoneAt(g.X, g.Y)
		if !ok {
			return
		}
		switch z.kind {
		case zoneDone:
			d.closeDrawer()
		case zoneTab:
			d.mu.Lock()
			d.drawerTab, d.drawerScroll, d.drawerPick = z.opt, 0, ""
			d.mu.Unlock()
			if z.opt == drawerCameras {
				go home.Get().Prewarm()
			}
		case zoneDismiss:
			d.mu.Lock()
			d.drawerPick = ""
			d.mu.Unlock()
		case zoneOption:
			d.mu.Lock()
			id := d.drawerPick
			d.drawerPick = ""
			d.mu.Unlock()
			if id == "radiosource" {
				if sources := home.RadioSources(); z.opt < len(sources) {
					d.mu.Lock()
					d.drawerScroll = 0
					d.mu.Unlock()
					home.Get().SetRadioSource(sources[z.opt])
				}
			}
		case zoneRow:
			d.drawerRowTap(z.id)
		}
	}
}

// drawerRowTap is a tap on one of the drawer's rows.
func (d *Display) drawerRowTap(id string) {
	if id == "radiosource" {
		d.mu.Lock()
		d.drawerPick, d.pickScroll = id, 0
		d.mu.Unlock()
		return
	}
	kind, n, ok := strings.Cut(id, ":")
	i, err := strconv.Atoi(n)
	if !ok || err != nil || i < 0 {
		return
	}
	switch kind {
	case "announce":
		d.announceTap()
	case "call":
		d.mu.Lock()
		list := d.callees
		d.mu.Unlock()
		if i < len(list) {
			d.closeDrawer()
			go func(c phone.Callee) {
				if err := phone.Get().CallCallee(c); err != nil {
					slog.Warn("screen: call", "err", err)
				}
			}(list[i])
		}
	case "cam":
		if cams := home.Get().Cameras(); i < len(cams) {
			d.closeDrawer()
			home.Get().ShowCamera(cams[i].Entity, camListShow)
		}
	case "st":
		rows := radioList(home.Get().Radio())
		if i >= len(rows) {
			return
		}
		// The pick is the last thing the drawer is for: what it leaves you looking at is the page
		// for it, so the drawer goes, the way it does for a camera. A station picked here used to
		// leave the list sitting over the station somebody had just chosen, and Stop the radio left
		// it over the clock, though either pick is as finished with the list as a camera is.
		d.closeDrawer()
		if rows[i] == "■ Stop" {
			home.Get().Stop()
			return
		}
		home.Get().Play(rows[i])
	}
}
