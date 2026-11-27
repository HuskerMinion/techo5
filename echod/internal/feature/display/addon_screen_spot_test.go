//go:build spot

package display

import (
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/addonhost"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
	"github.com/HuskerMinion/techo5/echod/pkg/addonapi"
)

var spotAddonNow = time.Date(2026, 11, 27, 18, 7, 0, 0, time.Local)

// spotCooking is the cooking page as an add-on sends it for a round face: a step, its timer and the way
// on, without the photo the Show has room for.
func spotCooking() addonapi.Block {
	tabs := addonapi.Row(addonapi.Chip("shopping", "", "mdi:cart"), addonapi.Chip("plan", "", "mdi:calendar"),
		addonapi.Block{Type: addonapi.BlockChip, ID: "cooking", Label: "Cooking", Selected: true})
	tabs.Spacing = "small"
	return addonapi.Column(tabs,
		addonapi.Block{Type: addonapi.BlockText, Text: "Step 2 of 5", Size: addonapi.SizeSmall},
		addonapi.Block{Type: addonapi.BlockText, Text: "Stir the onions until they are soft and golden.", Size: addonapi.SizeTitle, MaxLines: 3, Weight: 1},
		addonapi.Block{Type: addonapi.BlockProgress, Timer: "local:onions"},
		addonapi.Row(addonapi.Button("back", "", "mdi:arrow-left"), addonapi.Block{Type: addonapi.BlockSpacer, Weight: 1}, addonapi.Button("next", "Next", "mdi:arrow-right")))
}

// spotCard is a card as an add-on sends it for a round face: the step, its timer and the way on.
func spotCard() addonapi.Block {
	bar := addonapi.Block{Type: addonapi.BlockProgress, Timer: "local:onions", Weight: 1}
	return addonapi.Column(addonapi.Block{Type: addonapi.BlockText, Text: "Stir the onions", Size: addonapi.SizeTitle, MaxLines: 1},
		addonapi.Row(bar, addonapi.Chip("next", "", "mdi:arrow-right")))
}

func spotFrame(t *testing.T, dir, name string, s roundScene) (*image.RGBA, *roundRenderer) {
	t.Helper()
	s.now = spotAddonNow
	if s.phase == "" {
		s.phase = "idle"
	}
	if s.timers == nil {
		s.timers = sampleTimers()
	}
	img := image.NewRGBA(image.Rect(0, 0, side, side))
	r := newRoundRenderer(img)
	r.draw(s)
	if dir != "" {
		writePNG(t, filepath.Join(dir, name+".png"), img)
	}
	return img, r
}

