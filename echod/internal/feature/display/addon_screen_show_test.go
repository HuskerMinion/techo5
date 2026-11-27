//go:build !dot && !spot

package display

import (
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
	"github.com/HuskerMinion/techo5/echod/internal/lib/palette"
	"github.com/HuskerMinion/techo5/echod/pkg/addonapi"
)

var addonNow = time.Date(2026, 11, 27, 18, 7, 0, 0, time.Local)

// addonFrame draws s on a panel, as a frame would, and writes it to dir when dir is set.
func addonFrame(t *testing.T, dir, name string, wide, high int, s scene) (*image.RGBA, *renderer) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, wide, high))
	r := newRenderer(img)
	r.draw(s)
	if dir != "" {
		writePNG(t, dir, name+".png", img)
	}
	return img, r
}

// With SHOW_PREVIEW set, an add-on's page (Meals cooking, every block, a list), the drawer's Add-ons tab,
// the card over the clock by day and over the night clock, and a ring over a page that is up, are written
// there on the Show 5 and the Show 8, the page in the light Paper theme too.
func TestAddonPreviews(t *testing.T) {
	dir := os.Getenv("SHOW_PREVIEW")
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	keepPalette(t)
	h := sampleHost(t, cookingPage())
	useHost(t, h)
	for _, panel := range showPanels()[:2] {
		frame := func(name string, s scene) (*image.RGBA, *renderer) {
			s.now, s.phase = addonNow, "idle"
			if s.timers == nil {
				s.timers = sampleTimers()
			}
			return addonFrame(t, dir, name+"-"+panel.name, panel.wide, panel.high, s)
		}
		h.set("meals", addonapi.OnPage, cookingPage(), addonNow)
		frame("addon-page", scene{addonPage: addonPageOf(h, "meals")})
		h.set("meals", addonapi.OnPage, blocksPage(), addonNow)
		frame("addon-blocks", scene{addonPage: addonPageOf(h, "meals")})
		h.set("meals", addonapi.OnPage, cardsPage(), addonNow)
		frame("addon-cards", scene{addonPage: addonPageOf(h, "meals")})
		h.set("meals", addonapi.OnPage, listPage(), addonNow)
		frame("addon-list", scene{addonPage: addonPageOf(h, "meals")})
		v := addonPageOf(h, "meals")
		v.scrolls[0] = 10000 // as far as it goes: drawn at its end
		_, r := frame("addon-list-scrolled", scene{addonPage: v})
		r.zmu.Lock()
		lay := r.addon.pageAt.lay
		r.zmu.Unlock()
		if lay == nil || lay.maxScroll(0) <= 0 {
			t.Errorf("%s: the list does not scroll", panel.name)
		}
		frame("addon-waiting", scene{addonPage: &addonPageView{id: "plex", name: "Plex Remote"}})
		frame("addon-tab", scene{showDrawer: true, drawerTab: drawerAddons, weather: hassSunny()})
		frame("addon-card", scene{weather: hassSunny(), addonCard: addonCardOf(h, "meals")})
		frame("addon-card-night", scene{redClock: true, nightDim: true, addonCard: addonCardOf(h, "meals")})

		// Paper, a light theme.
		applyTheme(themes[themeIndex("Paper")])
		h.set("meals", addonapi.OnPage, cookingPage(), addonNow)
		frame("addon-page-Paper", scene{addonPage: addonPageOf(h, "meals")})
		frame("addon-card-Paper", scene{weather: hassSunny(), addonCard: addonCardOf(h, "meals")})
		applyTheme(themes[themeIndex(palette.Presets[0].Name)])
	}
}

// A page that is up is not drawn under a screen the device reserves, nor touched there: a ring, the
// settings, the PIN pad, a call; the add-on is told it is out of view, and in view again once it is gone.
func TestAddonPageUnderReservedScreens(t *testing.T) {
	dir := os.Getenv("SHOW_PREVIEW")
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	h := sampleHost(t, cookingPage())
	useHost(t, h)
	d := &Display{poke: make(chan struct{}, 1)}
	d.openAddon("meals", "Meals")

	frame := func(s scene) scene {
		s.now, s.phase = addonNow, cmpOr(s.phase, "idle")
		d.addonFrameBegin()
		d.addonScene(&s, addonNow)
		d.addonSettle()
		return s
	}
	if s := frame(scene{}); s.addonPage == nil || !h.said("shown meals page on") {
		t.Fatalf("the page with nothing over it: %+v %v", s.addonPage, h.calls)
	}
	ring := ringState{timer: "Rice"}
	for what, s := range map[string]scene{
		"a ring":     {ring: ring},
		"a turn":     {phase: "listening"},
		"the sheet":  {showSheet: true},
		"the PIN":    {pin: pinView{open: true}},
		"a question": {setupAsking: true},
		"a camera":   {showCamera: true},
		"Cast":       {showCast: true},
	} {
		h.calls = nil
		if got := frame(s); got.addonPage != nil {
			t.Errorf("under %s the page was drawn", what)
		}
		if !h.said("shown meals page off") {
			t.Errorf("under %s the add-on was not told its page went out of view: %v", what, h.calls)
		}
		h.calls = nil
		frame(scene{})
		if !h.said("shown meals page on") {
			t.Errorf("after %s the add-on was not told its page is back: %v", what, h.calls)
		}
	}
	// Drawn under the ring: the ring's page, and nothing of the add-on's to tap.
	s := frame(scene{ring: ring, timers: sampleTimers()})
	img, r := addonFrame(t, dir, "addon-under-ring-show8", show8Wide, show8High, s)
	r.zmu.Lock()
	id := r.addon.pageAt.id
	r.zmu.Unlock()
	if id != "" {
		t.Error("the page under the ring left its parts to tap")
	}
	_ = img
}

