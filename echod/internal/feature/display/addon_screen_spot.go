//go:build spot

package display

import (
	"fmt"
	"image"
	"image/color"
	"log/slog"
	"strings"
	"time"

	xdraw "golang.org/x/image/draw"

	"github.com/HuskerMinion/techo5/echod/internal/feature/addonhost"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	"github.com/HuskerMinion/techo5/echod/internal/lib/picscale"

	"github.com/HuskerMinion/techo5/echod/pkg/addonapi"
)

// An add-on's page and card on the Spot (stage 2 of the add-on API), as on the Show (addon_screen.go)
// but for the round face: the page is reached from the ring menu's Add-ons item (straight to the page
// with one add-on, through a dial of them with more), its name bar a band across the top of the circle
// and its way out a round button at the foot, its content inside the circle. The card sits along the
// foot of the clock face and of the night clock, at most a third of the face.

// itemAddons is the ring menu's Add-ons item, there while an add-on with a page runs; modeAddons the
// dial of them, with more than one.
const itemAddons itemID = "addons"

const modeAddons menuMode = 100

// addonItemColor is the add-ons' color on the ring.
var addonItemColor = color.RGBA{150, 200, 90, 255}

// addonItem is an add-on's item on the add-ons dial, by its id, and addonOf the add-on an item is. By id,
// not by its place on the dial: the list of add-ons is asked for again every few seconds, and one
// starting or stopping between the dial being drawn and a choice would move the others along, so a place
// could open another add-on than the one chosen.
func addonItem(id string) itemID { return itemID("addon:" + id) }

func addonOf(item itemID) (string, bool) {
	id, ok := strings.CutPrefix(string(item), "addon:")
	return id, ok && id != ""
}

// addonTile is the running add-on of that id with a page, if it runs.
func addonTile(id string) (addonhost.ScreenTile, bool) {
	for _, t := range addonTiles() {
		if t.ID == id {
			return t, true
		}
	}
	return addonhost.ScreenTile{}, false
}

// addonItems is the add-ons dial: one item for each running add-on with a page.
func addonItems() []menuItem {
	tiles := addonTiles()
	out := make([]menuItem, len(tiles))
	for i, t := range tiles {
		out[i] = menuItem{addonItem(t.ID), addonItemColor}
	}
	return out
}

// withAddons is the main dial with the Add-ons item before Settings, when an add-on with a page runs.
func withAddons(items []menuItem) []menuItem {
	if len(addonTiles()) == 0 {
		return items
	}
	out := make([]menuItem, 0, len(items)+1)
	for _, it := range items {
		if it.id == itemSettings {
			out = append(out, menuItem{itemAddons, addonItemColor})
		}
		out = append(out, it)
	}
	return out
}

// addonItemName and addonItemHint are an add-on item's words on the dial.
func addonItemName(id itemID) (string, bool) {
	if id == itemAddons {
		return tr("drawer_addons"), true
	}
	if a, ok := addonOf(id); ok {
		if t, ok := addonTile(a); ok {
			return t.Name, true
		}
		return tr("drawer_addons"), true
	}
	return "", false
}

func addonItemHint(id itemID) (string, bool) {
	if id != itemAddons {
		_, ok := addonOf(id)
		return "", ok
	}
	var names []string
	for _, t := range addonTiles() {
		names = append(names, t.Name)
	}
	return strings.Join(names, " · "), true
}

// addonIcon draws an add-on item: the Add-ons item a puzzle piece, an add-on the icon its catalog entry
// gives it or else its initials, in a ring, as a camera is drawn.
func (r *roundRenderer) addonIcon(id itemID, x, y, u, w float64, c color.RGBA) bool {
	if id == itemAddons {
		side := int(2.2 * u)
		r.mdiIconIn("mdi:puzzle-outline", image.Rect(int(x)-side/2, int(y)-side/2, int(x)+side/2, int(y)+side/2), side, c)
		return true
	}
	a, ok := addonOf(id)
	if !ok {
		return false
	}
	name, _ := addonItemName(id)
	r.ringAt(x, y, u-w/2, u+w/2, 0, 6.2832, c)
	if t, ok := addonTile(a); ok && addonHasIcon(t.Icon) {
		// The icon its catalog entry gives it, in the ring, as a camera's initials are.
		side := int(1.3 * u)
		r.mdiIconIn(t.Icon, image.Rect(int(x)-side/2, int(y)-side/2, int(x)+side/2, int(y)+side/2), side, c)
		return true
	}
	face := r.label
	if u > 20 {
		face = r.title
	}
	in := addonInitials(name)
	r.text(face, in, int(x)-r.width(face, in)/2, int(y)+int(u*0.38), c)
	return true
}