// With SPOT_PREVIEW set, an add-on's page (cooking, every block, a list), the ring menu with its Add-ons
// item and the dial of add-ons, the card over the clock by day and over the night clock, a ring over a
// page that is up, and the dial, the page and the card with an icon from the catalog, are written there.
func TestSpotAddonPreviews(t *testing.T) {
	dir := os.Getenv("SPOT_PREVIEW")
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	h := sampleHost(t, spotCooking())
	useHost(t, h)
	spotFrame(t, dir, "addon-page", roundScene{addonPage: addonPageOf(h, "meals")})
	h.set("meals", addonapi.OnPage, blocksPage(), spotAddonNow)
	spotFrame(t, dir, "addon-blocks", roundScene{addonPage: addonPageOf(h, "meals")})
	h.set("meals", addonapi.OnPage, cardsPage(), spotAddonNow)
	spotFrame(t, dir, "addon-cards", roundScene{addonPage: addonPageOf(h, "meals")})
	// The controls, for the round face: the servings, a toggle and the rows with a picture or an icon.
	controls := controlsPage()
	controls.Children = []addonapi.Block{controls.Children[1], controls.Children[2], controls.Children[4]}
	h.set("meals", addonapi.OnPage, controls, spotAddonNow)
	spotFrame(t, dir, "addon-controls", roundScene{addonPage: addonPageOf(h, "meals")})
	h.set("meals", addonapi.OnPage, overflowPage(), spotAddonNow)
	spotFrame(t, dir, "addon-overflow", roundScene{addonPage: addonPageOf(h, "meals"), timers: sampleTimers()})
	over := addonPageOf(h, "meals")
	over.scrolls[0] = 10000
	spotFrame(t, dir, "addon-overflow-scrolled", roundScene{addonPage: over, timers: sampleTimers()})
	h.set("meals", addonapi.OnCard, crowdedCard(), spotAddonNow)
	spotFrame(t, dir, "addon-card-crowded", roundScene{addonCard: addonCardOf(h, "meals"), timers: sampleTimers()})
	h.set("meals", addonapi.OnCard, cardScene(), spotAddonNow)
	h.set("meals", addonapi.OnPage, listPage(), spotAddonNow)
	_, r := spotFrame(t, dir, "addon-list", roundScene{addonPage: addonPageOf(h, "meals")})
	r.zmu.Lock()
	lay := r.addon.pageAt.lay
	r.zmu.Unlock()
	if lay == nil || lay.maxScroll(0) <= 0 {
		t.Error("the list does not scroll on the round face")
	}
	items := itemsFor(modeMain)
	sel := indexOf(items, itemAddons)
	if sel < 0 {
		t.Fatal("no Add-ons item on the ring menu")
	}
	spotFrame(t, dir, "addon-menu", roundScene{menuOpen: true, menuMode: modeMain, menuSel: sel, menuRot: restFor(sel, len(items))})
	spotFrame(t, dir, "addon-dial", roundScene{menuOpen: true, menuMode: modeAddons, menuSel: 1, menuRot: restFor(1, 2)})
	h.set("meals", addonapi.OnCard, spotCard(), spotAddonNow)
	spotFrame(t, dir, "addon-card", roundScene{addonCard: addonCardOf(h, "meals")})
	spotFrame(t, dir, "addon-card-night", roundScene{redClock: true, addonCard: addonCardOf(h, "meals")})

	// The icon a catalog reviewer chose for Meals, on the dial, its page's name bar and its card.
	withIcon(h, "mdi:chef-hat")
	spotFrame(t, dir, "addon-icon-dial", roundScene{menuOpen: true, menuMode: modeAddons, menuSel: 0, menuRot: restFor(0, 2)})
	h.set("meals", addonapi.OnPage, spotCooking(), spotAddonNow)
	spotFrame(t, dir, "addon-icon-page", roundScene{addonPage: addonPageOf(h, "meals")})
	spotFrame(t, dir, "addon-icon-card", roundScene{addonCard: addonCardOf(h, "meals")})
	withIcon(h, "")
}

// A page that is up is not drawn under the faces the device reserves, and the add-on is told it is out
// of view; a ring is drawn in its place.
func TestSpotAddonPageUnderReservedFaces(t *testing.T) {
	dir := os.Getenv("SPOT_PREVIEW")
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	h := sampleHost(t, spotCooking())
	useHost(t, h)
	d := &Display{poke: make(chan struct{}, 1)}
	d.openAddon("meals", "Meals")
	frame := func(s roundScene) roundScene {
		s.now = spotAddonNow
		if s.phase == "" {
			s.phase = "idle"
		}
		d.addonFrameBegin()
		d.addonSceneSpot(&s)
		d.addonSettle()
		return s
	}
	if s := frame(roundScene{}); s.addonPage == nil || !h.said("shown meals page on") {
		t.Fatalf("the page with nothing over it: %v", h.calls)
	}
	ring := ringing{timer: "Rice", timerOn: true}
	for what, s := range map[string]roundScene{
		"a ring":      {ringing: ring},
		"a turn":      {phase: "replying"},
		"the sheet":   {sheetOpen: true},
		"the PIN":     {pin: pinView{open: true}},
		"a question":  {setupAsking: true},
		"a camera":    {showCamera: true},
		"the menu":    {menuOpen: true},
		"first run":   {firstRunUp: true},
		"a reminder":  {showReminder: true},
		"a video ask": {showVideoAsk: true},
	} {
		h.calls = nil
		if got := frame(s); got.addonPage != nil {
			t.Errorf("under %s the page was drawn", what)
		}
		if !h.said("shown meals page off") {
			t.Errorf("under %s the add-on was not told: %v", what, h.calls)
		}
		frame(roundScene{})
	}
	_, r := spotFrame(t, dir, "addon-under-ring", frame(roundScene{ringing: ring}))
	r.zmu.Lock()
	defer r.zmu.Unlock()
	if r.addon.pageAt.id != "" {
		t.Error("the page under the ring left its parts to tap")
	}
}

