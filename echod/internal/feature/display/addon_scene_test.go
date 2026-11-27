//go:build !dot

package display

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/addonhost"
	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
	"github.com/HuskerMinion/techo5/echod/pkg/addonapi"
)

// fakeHost is the add-on host as the screen sees it, with add-ons and scenes a test gives it, and what the
// screen told it.
type fakeHost struct {
	mu     sync.Mutex
	tiles  []addonhost.ScreenTile
	scenes map[string]hostScene // by id and where: "meals/page"
	images map[string][]byte    // by name
	calls  []string
	revs   []uint64 // the revisions taps were sent with
}

func (h *fakeHost) note(s string) { h.mu.Lock(); h.calls = append(h.calls, s); h.mu.Unlock() }

func (h *fakeHost) ScreenTiles() []addonhost.ScreenTile {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]addonhost.ScreenTile(nil), h.tiles...)
}

func (h *fakeHost) Scene(id, on string) (addonhost.Scene, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	sc, ok := h.scenes[id+"/"+on]
	return sc, ok
}

func (h *fakeHost) set(id, on string, root addonapi.Block, at time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.scenes == nil {
		h.scenes = map[string]hostScene{}
	}
	rev := h.scenes[id+"/"+on].Rev + 1
	h.scenes[id+"/"+on] = hostScene{Root: root, Rev: rev, At: at, Set: rev}
}

// patch is the add-on patching its scene: a new revision of the same scene.set.
func (h *fakeHost) patch(id, on string, root addonapi.Block, at time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	cur := h.scenes[id+"/"+on]
	h.scenes[id+"/"+on] = hostScene{Root: root, Rev: cur.Rev + 1, At: at, Set: cur.Set}
}

func (h *fakeHost) Shown(id, on string, shown bool) {
	h.note("shown " + id + " " + on + " " + map[bool]string{true: "on", false: "off"}[shown])
}

func (h *fakeHost) Touch(id, on, block string, rev uint64) bool {
	h.note("touch " + id + " " + on + " " + block)
	h.mu.Lock()
	h.revs = append(h.revs, rev)
	h.mu.Unlock()
	return true
}
func (h *fakeHost) Left(id string) { h.note("left " + id) }
func (h *fakeHost) Change(id, on, block string, rev uint64, value any) bool {
	h.note(fmt.Sprintf("change %s %s %s %v", id, on, block, value))
	h.mu.Lock()
	h.revs = append(h.revs, rev)
	h.mu.Unlock()
	return true
}
func (h *fakeHost) Swipe(id, direction string) bool {
	h.note("swipe " + id + " " + direction)
	return true
}
func (h *fakeHost) Back(id string) bool   { h.note("back " + id); return true }
func (h *fakeHost) DismissCard(id string) { h.note("dismiss " + id) }
func (h *fakeHost) Asked(id string)       { h.note("asked " + id) }

