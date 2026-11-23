//go:build !dot && !spot

package display

import (
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
)

// The apps list a tap on a panel's clock opens (discussion #652): the same list as Settings → General →
// Apps, drawn on its own over the clock or the dashboard instead of over the settings. A pick opens the
// app; a tap anywhere else, or on the list's title, puts it away. Only a panel ever opens it
// (openTabletApps); on an Echo appsPop stays false.

// appsPopup draws the list over the page drawn under it, and makes its zones the screen's.
func (r *renderer) appsPopup(s scene) {
	p, ok := tabletAppsPicker()
	if !ok {
		return
	}
	r.pending = r.pending[:0]
	r.press.on = false
	pickMax := r.picker(p, s.pickScroll)
	r.appsNext = true // the screen's once the frame is done (renderer.draw)
	r.zmu.Lock()
	r.zones, r.pending = r.pending, r.zones
	r.cardMax, r.pickMax = 0, pickMax
	r.zmu.Unlock()
}

// appsPopFor is how long the list stays up untouched before it closes by itself.
const appsPopFor = 30 * time.Second

// closeAppsPop puts the list away.
func (d *Display) closeAppsPop() {
	d.mu.Lock()
	d.appsPop, d.pickScroll = false, 0
	d.mu.Unlock()
}

// appsPopGesture is a finger on the list: a pick opens that app, a swipe scrolls a long list, and
// anything else puts it away.
func (d *Display) appsPopGesture(g touch.Gesture) {
	if d.r == nil {
		d.closeAppsPop()
		return
	}
	switch g.Kind {
	case touch.SwipeUp, touch.SwipeDown:
		d.mu.Lock()
		d.appsPopAt = time.Now()
		d.mu.Unlock()
		by := d.r.s(notchPx)
		if g.Kind == touch.SwipeDown {
			by = -by
		}
		_, pickMax := d.r.scrollLimits()
		d.mu.Lock()
		d.pickScroll = min(max(d.pickScroll+by, 0), pickMax)
		d.mu.Unlock()
	case touch.Tap:
		z, ok := d.r.zoneAt(g.X, g.Y)
		d.closeAppsPop()
		if ok && z.kind == zoneOption {
			chooseTabletApp(z.opt)
		}
	default:
		d.closeAppsPop()
	}
}