// On the round face the page takes every finger but a hold: its way out at the foot leaves, a tap on a
// button goes to the add-on, and a swipe in from the left is back.
func TestSpotAddonPageFingers(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	h := sampleHost(t, spotCooking())
	useHost(t, h)
	img := image.NewRGBA(image.Rect(0, 0, side, side))
	d := &Display{on: true, poke: make(chan struct{}, 1), r: newRoundRenderer(img)}
	d.actAddons(itemAddons) // two add-ons: the dial of them
	if !d.menuOpen || d.menuMode != modeAddons {
		t.Fatalf("the Add-ons item with two add-ons: menu %v mode %v", d.menuOpen, d.menuMode)
	}
	// The dial's items are the add-ons by id: one starting between the dial being drawn and the choice
	// moves the others along the list, and the choice still opens the add-on chosen.
	items := addonItems()
	if len(items) != 2 || items[0].id != addonItem("meals") || items[1].id != addonItem("plex") {
		t.Fatalf("the dial %v", items)
	}
	h.mu.Lock()
	h.tiles = append([]addonhost.ScreenTile{{ID: "abacus", Name: "Abacus"}}, h.tiles...)
	h.mu.Unlock()
	forgetTiles()
	d.actAddons(items[0].id)
	if d.addon.page != "meals" || d.menuOpen || !h.said("asked meals") {
		t.Fatalf("an add-on chosen on the dial: page %q, menu %v, %v", d.addon.page, d.menuOpen, h.calls)
	}
	s := roundScene{now: spotAddonNow, phase: "idle", timers: sampleTimers()}
	d.addonSceneSpot(&s)
	s.addonPage.pics = picturesNow
	d.r.draw(s)
	var next image.Rectangle
	_, closeAt, content := d.r.addonPageRooms()
	d.r.zmu.Lock()
	lay := d.r.addon.pageAt.lay
	d.r.zmu.Unlock()
	for i, b := range lay.boxes {
		if b.tap == "next" {
			_, next = lay.at(i, nil, image.Point{})
		}
	}
	if next.Empty() || !next.In(content) {
		t.Fatalf("the Next button at %v, outside %v", next, content)
	}
	d.gesture(touch.Gesture{Kind: touch.Tap, X: next.Min.X + 6, Y: next.Min.Y + 6})
	d.gesture(touch.Gesture{Kind: touch.SwipeRight, X: 40, Y: 240})
	if !h.said("touch meals page next") || !h.said("back meals") {
		t.Errorf("a tap and back: %v", h.calls)
	}
	d.gesture(touch.Gesture{Kind: touch.Tap, X: center, Y: closeAt.Min.Y + 8})
	if d.addon.page != "" || !h.said("left meals") {
		t.Errorf("the way out left the page up, or did not say so: %v", h.calls)
	}
}

