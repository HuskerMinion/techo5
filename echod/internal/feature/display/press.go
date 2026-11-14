//go:build !dot && !spot

package display

import (
	"image"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
)

// The finger a press is shown under (press_draw.go draws it): where it came down, from the moment the
// touchscreen says so until a moment after it lifts. It is let go on the lift, when the page it came
// down on is no longer the one drawn, when the finger goes on to swipe or drag, and after pressMax
// whatever else happens, so nothing is left looking pressed.

const (
	// pressLinger is how long a press shows after the finger has lifted: a quick tap is over before
	// the frame that shows it is drawn.
	pressLinger = 120 * time.Millisecond
	// pressMax is the longest a press shows under a finger that stays down.
	pressMax = 4 * time.Second
)

// pressState is the press being shown, if any (set).
type pressState struct {
	set   bool
	at    image.Point
	down  bool      // the finger is still on the panel
	since time.Time // when it came down
	until time.Time // lifted: shown until then
	page  pageID    // the page it came down on
	seq   uint64    // which press this is, for the timer that ends a long one
}

// pageID is which page a frame draws, as far as a press cares: a press made on one page is not shown on
// another, where something else is under the same point.
type pageID struct {
	phase                                                   string
	call, ring, pin, ask, videoAsk, video, pairing, wifi    bool
	qr, camTest, sheet, picker, camera, alert, calendar     bool
	deck, radar, weather, dash, dashSheet, playing, strip   bool
	drawer, drawerPick, saver, sunrise, redClock, dashboard bool
	cat                                                     category
	custom                                                  string
	tessPage, drawerTab                                     int
}

// pageOf is the page s draws.
func pageOf(s *scene) pageID {
	return pageID{
		phase: s.phase, call: s.callPageUp(), ring: s.ring.any(), pin: s.pin.open, ask: s.setupAsking,
		videoAsk: s.showVideoAsk, video: s.showVideo, pairing: s.bt.Pairing, wifi: s.showWifi,
		qr: s.qrscan != nil, camTest: s.camtest != nil, sheet: s.showSheet, picker: s.showSheet && s.sheet.picker != "",
		camera: s.showCamera, alert: s.showAlert, calendar: s.showCalendar, deck: s.showDeck, radar: s.showRadar,
		weather: s.showWeather, dash: s.showDash, dashSheet: s.showDash && s.dashSheetUp(), playing: s.nowPlaying,
		strip: s.strip, drawer: s.showDrawer, drawerPick: s.showDrawer && s.drawerPick != "",
		saver: s.slideshowScreensaver != nil, sunrise: s.sunrise > 0, redClock: s.redClock,
		cat: s.sheet.cat, custom: s.custom.name, tessPage: s.tessPage, drawerTab: s.drawerTab,
	}
}

// fingerLanded is a finger coming down at p: the press to show, from the next frame. Not on a dark
// panel, where nothing is drawn, nor on a card over the page, which is not the control under it.
func (d *Display) fingerLanded(p touch.Point) {
	at := image.Pt(p.X, p.Y)
	if d.cardAt(at) {
		return
	}
	now := time.Now()
	d.mu.Lock()
	if !d.on {
		d.mu.Unlock()
		return
	}
	d.pressSeq++
	seq := d.pressSeq
	d.press = pressState{set: true, at: at, down: true, since: now, page: d.pageShown, seq: seq}
	d.mu.Unlock()
	d.wake()
	// A finger that never lifts, as far as the touchscreen says: the frame that ends the press is asked
	// for, once, and only if this press is still the one shown.
	time.AfterFunc(pressMax+20*time.Millisecond, func() {
		d.mu.Lock()
		still := d.press.set && d.press.seq == seq
		d.mu.Unlock()
		if still {
			d.wake()
		}
	})
}

// fingerDown hears the finger lift (the touchscreen's Down, false): the press shows a moment longer,
// and then a frame draws the control plain again.
func (d *Display) fingerDown(down bool) {
	if down {
		return
	}
	d.mu.Lock()
	lifted := d.press.set && d.press.down
	if lifted {
		d.press.down, d.press.until = false, time.Now().Add(pressLinger)
	}
	d.mu.Unlock()
	if lifted {
		d.wake()
		time.AfterFunc(pressLinger+20*time.Millisecond, d.wake)
	}
}

// pressClear lets the press go at once: the finger went on to swipe or drag, or held until what it was
// on opened something. It says whether there was one, for a caller that has no frame coming.
func (d *Display) pressClear() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	was := d.press.set
	d.press = pressState{}
	return was
}

// pressMoved is the finger of a press having gone to x, y: further than a tap wanders, it is dragging
// something, and the press is over.
func (d *Display) pressMoved(x, y int) {
	if d.r == nil {
		return
	}
	w := d.r.s(tapWander)
	d.mu.Lock()
	far := d.press.set && (abs(x-d.press.at.X) > w || abs(y-d.press.at.Y) > w)
	if far {
		d.press = pressState{}
	}
	d.mu.Unlock()
}

// pressScene puts the press on the frame, while there is one to show and the frame is still the page it
// was made on, and notes the page for the next finger to land on.
func (d *Display) pressScene(s *scene, now time.Time) {
	page := pageOf(s)
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pageShown = page
	p := &d.press
	if !p.set {
		return
	}
	if p.page != page || (p.down && now.Sub(p.since) >= pressMax) || (!p.down && !now.Before(p.until)) {
		*p = pressState{}
		return
	}
	s.press = pressView{on: true, at: p.at}
}