// The add-on's page takes every finger: the name bar's way out leaves, a tap on a button goes to the
// add-on, a swipe in from the left is back, another swipe the add-on's, and a swipe up scrolls its list.
func TestAddonPageFingers(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	h := sampleHost(t, listPage())
	useHost(t, h)
	img := image.NewRGBA(image.Rect(0, 0, showWide, showHigh))
	d := &Display{on: true, poke: make(chan struct{}, 1), r: newRenderer(img)}
	d.openAddon("meals", "Meals")
	draw := func() {
		s := scene{now: addonNow, phase: "idle"}
		d.addonScene(&s, addonNow)
		s.addonPage.pics = picturesNow
		d.r.draw(s)
	}
	draw()
	if !d.addonPageShowing() {
		t.Fatal("the page was not drawn")
	}
	d.r.zmu.Lock()
	lay := d.r.addon.pageAt.lay
	closeAt := d.r.addon.pageAt.close
	d.r.zmu.Unlock()
	var soup image.Rectangle
	for i, b := range lay.boxes {
		if b.tap == "stew" {
			_, soup = lay.at(i, nil, image.Point{})
		}
	}
	d.gesture(touch.Gesture{Kind: touch.Tap, X: soup.Min.X + 200, Y: soup.Min.Y + soup.Dy()/2})
	if !h.said("touch meals page stew") {
		t.Errorf("a tap on a list item: %v", h.calls)
	}
	d.gesture(touch.Gesture{Kind: touch.SwipeUp, X: soup.Min.X + 200, Y: soup.Min.Y})
	d.mu.Lock()
	scrolled := d.addon.scrolls[0]
	d.mu.Unlock()
	if scrolled <= 0 {
		t.Error("a swipe up did not scroll the list")
	}
	d.gesture(touch.Gesture{Kind: touch.SwipeRight, X: 20, Y: 300})
	d.gesture(touch.Gesture{Kind: touch.SwipeLeft, X: 600, Y: 300})
	if !h.said("back meals") || !h.said("swipe meals left") {
		t.Errorf("back and a swipe: %v", h.calls)
	}
	d.gesture(touch.Gesture{Kind: touch.Tap, X: closeAt.Min.X + 5, Y: closeAt.Min.Y + 5})
	d.mu.Lock()
	up := d.addon.page
	d.mu.Unlock()
	if up != "" {
		t.Error("the name bar's way out left the page up")
	}
}

// An add-on that opens its page at night lifts the night light, as a long press does: the server allows
// it only in answer to somebody. Closed by the add-on, the page goes.
func TestAddonOpenAtNight(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	h := sampleHost(t, cookingPage())
	useHost(t, h)
	d := &Display{on: true, ceiling: 60, nightGlow: true, poke: make(chan struct{}, 1)}
	addonSink{d}.Open("meals")
	for end := time.Now().Add(2 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		d.mu.Lock()
		glow, page, name := d.nightGlow, d.addon.page, d.addon.name
		d.mu.Unlock()
		if !glow {
			if page != "meals" || name != "Meals" {
				t.Errorf("the page up is %q %q", page, name)
			}
			break
		}
		if time.Now().After(end) {
			t.Fatal("the night light stayed")
		}
	}
	addonSink{d}.Close("meals")
	if d.addon.page != "" {
		t.Error("screen.close left the page up")
	}
}

// The drawer's Add-ons tab is there while an add-on with a page runs, with a row for each; a tap on one
// opens its page and counts as somebody asking.
func TestAddonTab(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	useHost(t, &fakeHost{})
	for _, id := range shownTabIDs() {
		if id == drawerAddons {
			t.Fatal("an Add-ons tab with no add-on")
		}
	}
	h := sampleHost(t, cookingPage())
	useHost(t, h)
	found := false
	for _, id := range shownTabIDs() {
		found = found || id == drawerAddons
	}
	rows, _ := addonRows()
	if !found || len(rows) != 2 || rows[0].label != "Meals" || !strings.HasPrefix(rows[0].id, "addon:") {
		t.Fatalf("the tab %v, its rows %+v", found, rows)
	}
	d := &Display{poke: make(chan struct{}, 1)}
	d.openDrawer(drawerAddons)
	d.drawerRowTap(rows[0].id)
	if !h.said("asked meals") || d.addon.page != "meals" || d.drawer {
		t.Errorf("a tap on the tile: %v, page %q, drawer %v", h.calls, d.addon.page, d.drawer)
	}
}