// The volume turned over the page leaves the page in view under it, dimmed; a call takes it, whatever
// the call's phase.
func TestSpotAddonPageUnderTheVolumeAndACall(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	h := sampleHost(t, spotCooking())
	useHost(t, h)
	img := image.NewRGBA(image.Rect(0, 0, side, side))
	d := &Display{on: true, poke: make(chan struct{}, 1), r: newRoundRenderer(img)}
	d.openAddon("meals", "Meals")
	s := roundScene{now: spotAddonNow, phase: "idle", showVolume: true, volume: 5, maxVolume: 10}
	d.addonSceneSpot(&s)
	if s.addonPage == nil {
		t.Fatal("the volume took the page out of view")
	}
	s.addonPage.pics = picturesNow
	d.r.draw(s)
	if !d.addonPageShowing() {
		t.Error("the page under the volume takes no finger")
	}
	s = roundScene{now: spotAddonNow, phase: "idle"}
	s.call.Phase = phone.Dialing
	d.addonSceneSpot(&s)
	if s.addonPage != nil {
		t.Error("the page was drawn over a call")
	}
}

// With SPOT_PREVIEW set, an add-on's card over the clock face by day in every clock style, at the center
// and at the foot: the classic face moves up clear of it, every other face is drawn whole above it.
func TestSpotAddonCardOverEveryStyle(t *testing.T) {
	dir := os.Getenv("SPOT_PREVIEW")
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	h := sampleHost(t, spotCooking())
	useHost(t, h)
	at := spotAddonNow
	var week []hass.Day
	for i, c := range []string{"partlycloudy", "rainy", "sunny", "cloudy", "snowy"} {
		week = append(week, hass.Day{When: at.AddDate(0, 0, i), Condition: c, High: float64(72 - 3*i), Low: float64(51 - 2*i)})
	}
	facts := styleFacts{
		rise: time.Date(2026, 11, 27, 7, 28, 0, 0, time.Local), set: time.Date(2026, 11, 27, 16, 55, 0, 0, time.Local), sunOK: true,
		days: week, places: worldPlaces(nil),
		next: []hass.Event{{Summary: "Dentist", Start: at.Add(83 * time.Minute), End: at.Add(143 * time.Minute)}},
	}
	t.Cleanup(func() {
		_ = config.Set().Screen().ClockStyle(styleClassic)
		_ = config.Set().Screen().ClockPosition("")
	})
	for _, pos := range []string{"", "bottom"} {
		for _, st := range clockStyles {
			if pos != "" && !footStyle(st.value) {
				continue
			}
			if err := config.Set().Screen().ClockStyle(st.value); err != nil {
				t.Fatal(err)
			}
			if err := config.Set().Screen().ClockPosition(pos); err != nil {
				t.Fatal(err)
			}
			name := "addon-card-style-" + strings.ToLower(st.label)
			if pos != "" {
				name += "-" + pos
			}
			sc := roundScene{style: facts, weather: home.Weather{Condition: "sunny", Temp: "68°"}, addonCard: addonCardOf(h, "meals")}
			spotFrame(t, dir, name, sc)
			// Nothing of the face where the card goes: drawn without the card, the card's room (inside the
			// rim) is the face's ground.
			addonCardUnpainted = true
			img, r := spotFrame(t, "", "", sc)
			addonCardUnpainted = false
			box := spotAddonCard
			box.Min.Y = r.addonCardTop(sc)
			if box.Min.Y <= 0 {
				t.Fatal("no card")
			}
			if st.value == styleGlow {
				continue // its ground is its glow
			}
			for y := box.Min.Y; y < box.Max.Y; y++ {
				for x := box.Min.X; x < box.Max.X; x++ {
					if dx, dy := x-center, y-center; dx*dx+dy*dy > 200*200 {
						continue // the rim, the device's own, round the face's edge
					}
					if c := img.RGBAAt(x, y); c != colBackground {
						t.Fatalf("%s at %q: the face draws %v at %d,%d, under the card %v", st.label, pos, c, x, y, box)
					}
				}
			}
		}
	}
}