func (h *fakeHost) Image(id string, b *addonapi.Block) ([]byte, string, bool) {
	if len(b.Data) > 0 {
		return b.Data, id + "/d", true
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	data, ok := h.images[b.Name]
	return data, id + "/n:" + b.Name, ok
}

func (h *fakeHost) said(s string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.calls {
		if c == s {
			return true
		}
	}
	return false
}

// useHost has the screen see h as the add-on host for the test.
func useHost(t *testing.T, h *fakeHost) {
	t.Helper()
	was := addonHost
	addonHost = func() screenHost { return h }
	forgetTiles()
	t.Cleanup(func() { addonHost = was; forgetTiles() })
}

// photo is a made-up photo, w by h: a warm sky over a dark field, with a sun.
func photo(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			c := color.RGBA{uint8(200 - 80*y/h), uint8(120 + 60*x/w), uint8(60 + 100*y/h), 255}
			if y > h*2/3 {
				c = color.RGBA{40, uint8(70 + 30*x/w), 40, 255}
			}
			dx, dy := x-w*3/4, y-h/3
			if dx*dx+dy*dy < (h/8)*(h/8) {
				c = color.RGBA{250, 220, 120, 255}
			}
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// Sample scenes, as an add-on like Meals would send them.

func cookingPage() addonapi.Block {
	tabs := addonapi.Row(addonapi.Chip("shopping", "Shopping", "mdi:cart"), addonapi.Chip("plan", "Plan", "mdi:calendar"),
		addonapi.Block{Type: addonapi.BlockChip, ID: "cooking", Label: "Cooking", Icon: "mdi:pot-steam", Selected: true})
	tabs.Spacing = "small"
	pic := addonapi.ImageNamed("dish")
	pic.Fit, pic.Weight = "cover", 1
	steps := addonapi.Column(
		addonapi.Block{Type: addonapi.BlockText, Text: "Step 2 of 5", Size: addonapi.SizeSmall},
		addonapi.Block{Type: addonapi.BlockText, ID: "step", Text: "Stir the onions until they are soft and golden, about eight minutes.", Size: addonapi.SizeTitle, MaxLines: 3},
		addonapi.Block{Type: addonapi.BlockProgress, ID: "timer", Timer: "local:onions"},
	)
	steps.Weight = 2
	body := addonapi.Row(pic, steps)
	body.Weight, body.Spacing = 1, "large"
	buttons := addonapi.Row(addonapi.Button("back", "Back", "mdi:arrow-left"), addonapi.Block{Type: addonapi.BlockSpacer, Weight: 1},
		addonapi.Button("timer_start", "", "mdi:timer-outline"), addonapi.Button("next", "Next step", "mdi:arrow-right"))
	return addonapi.Column(tabs, body, buttons)
}

func blocksPage() addonapi.Block {
	half := 0.6
	return addonapi.Column(
		addonapi.Row(
			addonapi.Block{Type: addonapi.BlockText, Text: "42°", Size: addonapi.SizeHuge, Bold: true},
			addonapi.Column(
				addonapi.Block{Type: addonapi.BlockText, Text: "A title, bold", Size: addonapi.SizeTitle, Bold: true},
				addonapi.Text("Body words wrap.\nLine breaks stay."),
			),
		),
		addonapi.Row(addonapi.Button("play", "Play", "mdi:play"), addonapi.Button("pause", "", "mdi:pause")),
		addonapi.Row(addonapi.Chip("chip", "A chip", ""), addonapi.Block{Type: addonapi.BlockChip, ID: "picked", Label: "Picked", Icon: "mdi:check", Selected: true}),
		addonapi.Block{Type: addonapi.BlockProgress, Value: &half},
		addonapi.Divider(),
		addonapi.Block{Type: addonapi.BlockText, Text: "Small words, centered", Size: addonapi.SizeSmall, Align: "center"},
	)
}

func cardsPage() addonapi.Block {
	cards := addonapi.Row(
		addonapi.Card("accent", addonapi.Block{Type: addonapi.BlockText, Text: "Accent", Bold: true}, addonapi.Text("The theme's accent")),
		addonapi.Card("dim", addonapi.Block{Type: addonapi.BlockText, Text: "Dim", Bold: true}, addonapi.Text("And its dim")),
		addonapi.Card("", addonapi.Block{Type: addonapi.BlockText, Text: "Plain", Bold: true}, addonapi.Text("No mark")),
	)
	cards.Spacing = "small"
	return addonapi.Column(cards,
		addonapi.Row(addonapi.ImageNamed("thumb"), addonapi.Spacer(),
			addonapi.Block{Type: addonapi.BlockText, Text: "An image put by name, and one still to come:", Size: addonapi.SizeSmall},
			addonapi.Image("http://192.168.1.20/missing.png")),
	)
}

func listPage() addonapi.Block {
	item := func(id, name, sub string) addonapi.Block {
		thumb := addonapi.ImageNamed("thumb")
		return addonapi.Block{Type: addonapi.BlockRow, ID: id, Children: []addonapi.Block{thumb,
			addonapi.Column(addonapi.Block{Type: addonapi.BlockText, Text: name, Bold: true}, addonapi.Block{Type: addonapi.BlockText, Text: sub, Size: addonapi.SizeSmall})}}
	}
	fav := item("soup", "Tomato soup", "30 min · 4 servings")
	fav.Children = append(fav.Children, addonapi.Block{Type: addonapi.BlockSpacer, Weight: 1}, addonapi.Chip("soup_fav", "", "mdi:star"))
	return addonapi.Column(
		addonapi.Block{Type: addonapi.BlockText, Text: "Recipes", Size: addonapi.SizeTitle, Bold: true},
		addonapi.List(
			addonapi.Section{Heading: "Soups", Items: []addonapi.Block{fav, item("stew", "Lentil stew", "45 min"), item("pho", "Pho", "2 h")}},
			addonapi.Section{Heading: "Bread", Items: []addonapi.Block{item("rye", "Rye loaf", "3 h"), item("flat", "Flatbread", "25 min"), addonapi.Text("An item with no id is not tapped.")}},
		),
	)
}

func cardScene() addonapi.Block {
	return addonapi.Row(addonapi.ImageNamed("thumb"),
		addonapi.Column(addonapi.Block{Type: addonapi.BlockText, Text: "Stir the onions", Size: addonapi.SizeTitle, MaxLines: 1},
			addonapi.Block{Type: addonapi.BlockProgress, Timer: "local:onions"}),
		addonapi.Chip("next", "Next", "mdi:arrow-right"))
}

// controlsPage is the controls an add-on takes a choice or a number with: a row of tabs, one selected (the
// add-on's to set), a stepper for the servings, toggles, and a list whose rows have a picture or an icon
// at their start.
func controlsPage() addonapi.Block {
	tabs := addonapi.Row(addonapi.Block{Type: addonapi.BlockChip, ID: "tab_week", Label: "This week", Selected: true},
		addonapi.Chip("tab_next", "Next week", ""))
	tabs.Spacing = "small"
	rows := addonapi.List(addonapi.Section{Heading: "Tonight", Items: []addonapi.Block{
		{Type: addonapi.BlockRow, ID: "soup", Name: "thumb", Children: []addonapi.Block{
			addonapi.Block{Type: addonapi.BlockText, Text: "Tomato soup", Bold: true}}},
		{Type: addonapi.BlockRow, ID: "oven", Icon: "mdi:stove", Children: []addonapi.Block{addonapi.Text("Oven at 200°")}},
	}})
	return addonapi.Column(tabs,
		addonapi.Stepper("servings", "Servings", 4, 1, 12, 1, "people"),
		addonapi.Toggle("warm", "Keep warm", true),
		addonapi.Toggle("alarm", "Ring when done", false),
		rows)
}

// sampleHost is a host with Meals running, its page and card set, and its images put.
func sampleHost(t *testing.T, page addonapi.Block) *fakeHost {
	t.Helper()
	h := &fakeHost{tiles: []addonhost.ScreenTile{{ID: "meals", Name: "Meals"}, {ID: "plex", Name: "Plex Remote"}},
		images: map[string][]byte{"dish": photo(t, 640, 480), "thumb": photo(t, 96, 96)}}
	at := time.Date(2026, 11, 27, 18, 5, 0, 0, time.Local)
	h.set("meals", addonapi.OnPage, page, at)
	h.set("meals", addonapi.OnCard, cardScene(), at)
	return h
}

// sampleTimers is the onions' timer, 4:12 of 8 minutes left.
func sampleTimers() []timer.Countdown {
	return []timer.Countdown{{ID: "local:onions", Name: "Meals: onions", Left: 4*time.Minute + 12*time.Second, Total: 8 * time.Minute, Active: true, Owner: "meals"}}
}

// addonPageOf is h's page of id as a frame would draw it, its images decoded at once.
func addonPageOf(h *fakeHost, id string) *addonPageView {
	sc, ok := h.Scene(id, addonapi.OnPage)
	return &addonPageView{id: id, name: addonNameIn(h, id), icon: addonIconIn(h, id), sc: sc, has: ok,
		images: func(b *addonapi.Block) ([]byte, string, bool) { return h.Image(id, b) }, pics: picturesNow}
}

// addonCardOf is h's card of id likewise.
func addonCardOf(h *fakeHost, id string) *addonCardView {
	sc, _ := h.Scene(id, addonapi.OnCard)
	return &addonCardView{id: id, name: addonNameIn(h, id), icon: addonIconIn(h, id), sc: sc,
		images: func(b *addonapi.Block) ([]byte, string, bool) { return h.Image(id, b) }, pics: picturesNow}
}

func addonIconIn(h *fakeHost, id string) string {
	for _, t := range h.ScreenTiles() {
		if t.ID == id {
			return t.Icon
		}
	}
	return ""
}

// withIcon gives h's Meals the icon a catalog reviewer chose for it.
func withIcon(h *fakeHost, icon string) {
	h.mu.Lock()
	h.tiles[0].Icon = icon
	h.mu.Unlock()
	forgetTiles()
}

func addonNameIn(h *fakeHost, id string) string {
	for _, t := range h.ScreenTiles() {
		if t.ID == id {
			return t.Name
		}
	}
	return id
}

// A layout puts every block inside its room, finds what can be tapped, the innermost first, and
// scrolls a list within its view only.
func TestSceneLayout(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 960, 480))
	p := &paint{dst: img, w: 960, h: 480}
	h := sampleHost(t, listPage())
	room := image.Rect(28, 94, 960-28, 480-18)
	sc, _ := h.Scene("meals", addonapi.OnPage)
	lay := p.layoutScene(&sc.Root, room, false, func(b *addonapi.Block) ([]byte, string, bool) { return h.Image("meals", b) })
	if len(lay.lists) != 1 {
		t.Fatalf("lists: %d", len(lay.lists))
	}
	view := lay.lists[0].view
	if !view.In(room) || view.Dy() <= 0 || lay.maxScroll(0) <= 0 {
		t.Fatalf("the list's view %v in %v, scrolling %d", view, room, lay.maxScroll(0))
	}
	for i, b := range lay.boxes {
		if b.list == 0 && !b.r.In(room) {
			t.Errorf("box %d (%s) at %v is outside the room %v", i, b.b.Type, b.r, room)
		}
	}
	var soup, star image.Rectangle
	for i, b := range lay.boxes {
		switch b.tap {
		case "soup":
			_, soup = lay.at(i, nil, image.Point{})
		case "soup_fav":
			_, star = lay.at(i, nil, image.Point{})
		}
	}
	if soup.Empty() || star.Empty() || !star.In(soup) {
		t.Fatalf("the item %v and its chip %v", soup, star)
	}
	mid := func(r image.Rectangle) image.Point { return r.Min.Add(r.Size().Div(2)) }
	if got := lay.tapAt(mid(star), 0, nil, image.Point{}); got != "soup_fav" {
		t.Errorf("a tap on the chip in an item is %q", got)
	}
	if got := lay.tapAt(image.Pt(soup.Min.X+p.s(150), mid(soup).Y), 0, nil, image.Point{}); got != "soup" {
		t.Errorf("a tap on the item is %q", got)
	}
	// Scrolled past it, the item is not tapped, even where it would be under the view's edge.
	scrolls := []int{lay.maxScroll(0)}
	if got := lay.tapAt(mid(soup), 0, scrolls, image.Point{}); got == "soup" {
		t.Error("an item scrolled out of view was tapped")
	}
	if got := lay.tapAt(image.Pt(mid(soup).X, view.Min.Y-5), 0, scrolls, image.Point{}); got != "" {
		t.Errorf("a tap above the list found %q", got)
	}
}