// actAddons does what the Add-ons item or an add-on's item says: the add-on's page, or with several the
// dial of them.
func (d *Display) actAddons(id itemID) bool {
	tiles := addonTiles()
	switch {
	case id == itemAddons && len(tiles) == 1:
		d.addonTileTap(tiles[0].ID)
	case id == itemAddons && len(tiles) > 1:
		d.locked(func() { d.openMenu(modeAddons, addonItem(tiles[0].ID)) })
		d.wake()
	case id == itemAddons:
	default:
		a, ok := addonOf(id)
		if !ok {
			return false
		}
		// The add-on chosen, by its id; one that stopped meanwhile opens nothing.
		d.addonTileTap(a)
	}
	return true
}

// addonTileTap is a choice of an add-on on the menu: its page comes up, and the add-on may bring it up
// itself for a moment after (screen.open), somebody having asked for it.
func (d *Display) addonTileTap(id string) {
	h := addonHost()
	if h == nil || !addonRunning(id) {
		return
	}
	h.Asked(id)
	d.openAddon(id, addonName(id))
}

// openAddon puts the add-on's page up: the menu goes, and the page is drawn once nothing more pressing
// is up.
func (d *Display) openAddon(id, name string) {
	d.mu.Lock()
	if d.addon.page != id {
		d.addon.scrolls = [maxSceneLists]int{}
	}
	d.addon.page, d.addon.name, d.addon.touched = id, name, time.Now()
	if d.menuOpen {
		d.closeMenu()
	}
	d.mu.Unlock()
	slog.Info("add-on page up", "addon", id)
	d.wake()
}

// addonLiftNight takes the red night clock down for a page somebody asked for, as a touch does.
func (d *Display) addonLiftNight() { d.liftNight() }

// addonBackEdge is how near the left edge of the circle a swipe right starts to be the back gesture.
func (d *Display) addonBackEdge() int { return 70 }

// addonPageSpot draws the add-on's page on the round face.
func (r *roundRenderer) addonPageSpot(s roundScene) {
	v := s.addonPage
	pal := themePalette()
	fillCanvas(r.dst, walnut)
	bar, closeAt, content := r.addonPageRooms()
	var lay *sceneLayout
	if v.has {
		lay = r.laid(&r.addon.page, v.id, v.sc, v.local, content, false, v.images)
		r.drawScene(lay, v.scrolls[:], image.Point{}, pal, timersIn(s.timers), v.pics)
	} else {
		f := sceneFace(r.faceKey(), r.sceneMetrics().text[1], false)
		msg := r.fit(f, fmt.Sprintf(tr("addon_waiting"), v.name), content.Dx())
		r.text(f, msg, center-r.width(f, msg)/2, center, dim)
	}
	r.addonBar(v.name, v.icon, bar, closeAt, pal, 0)
	if s.showVolume {
		// The volume being turned, over the page: the page dimmed under it, so its number reads, and
		// still in view.
		r.dim(184)
		r.volume(s)
	}
	r.zmu.Lock()
	r.addon.pageAt.id, r.addon.pageAt.lay, r.addon.pageAt.scrolls, r.addon.pageAt.close = v.id, lay, v.scrolls, closeAt
	r.zmu.Unlock()
}

// spotAddonCard is where the card goes on the round face: along the foot of the clock, inside the circle,
// a third of the face at the most.
var spotAddonCard = image.Rect(104, side-44-160, side-104, side-44)

// addonCardTop is where the card over the clock face begins, as tall as what it holds; 0 with none.
func (r *roundRenderer) addonCardTop(s roundScene) int {
	if s.addonCard == nil {
		return 0
	}
	m := r.sceneMetrics()
	label := sceneFace(r.faceKey(), m.text[0], true)
	box := spotAddonCard
	inner := image.Rect(box.Min.X+m.pad, box.Min.Y+m.pad+lineH(label)+m.gap[1], box.Max.X-m.pad, box.Max.Y-m.pad)
	lay := r.laidAt(&r.addon.card, s.addonCard.id, s.addonCard.sc, s.addonCard.local, inner, true, s.nightClockFace(), s.addonCard.images)
	return box.Max.Y - min(box.Dy(), lay.height+2*m.pad+lineH(label)+m.gap[1])
}

