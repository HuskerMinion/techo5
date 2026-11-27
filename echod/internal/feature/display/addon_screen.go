//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"log/slog"
	"time"

	"github.com/HuskerMinion/techo5/echod/pkg/addonapi"
)

// An add-on's page and card on the Show (stage 2 of the add-on API). The page is reached from the
// drawer's Add-ons tab, or brought up by the add-on in answer to somebody (the server allows screen.open
// only then), and is drawn full screen under a name bar that is the daemon's: the add-on's name and
// mark, and the way out, which always leaves. It waits behind everything more pressing, as the deck
// does: a turn, a ring, a call, the settings and their PIN pad, the setup page's and a video's questions,
// Cast, a camera, the Wi-Fi and Bluetooth pages, first-run, and the pages Home Assistant or a turn put
// up. While one of those is up the page is not drawn and gets no finger, and the add-on is told it is out
// of view. The card shows on the clock and the night clock alone, at the foot, at most a third of the
// screen, under the device's own cards and strips.

// addonPage draws the add-on's page: its name bar, then its scene.
func (r *renderer) addonPage(s scene) {
	v := s.addonPage
	pal := themePalette()
	bar, closeAt, content := r.addonPageRooms()
	fillCanvas(r.dst, walnut)
	var lay *sceneLayout
	if v.has {
		lay = r.laid(&r.addon.page, v.id, v.sc, v.local, content, false, v.images)
		r.drawScene(lay, v.scrolls[:], image.Point{}, pal, timersIn(s.timers), v.pics)
	} else {
		f := sceneFace(r.faceKey(), r.sceneMetrics().text[1], false)
		msg := r.fit(f, fmt.Sprintf(tr("addon_waiting"), v.name), content.Dx())
		r.text(f, msg, content.Min.X+(content.Dx()-r.width(f, msg))/2, content.Min.Y+content.Dy()/2, dim)
	}
	r.addonBar(v.name, v.icon, bar, closeAt, pal, r.addonPillAt(s).Min.X)
	r.zmu.Lock()
	r.addon.pageAt.id, r.addon.pageAt.lay, r.addon.pageAt.scrolls, r.addon.pageAt.close = v.id, lay, v.scrolls, closeAt
	r.zmu.Unlock()
}

// addonPillAt is where the device's own pill (the microphone off, a lost connection: headerLine) goes
// over an add-on's page: in its name bar, at the end before the way out, rather than across the middle of
// the bar where it covered the add-on's name, which says whose page it is. Empty with no pill.
func (r *renderer) addonPillAt(s scene) image.Rectangle {
	line := headerLine(s)
	if line == "" || s.addonPage == nil {
		return image.Rectangle{}
	}
	bar, closeAt, _ := r.addonPageRooms()
	w, pad := r.width(r.tiny, line), r.s(14)
	right := closeAt.Min.X - r.s(16)
	top := bar.Min.Y + (bar.Dy()-r.s(28))/2
	return image.Rect(right-w-2*pad, top, right, top+r.s(28))
}

// addonCardRoom is where the card goes over the clock: along the foot, where the Next up card is, at most
// a third of the screen tall; h is how tall what it holds is.
func (r *renderer) addonCardRoom(s scene) (box, inner image.Rectangle) {
	left := r.margin
	if s.callButton {
		left = r.callButtonRect().Max.X + r.s(12)
	}
	bottom := r.h - r.s(24)
	if footerBusy(s) {
		bottom = r.h - r.s(46)
	}
	m := r.sceneMetrics()
	labelH := lineH(sceneFace(r.faceKey(), m.text[0], true))
	most := r.h / 3
	inner = image.Rect(left+m.pad, bottom-most+m.pad+labelH+m.gap[1], r.w-r.margin-m.pad, bottom-m.pad)
	return image.Rect(left, bottom-most, r.w-r.margin, bottom), inner
}

// addonCard draws the card over the clock, by day or in the night clock's tone.
func (r *renderer) addonCard(s scene) {
	if s.addonCard == nil || addonCardUnpainted {
		return
	}
	box, inner := r.addonCardRoom(s)
	r.addonCardIn(s.addonCard, box, inner, &r.addon, timersIn(s.timers), s.redClockFace(), &r.nightBuf)
}

// addonCardReserve is how much of the night clock's foot the card takes, for the clock to keep clear of.
func (r *renderer) addonCardReserve(s scene) int {
	if s.addonCard == nil {
		return 0
	}
	box, inner := r.addonCardRoom(s)
	return r.h - box.Max.Y + r.addonCardHeight(s.addonCard, box, inner, &r.addon, s.redClockFace()) + r.s(nightInfoGap)
}

// addonCardHeight is how tall v's card is in box, laid out in inner, over the night clock or not.
func (p *paint) addonCardHeight(v *addonCardView, box, inner image.Rectangle, drawn *addonDrawn, night bool) int {
	lay := p.laidAt(&drawn.card, v.id, v.sc, v.local, inner, true, night, v.images)
	m := p.sceneMetrics()
	return min(box.Dy(), lay.height+2*m.pad+lineH(sceneFace(p.faceKey(), m.text[0], true))+m.gap[1])
}

