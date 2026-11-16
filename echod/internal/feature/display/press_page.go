//go:build !dot && !spot

package display

import (
	"image"
	"time"
)

// pageID is which page a frame draws, as far as a press cares: a press made on one page is not shown on
// another, where something else is under the same point.
type pageID struct {
	phase                                                 string
	call, ring, pin, ask, videoAsk, video, pairing, wifi  bool
	qr, camTest, sheet, picker, camera, alert, calendar   bool
	deck, radar, weather, dash, dashSheet, playing, strip bool
	drawer, drawerPick, saver, sunrise, redClock          bool
	cat                                                   category
	custom                                                string
	tessPage, drawerTab                                   int
	// cal is the calendar's view, and over the dashboard which sheet is up and the library's page: a
	// tap on a day, on Library or on a tab puts something else under the finger.
	cal      calPage
	dashOver int
	lib      libPageID
}

// calPage is which of the calendar's views a frame draws.
type calPage struct {
	month, day                  int64
	week, agenda, detail, chips bool
}

// libPageID is which page of the music library a frame draws.
type libPageID struct {
	tab, depth   int
	menu, typing bool
}

// pageOf is the page s draws.
func pageOf(s *scene) pageID {
	p := pageID{
		phase: s.phase, call: s.callPageUp(), ring: s.ring.any(), pin: s.pin.open, ask: s.setupAsking,
		videoAsk: s.showVideoAsk, video: s.showVideo, pairing: s.bt.Pairing, wifi: s.showWifi,
		qr: s.qrscan != nil, camTest: s.camtest != nil, sheet: s.showSheet, picker: s.showSheet && s.sheet.picker != "",
		camera: s.showCamera, alert: s.showAlert, calendar: s.showCalendar, deck: s.showDeck, radar: s.showRadar,
		weather: s.showWeather, dash: s.showDash, dashSheet: s.showDash && s.dashSheetUp(), playing: s.nowPlaying,
		strip: s.strip, drawer: s.showDrawer, drawerPick: s.showDrawer && s.drawerPick != "",
		saver: s.slideshowScreensaver != nil, sunrise: s.sunrise > 0, redClock: s.redClock,
		cat: s.sheet.cat, custom: s.custom.name, tessPage: s.tessPage, drawerTab: s.drawerTab,
	}
	if s.showCalendar {
		v := s.cal
		p.cal = calPage{month: v.month.Unix(), day: v.day.Unix(), week: v.week, agenda: v.agenda,
			detail: v.detail != nil, chips: v.chipsOpen}
	}
	if s.showDash {
		switch {
		case s.dashColor != nil:
			p.dashOver = 1
		case s.dashMedia != nil:
			p.dashOver = 2
		case s.dashClimate != nil:
			p.dashOver = 3
		case s.dashFan != nil:
			p.dashOver = 4
		case s.dashLibrary != nil:
			l := s.dashLibrary.sheet
			p.dashOver, p.lib = 5, libPageID{tab: l.tab, depth: len(l.stack), menu: l.menu.open, typing: l.typing}
		}
	}
	return p
}

// pressScene puts the press on the frame, while there is one to show and the frame is still the page it
// was made on, and notes the page for the next finger to land on.
func (d *Display) pressScene(s *scene, now time.Time) {
	s.press = d.pressNow(pageOf(s), now)
}

// pressRefused is whether a finger landing at at, on the page shown, is on no control to show it on: a
// card over the page is not the control under it. A card waiting under the call or ringing page is not
// drawn there, and the finger is on that page's buttons, which show the press.
func (d *Display) pressRefused(at image.Point, shown pageID) bool {
	return !shown.call && !shown.ring && d.cardAt(at)
}
