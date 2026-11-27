//go:build !dot

package display

import (
	"bytes"
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
	h.scenes[id+"/"+on] = hostScene{Root: root, Rev: rev, At: at}
}

func (h *fakeHost) Shown(id, on string, shown bool) {
	h.note("shown " + id + " " + on + " " + map[bool]string{true: "on", false: "off"}[shown])
}

func (h *fakeHost) Touch(id, on, block string) bool {
	h.note("touch " + id + " " + on + " " + block)
	return true
}
func (h *fakeHost) Swipe(id, direction string) bool {
	h.note("swipe " + id + " " + direction)
	return true
}
func (h *fakeHost) Back(id string) bool   { h.note("back " + id); return true }
func (h *fakeHost) DismissCard(id string) { h.note("dismiss " + id) }
func (h *fakeHost) Asked(id string)       { h.note("asked " + id) }

func (h *fakeHost) Image(_ string, b *addonapi.Block) ([]byte, string, bool) {
	if len(b.Data) > 0 {
		return b.Data, "d", true
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	data, ok := h.images[b.Name]
	return data, "n:" + b.Name, ok
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
	return &addonPageView{id: id, name: addonNameIn(h, id), sc: sc, has: ok,
		images: func(b *addonapi.Block) ([]byte, string, bool) { return h.Image(id, b) }, pics: picturesNow}
}

// addonCardOf is h's card of id likewise.
func addonCardOf(h *fakeHost, id string) *addonCardView {
	sc, _ := h.Scene(id, addonapi.OnCard)
	return &addonCardView{id: id, name: addonNameIn(h, id), sc: sc,
		images: func(b *addonapi.Block) ([]byte, string, bool) { return h.Image(id, b) }, pics: picturesNow}
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