// addonCardTop is where the card over the clock begins, as tall as what it holds; 0 with none.
func (r *renderer) addonCardTop(s scene) int {
	if s.addonCard == nil {
		return 0
	}
	box, inner := r.addonCardRoom(s)
	return box.Max.Y - r.addonCardHeight(s.addonCard, box, inner, &r.addon, s.redClockFace())
}

// addonClear forgets where the add-on's page and card were drawn, for a frame that draws neither.
func (r *renderer) addonClear(s scene) {
	r.addonForget(&r.addon, s.addonPage == nil, s.addonCard == nil)
}

// openAddon puts the add-on's page up: the drawer goes, and the page is drawn once nothing more pressing
// is up.
func (d *Display) openAddon(id, name string) {
	d.mu.Lock()
	if d.addon.page != id {
		d.addon.scrolls = [maxSceneLists]int{}
	}
	d.addon.page, d.addon.name, d.addon.touched = id, name, time.Now()
	d.drawer, d.drawerPick, d.appsPop = false, "", false
	d.mu.Unlock()
	slog.Info("add-on page up", "addon", id)
	d.wake()
}

// addonScene fills in the add-on's page, when one is up and nothing that comes before it has the screen;
// called before the deck, the custom screens and the dashboard, which it comes before.
func (d *Display) addonScene(s *scene, now time.Time) {
	h := addonHost()
	if h == nil {
		return
	}
	over := s.showSheet || s.showDrawer || s.showCamera || s.showWifi || s.showAlert || s.showCalendar ||
		s.showWeather || s.showRadar || s.bt.Pairing || s.showCast || s.showVideo || s.showVideoAsk || s.setupAsking ||
		s.pin.open || s.ring.any() || s.callPageUp() || s.camtest != nil || s.qrscan != nil || s.redClockFace() ||
		s.phase != "idle"
	d.mu.Lock()
	id, name, touched := d.addon.page, d.addon.name, d.addon.touched
	d.mu.Unlock()
	if id == "" || d.addonPageGone(id, touched) || over {
		return
	}
	s.addonPage = d.addonPageNow(h, id, name)
	_ = now
}

// addonCardScene fills in the newest card over the clock, when the frame is the clock (by day, not under
// the screensaver or the light before an alarm, nor with the drawer or a strip over it) or the night
// clock with nothing over it.
func (d *Display) addonCardScene(s *scene, clock bool) {
	h := addonHost()
	if h == nil {
		return
	}
	night := s.redClockFace() && !s.ring.any() && !s.showNotification
	day := clock && !s.redClock && s.sunrise == 0 && s.slideshowScreensaver == nil && !s.showDrawer && !s.appsPop &&
		!s.showAnnouncement && !s.announceRecording
	if !night && !day {
		return
	}
	best := d.addonCardNow(h, night)
	if best == nil {
		return
	}
	v := *best
	v.local = d.addonChangedIn(v.id, addonapi.OnCard, &v.sc)
	s.addonCard = &v
	// It takes the foot: the Next up card and the chips wait under it until it goes.
	s.nextUp, s.glance = nil, nil
	d.mu.Lock()
	d.addon.frameCard = addonShown{best.id, best.sc.Set}
	d.mu.Unlock()
}

// addonCovered takes the page out of a frame whose setup page's question or video turned out, once the
// frame had looked further, to be over it.
func (d *Display) addonCovered(s *scene) {
	if s.addonPage == nil || !(s.setupAsking || s.showVideo || s.showVideoAsk) {
		return
	}
	s.addonPage = nil
	d.mu.Lock()
	d.addon.framePage = addonShown{}
	d.mu.Unlock()
}

// addonRows is the drawer's Add-ons tab: each running add-on that has a page, to open it.
func addonRows() ([]settingRow, string) {
	tiles := addonTiles()
	if len(tiles) == 0 {
		return nil, tr("drawer_no_addons")
	}
	rows := make([]settingRow, 0, len(tiles))
	for _, t := range tiles {
		rows = append(rows, settingRow{id: "addon:" + t.ID, label: t.Name, kind: ctlButton, button: tr("ui_open"), rowTap: true,
			mark: &rowMark{name: t.Name, icon: t.Icon}})
	}
	return rows, ""
}

// addonTileTap is a tap on an add-on's row under the Add-ons tab: its page comes up, and the add-on may
// bring it up itself for a moment after (screen.open), somebody having asked for it.
func (d *Display) addonTileTap(id string) {
	h := addonHost()
	if h == nil || !addonRunning(id) {
		return
	}
	d.closeDrawer()
	h.Asked(id)
	d.openAddon(id, addonName(id))
}

// addonLiftNight lifts the night light for a page somebody asked for, as a long press does.
func (d *Display) addonLiftNight() { d.liftNightLight("an add-on's page somebody asked for") }

// addonBackEdge is how near the left edge a swipe right starts to be the back gesture: where a swipe in
// opens the dashboard from the clock.
func (d *Display) addonBackEdge() int { return d.r.drawerEdge() }