// A card's words are held to three lines, and a card is no taller than what it holds.
func TestCardLayout(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1280, 800))
	p := &paint{dst: img, w: 1280, h: 800, sNum: 1280, sDen: 960}
	long := addonapi.Block{Type: addonapi.BlockText, Text: "one two three four five six seven eight nine ten eleven twelve thirteen fourteen fifteen sixteen seventeen eighteen nineteen twenty", Size: addonapi.SizeTitle}
	room := image.Rect(60, 500, 400, 800-40)
	lay := p.layoutScene(&long, room, true, nil)
	if n := len(lay.boxes[0].lines); n != addonapi.MaxCardLines {
		t.Errorf("a card's words in %d lines", n)
	}
	if lay.height >= room.Dy() || lay.height <= 0 {
		t.Errorf("a card %d tall in %d", lay.height, room.Dy())
	}
}

// Over the night clock the card shows only what cannot be pressed, since it takes no finger there: its
// chips go, and a row left with nothing but them goes too, while its words, picture and progress stay.
// The add-on's own scene is untouched.
func TestNightCardShowsNothingPressable(t *testing.T) {
	sc := addonapi.Column(cardScene(),
		addonapi.Row(addonapi.Chip("a", "A", ""), addonapi.Block{Type: addonapi.BlockSpacer}, addonapi.Chip("b", "B", "")))
	got := withoutPressable(sc)
	var walk func(b addonapi.Block, depth int)
	words, pics, bars := 0, 0, 0
	walk = func(b addonapi.Block, depth int) {
		switch {
		case addonapi.Pressable(b.Type):
			t.Errorf("a %s %q is left", b.Type, b.ID)
		case b.Type == addonapi.BlockText:
			words++
		case b.Type == addonapi.BlockImage:
			pics++
		case b.Type == addonapi.BlockProgress:
			bars++
		}
		for _, c := range b.Children {
			walk(c, depth+1)
		}
	}
	walk(got, 0)
	if words != 1 || pics != 1 || bars != 1 || len(got.Children) != 1 {
		t.Errorf("left %d words, %d pictures, %d bars, %d rows: %+v", words, pics, bars, len(got.Children), got)
	}
	if b, ok := sc.Find("next"); !ok || b.Type != addonapi.BlockChip {
		t.Error("the add-on's scene lost its chip")
	}

	// Laid out on a card at night: nothing to tap; by day the chip is there.
	img := image.NewRGBA(image.Rect(0, 0, 1280, 800))
	p := &paint{dst: img, w: 1280, h: 800, sNum: 1280, sDen: 960}
	room := image.Rect(60, 540, 1220, 760)
	hs := hostScene{Root: cardScene(), Rev: 1}
	var kept addonLaid
	taps := func(lay *sceneLayout) (n int) {
		for _, b := range lay.boxes {
			if b.tap != "" {
				n++
			}
		}
		return n
	}
	if n := taps(p.laidAt(&kept, "meals", hs, 0, room, true, false, nil)); n != 1 {
		t.Errorf("by day the card has %d to tap", n)
	}
	if n := taps(p.laidAt(&kept, "meals", hs, 0, room, true, true, nil)); n != 0 {
		t.Errorf("at night the card has %d to tap", n)
	}
}