// The card on the clock: a tap on its chip goes to the add-on, anywhere else on it opens the page, and a
// swipe across it puts it away. Over the night clock it takes no finger.
func TestAddonCardFingers(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	h := sampleHost(t, cookingPage())
	useHost(t, h)
	img := image.NewRGBA(image.Rect(0, 0, show8Wide, show8High))
	d := &Display{on: true, poke: make(chan struct{}, 1), r: newRenderer(img)}
	s := scene{now: addonNow, phase: "idle", timers: sampleTimers()}
	d.addonCardScene(&s, true)
	if s.addonCard == nil {
		t.Fatal("no card on the clock")
	}
	s.addonCard.pics = picturesNow
	d.r.draw(s)
	d.r.zmu.Lock()
	at := d.r.addon.cardAt
	d.r.zmu.Unlock()
	var chip image.Rectangle
	for i, b := range at.lay.boxes {
		if b.tap == "next" {
			_, chip = at.lay.at(i, nil, at.off)
		}
	}
	if chip.Empty() || !chip.In(at.box) || at.box.Dy() > show8High/3 {
		t.Fatalf("the card %v (a third is %d) and its chip %v", at.box, show8High/3, chip)
	}
	if !d.addonCardGesture(touch.Gesture{Kind: touch.Tap, X: chip.Min.X + 4, Y: chip.Min.Y + 4}) || !h.said("touch meals card next") {
		t.Errorf("a tap on the card's chip: %v", h.calls)
	}
	d.addonCardGesture(touch.Gesture{Kind: touch.Tap, X: at.box.Min.X + 10, Y: at.box.Min.Y + 10})
	if !h.said("asked meals") || d.addon.page != "meals" {
		t.Errorf("a tap on the card: %v, page %q", h.calls, d.addon.page)
	}
	d.addonCardGesture(touch.Gesture{Kind: touch.SwipeLeft, X: at.box.Min.X + 40, Y: at.box.Min.Y + 10})
	if !h.said("dismiss meals") {
		t.Errorf("a swipe across the card: %v", h.calls)
	}

	// At night: drawn in the night's tone, and no finger is its.
	s = scene{now: addonNow, phase: "idle", redClock: true, nightDim: true}
	d.addonCardScene(&s, true)
	if s.addonCard == nil {
		t.Fatal("no card on the night clock")
	}
	s.addonCard.pics = picturesNow
	d.r.draw(s)
	if d.addonCardGesture(touch.Gesture{Kind: touch.Tap, X: at.box.Min.X + 10, Y: at.box.Max.Y - 10}) {
		t.Error("the card over the night clock took a tap")
	}
	// Nothing brighter than the night clock's red.
	for y := 0; y < show8High; y += 3 {
		for x := 0; x < show8Wide; x += 3 {
			c := img.RGBAAt(x, y)
			if c.G > nightRed.R || c.B > nightRed.R || c.R > 255 {
				t.Fatalf("a pixel at %d,%d is %v, brighter or bluer than the night's red", x, y, c)
			}
		}
	}
}

// A page whose add-on stops goes with it; of several add-ons' cards the newest shows, and the next comes
// back when it goes.
func TestAddonGoesWithItsAddon(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	h := sampleHost(t, cookingPage())
	useHost(t, h)
	h.set("plex", addonapi.OnCard, addonapi.Text("Now playing: a film"), addonNow.Add(time.Minute))
	s := scene{now: addonNow, phase: "idle"}
	d := &Display{poke: make(chan struct{}, 1)}
	d.addonCardScene(&s, true)
	if s.addonCard == nil || s.addonCard.id != "plex" {
		t.Fatalf("the newest card: %+v", s.addonCard)
	}
	h.mu.Lock()
	delete(h.scenes, "plex/card")
	h.mu.Unlock()
	s = scene{now: addonNow, phase: "idle"}
	d.addonCardScene(&s, true)
	if s.addonCard == nil || s.addonCard.id != "meals" {
		t.Fatalf("the card behind it: %+v", s.addonCard)
	}

	d.openAddon("meals", "Meals")
	h.mu.Lock()
	h.tiles = h.tiles[1:] // Meals stopped
	h.mu.Unlock()
	forgetTiles()
	s = scene{now: addonNow, phase: "idle"}
	d.addonScene(&s, addonNow)
	if s.addonPage != nil || d.addon.page != "" {
		t.Error("the page of an add-on that stopped stayed up")
	}
}