// addonSqueezed draws what f draws for the whole face smaller, into the circle's width above bottom, as a
// face laid out for the whole circle cannot make room for the card itself: drawn on the face's ground,
// scaled, and put down where it is not that ground, so a photo behind the face shows through it.
func (r *roundRenderer) addonSqueezed(bottom int, f func()) {
	if bottom <= 0 {
		return
	}
	full := r.dst
	if r.squeeze == nil || r.squeeze.Rect != full.Rect {
		r.squeeze = image.NewRGBA(full.Rect)
	}
	fillCanvas(r.squeeze, colBackground)
	r.dst = r.squeeze
	f()
	r.dst = full
	w := full.Rect.Dx() * bottom / full.Rect.Dy()
	small := image.NewRGBA(image.Rect(0, 0, w, bottom))
	picscale.CatmullRom.Scale(small, small.Rect, r.squeeze, r.squeeze.Rect, xdraw.Src)
	x0 := full.Rect.Min.X + (full.Rect.Dx()-w)/2
	for y := range bottom {
		for x := range w {
			c := small.RGBAAt(x, y)
			if c != colBackground {
				full.SetRGBA(x0+x, full.Rect.Min.Y+y, c)
			}
		}
	}
}

// addonCardSpot draws the card over the clock face or the night clock.
func (r *roundRenderer) addonCardSpot(s roundScene) {
	if s.addonCard == nil || addonCardUnpainted {
		return
	}
	m := r.sceneMetrics()
	labelH := lineH(sceneFace(r.faceKey(), m.text[0], true))
	box := spotAddonCard
	inner := image.Rect(box.Min.X+m.pad, box.Min.Y+m.pad+labelH+m.gap[1], box.Max.X-m.pad, box.Max.Y-m.pad)
	r.addonCardIn(s.addonCard, box, inner, &r.addon, timersIn(s.timers), s.nightClockFace(), &r.nightBuf)
}

// addonSceneSpot fills in the add-on's page, when one is up and nothing that comes before it has the face.
func (d *Display) addonSceneSpot(s *roundScene) {
	h := addonHost()
	if h == nil {
		return
	}
	// The volume is not among them: it is drawn over the page, dimmed for it, and the page stays in view
	// under it (addonPageSpot), as on the Show. A call is, whatever its phase: the call's screens are the
	// device's.
	over := s.phase != "idle" || s.call.Phase != phone.Idle || s.ringing.any() || s.pin.open || s.setupAsking || s.showVideoAsk ||
		s.firstRunUp || s.announceRecording || s.showNotification || s.showReminder || s.showAnnouncement || s.showAlert ||
		s.sheetOpen || s.menuOpen || s.showCamera || s.btPairing || s.showVideo || s.nightClockFace()
	d.mu.Lock()
	id, name, touched := d.addon.page, d.addon.name, d.addon.touched
	d.mu.Unlock()
	if id == "" || d.addonPageGone(id, touched) || over {
		return
	}
	s.addonPage = d.addonPageNow(h, id, name)
}

// addonCardSceneSpot fills in the newest card, over the clock face or the night clock with nothing over
// it.
func (d *Display) addonCardSceneSpot(s *roundScene) {
	h := addonHost()
	if h == nil || s.addonPage != nil {
		return
	}
	night := s.nightClockFace() && !s.ringing.any() && !s.showNotification
	day := !s.redClock && s.phase == "idle" && s.call.Phase == phone.Idle && !s.ringing.any() && !s.showVolume && !s.showCamera &&
		!s.nowPlaying && !s.menuOpen && !s.sheetOpen && !s.showDash && s.slideshowScreensaver == nil && s.sunrise == 0 &&
		!s.pin.open && !s.setupAsking && !s.showVideoAsk && !s.showVideo && !s.firstRunUp && !s.announceRecording &&
		!s.showNotification && !s.showReminder && !s.showAnnouncement && !s.showAlert && !s.btPairing
	if !night && !day {
		return
	}
	if best := d.addonCardNow(h, night); best != nil {
		v := *best
		v.local = d.addonChangedIn(v.id, addonapi.OnCard, &v.sc)
		s.addonCard = &v
		d.mu.Lock()
		d.addon.frameCard = addonShown{best.id, best.sc.Set}
		d.mu.Unlock()
	}
}