// An add-on's mark is the icon its catalog entry gives it, drawn in the disc, or its initials without
// one; an icon this build has no glyph for, or one not named as the catalog names them, is its initials,
// never an empty disc.
func TestAddonMarkIsTheIconOrTheInitials(t *testing.T) {
	mark := func(icon string) *image.RGBA {
		img := image.NewRGBA(image.Rect(0, 0, 80, 80))
		p := &paint{dst: img, w: 80, h: 80}
		m := p.sceneMetrics()
		p.addonMark("Meals", icon, 40, 40, 30, themePalette(), sceneFace(p.faceKey(), m.text[0], true))
		return img
	}
	initials, icon := mark(""), mark("mdi:chef-hat")
	if string(initials.Pix) == string(icon.Pix) {
		t.Error("the icon is drawn as the initials")
	}
	for _, bad := range []string{"mdi:no-such-icon-anywhere", "chef-hat", "hass:chef-hat"} {
		if string(mark(bad).Pix) != string(initials.Pix) {
			t.Errorf("%q is not drawn as the initials", bad)
		}
	}
	// The page and the card carry the icon of the add-on's tile, which is the host's (its catalog entry).
	h := sampleHost(t, cookingPage())
	h.tiles[0].Icon = "mdi:chef-hat"
	useHost(t, h)
	d := &Display{poke: make(chan struct{}, 1)}
	if v := d.addonPageNow(h, "meals", "Meals"); v.icon != "mdi:chef-hat" {
		t.Errorf("the page's icon %q", v.icon)
	}
	if v := d.addonPageNow(h, "plex", "Plex Remote"); v.icon != "" {
		t.Errorf("an add-on without one has the icon %q", v.icon)
	}
	if c := addonNewestCard(h); c == nil || c.icon != "mdi:chef-hat" {
		t.Errorf("the card %+v", c)
	}
}
