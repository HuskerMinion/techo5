//go:build !dot

package display

import (
	"fmt"
	"image"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/pkg/addonapi"
)

// freshPictures has the test use pictures of its own, made by hand, and puts the screen's back after.
func freshPictures(t *testing.T) *addonPictures {
	t.Helper()
	was := addonPics
	a := &addonPictures{}
	addonPics = a
	t.Cleanup(func() { addonPics = was })
	return a
}

// waitPictures waits for the pictures asked for to be made.
func waitPictures(t *testing.T, a *addonPictures) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(2 * time.Millisecond) {
		a.mu.Lock()
		n := len(a.asked)
		a.mu.Unlock()
		if n == 0 {
			return
		}
	}
	t.Fatal("the pictures were not made")
}

// An add-on's pictures are its own: one add-on's key never stands for another's, they go when it does,
// and one on its way when it goes is not kept.
func TestAddonPicturesAreEachAddonsOwn(t *testing.T) {
	a := freshPictures(t)
	data := photo(t, 64, 48)
	meals, plex := scenePic{data: data, key: "meals/n:x"}, scenePic{data: data, key: "plex/n:x"}
	a.picture(meals, 32, 24)
	a.picture(plex, 32, 24)
	waitPictures(t, a)
	if a.picture(meals, 32, 24) == nil || a.picture(plex, 32, 24) == nil {
		t.Fatal("the pictures were not kept")
	}
	a.drop("meals")
	a.mu.Lock()
	n := len(a.kept)
	_, kept := a.kept[addonPicKey{"plex/n:x", 32, 24, false}]
	a.mu.Unlock()
	if n != 1 || !kept {
		t.Errorf("after Meals went, %d pictures are kept (Plex's: %v)", n, kept)
	}
	// One asked for, then dropped before it is made, is not kept.
	a.picture(scenePic{data: data, key: "meals/n:y"}, 16, 12)
	a.drop("meals")
	time.Sleep(50 * time.Millisecond)
	a.mu.Lock()
	_, came := a.kept[addonPicKey{"meals/n:y", 16, 12, false}]
	a.mu.Unlock()
	if came {
		t.Error("a picture on its way when its add-on went was kept")
	}
}

// An add-on has at most addonPicBurst pictures made at once, then addonPicsPerSecond a second: the rest
// wait, and another add-on's are not held up by them.
func TestAddonPicturesAreMadeAFewAtATime(t *testing.T) {
	a := freshPictures(t)
	a.changed = func() {}
	data := photo(t, 64, 48)
	for i := range 3 * addonPicBurst {
		a.picture(scenePic{data: data, key: fmt.Sprintf("meals/n:%d", i)}, 32, 24)
	}
	a.picture(scenePic{data: data, key: "plex/n:0"}, 32, 24)
	waitPictures(t, a)
	a.mu.Lock()
	meals, plex := 0, 0
	for k := range a.kept {
		if k.addon() == "meals" {
			meals++
		} else {
			plex++
		}
	}
	a.mu.Unlock()
	if meals > addonPicBurst+addonPicsPerSecond || meals < addonPicBurst || plex != 1 {
		t.Errorf("%d of Meals' pictures were made at once (at most %d), and %d of Plex's", meals, addonPicBurst, plex)
	}
}

// The pictures that would not decode are remembered for a while, and no more than addonPicsFailed of
// them; one that would not decode is an error, never nothing.
func TestAddonPicturesThatFail(t *testing.T) {
	a := freshPictures(t)
	a.failed = map[addonPicKey]time.Time{}
	for i := range 2 * addonPicsFailed {
		a.mu.Lock()
		a.failLocked(addonPicKey{key: fmt.Sprintf("meals/n:%d", i), w: 1, h: 1})
		a.mu.Unlock()
	}
	if len(a.failed) > addonPicsFailed {
		t.Errorf("%d failed pictures remembered", len(a.failed))
	}
	for what, data := range map[string][]byte{
		"not an image":    []byte("not an image at all"),
		"a cut-off PNG":   photo(t, 64, 48)[:60],
		"a PNG too large": photo(t, addonapi.MaxImageLong+1, 4),
	} {
		if img, err := scaledPicture(data, 10, 10, false); err == nil || img != nil {
			t.Errorf("%s: %v %v", what, img != nil, err)
		}
	}
	if img, err := scaledPicture(photo(t, 64, 48), 10, 10, true); err != nil || img.Rect.Dx() != 10 {
		t.Errorf("a picture: %v", err)
	}
}

// The card over the clock changes at most once a second (once in five over the night clock), however
// often the add-on patches it, and a frame is drawn for the change when it may; a card that went goes at
// once.
func TestAddonCardChangesAtMostSoOften(t *testing.T) {
	h := sampleHost(t, cookingPage())
	useHost(t, h)
	d := &Display{poke: make(chan struct{}, 1)}
	first := d.addonCardNow(h, false)
	if first == nil {
		t.Fatal("no card")
	}
	h.patch("meals", addonapi.OnCard, addonapi.Text("Next: chop"), time.Now())
	if got := d.addonCardNow(h, false); got.sc.Rev != first.sc.Rev {
		t.Error("a change a moment after the card was drawn was drawn at once")
	}
	d.mu.Lock()
	armed := d.addon.card.timer != nil
	d.addon.card.at = d.addon.card.at.Add(-addonCardEvery)
	d.mu.Unlock()
	if !armed {
		t.Error("no frame was asked for the change that waits")
	}
	got := d.addonCardNow(h, false)
	if got.sc.Rev == first.sc.Rev {
		t.Error("the change was not drawn once a second had passed")
	}
	// At night, five seconds.
	h.patch("meals", addonapi.OnCard, addonapi.Text("Next: stir"), time.Now())
	d.mu.Lock()
	d.addon.card.at = d.addon.card.at.Add(-2 * addonCardEvery)
	d.mu.Unlock()
	if n := d.addonCardNow(h, true); n.sc.Rev != got.sc.Rev {
		t.Error("over the night clock, the card changed within five seconds")
	}
	// Told of a change, the display draws no frame before the card may change.
	select {
	case <-d.poke:
	default:
	}
	addonSink{d}.CardChanged("meals")
	select {
	case <-d.poke:
		t.Error("a frame was drawn at once for a card that may not change yet")
	default:
	}
	// Gone, it goes at once.
	h.mu.Lock()
	delete(h.scenes, "meals/card")
	h.mu.Unlock()
	if d.addonCardNow(h, true) != nil {
		t.Error("a card that went was drawn on")
	}
}

// A card cleared and set again shows at once, rather than the card before it, whose taps the server no
// longer takes; set over and over, it is drawn no more than once an interval that way.
func TestAddonCardSetAgainShowsAtOnce(t *testing.T) {
	h := sampleHost(t, cookingPage())
	useHost(t, h)
	d := &Display{poke: make(chan struct{}, 1)}
	first := d.addonCardNow(h, false)
	h.set("meals", addonapi.OnCard, addonapi.Text("A new card"), time.Now())
	got := d.addonCardNow(h, false)
	if got.sc.Set == first.sc.Set {
		t.Error("a card set again a moment after was held behind the one before it")
	}
	h.set("meals", addonapi.OnCard, addonapi.Text("And another"), time.Now())
	if again := d.addonCardNow(h, false); again.sc.Set != got.sc.Set {
		t.Error("a card set over and over was drawn each time")
	}
}

// A new scene starts its lists at their tops; a patch leaves them where they were scrolled to.
func TestAddonScrollsAreTheScenes(t *testing.T) {
	h := sampleHost(t, listPage())
	useHost(t, h)
	d := &Display{poke: make(chan struct{}, 1)}
	d.openAddon("meals", "Meals")
	d.addonPageNow(h, "meals", "Meals")
	d.mu.Lock()
	d.addon.scrolls[0] = 120
	d.mu.Unlock()
	// A patch: the same scene.
	h.patch("meals", addonapi.OnPage, listPage(), time.Now())
	if v := d.addonPageNow(h, "meals", "Meals"); v.scrolls[0] != 120 {
		t.Errorf("a patch scrolled the list to %d", v.scrolls[0])
	}
	h.set("meals", addonapi.OnPage, listPage(), time.Now())
	if v := d.addonPageNow(h, "meals", "Meals"); v.scrolls[0] != 0 {
		t.Errorf("a new scene's list starts scrolled %d", v.scrolls[0])
	}
}

// A card's content is drawn inside its box only: a layout taller than the box (the tallest card's room,
// moved down to where this one's content starts) reaches nothing under it.
func TestAddonCardStaysInItsBox(t *testing.T) {
	long := addonapi.Column(addonapi.Block{Type: addonapi.BlockText, Text: "one\ntwo\nthree", Size: addonapi.SizeTitle},
		addonapi.Block{Type: addonapi.BlockText, Text: "four\nfive\nsix", Size: addonapi.SizeTitle})
	box := image.Rect(40, 380, 920, 460)
	inner := image.Rect(52, 300, 908, 460) // taller than the box, as the tallest card's room is
	card := func(root addonapi.Block) *image.RGBA {
		img := image.NewRGBA(image.Rect(0, 0, 960, 480))
		p := &paint{dst: img, w: 960, h: 480, sNum: 1, sDen: 1}
		v := &addonCardView{id: "meals", name: "Meals", sc: hostScene{Root: root, Rev: 1, Set: 1}}
		var drawn addonDrawn
		var buf *image.RGBA
		p.addonCardIn(v, box, inner, &drawn, nil, false, &buf)
		return img
	}
	// The card's own box and shadow are drawn either way; what it holds may add nothing outside the box.
	full, empty := card(long), card(addonapi.Column(addonapi.Spacer()))
	for y := 0; y < 480; y++ {
		for x := 0; x < 960; x++ {
			if !image.Pt(x, y).In(box) && full.RGBAAt(x, y) != empty.RGBAAt(x, y) {
				t.Fatalf("the card's content drew at %d,%d, outside its box %v", x, y, box)
			}
		}
	}
}

// A tap is sent with the revision of the scene whose layout it was found in.
func TestAddonTapCarriesItsRevision(t *testing.T) {
	h := sampleHost(t, cookingPage())
	useHost(t, h)
	h.set("meals", addonapi.OnPage, cookingPage(), time.Now()) // revision 2
	img := image.NewRGBA(image.Rect(0, 0, 960, 480))
	p := &paint{dst: img, w: 960, h: 480, sNum: 1, sDen: 1}
	var kept addonLaid
	sc, _ := h.Scene("meals", addonapi.OnPage)
	lay := p.laid(&kept, "meals", sc, 0, image.Rect(20, 80, 940, 460), false, nil)
	if lay.rev != sc.Rev || sc.Rev != 2 {
		t.Errorf("the layout of revision %d says %d", sc.Rev, lay.rev)
	}
}

// What does not fit whole in a column, a row or a card is left out, never drawn over the block below it
// or cut through: no two of a page's blocks outside its lists overlap, each is inside its room, and a
// card's blocks are all inside the card's height. Gaps narrow and pictures shrink first.
func TestAddonOverflowIsLeftOut(t *testing.T) {
	h := sampleHost(t, overflowPage())
	useHost(t, h)
	for _, size := range []image.Point{{960, 480}, {1280, 800}} {
		img := image.NewRGBA(image.Rectangle{Max: size})
		p := &paint{dst: img, w: size.X, h: size.Y, sNum: size.X, sDen: 960}
		_, _, content := p.addonPageRooms()
		sc, _ := h.Scene("meals", addonapi.OnPage)
		imgs := func(b *addonapi.Block) ([]byte, string, bool) { return h.Image("meals", b) }
		lay := p.layoutScene(&sc.Root, content, false, imgs)
		var leaves []image.Rectangle
		for _, b := range lay.boxes {
			if b.list > 0 || b.step != 0 || b.b == nil || b.b.Type == addonapi.BlockCard {
				continue
			}
			r := b.r
			if b.b.Type == addonapi.BlockImage {
				r = r.Intersect(b.clip)
			}
			if !r.In(content) {
				t.Errorf("%v: a %s at %v, outside the page's room %v", size, b.b.Type, r, content)
			}
			for _, o := range leaves {
				if o.Overlaps(r) {
					t.Errorf("%v: a %s at %v over another block at %v", size, b.b.Type, r, o)
				}
			}
			leaves = append(leaves, r)
		}
		// On a page nothing is left out: the column too tall for its room scrolls, and its stepper and
		// toggle are in it, reachable.
		reach := map[string]bool{}
		for _, b := range lay.boxes {
			if b.b != nil && (b.b.ID == "servings" || b.b.ID == "warm") && b.tap != "" {
				reach[b.b.ID] = true
			}
		}
		if !reach["servings"] || !reach["warm"] {
			t.Errorf("%v: the page's stepper and toggle are left out: %v", size, reach)
		}
		card := p.layoutScene(func() *addonapi.Block { c := crowdedCard(); return &c }(), image.Rect(60, 500, size.X-60, 500+size.Y/5), true, nil)
		for _, b := range card.boxes {
			if b.r.Max.Y > 500+card.height {
				t.Errorf("%v: a card's %s ends at %d, past the card's %d", size, b.b.Type, b.r.Max.Y, 500+card.height)
			}
		}
	}
}

// A scrolling column leaves its thumb a gutter, and shows its words and controls only whole: scrolled
// part of the way, nothing is drawn or tapped of a block cut by the view's edge.
func TestAddonScrollingColumnEdges(t *testing.T) {
	h := sampleHost(t, overflowPage())
	useHost(t, h)
	img := image.NewRGBA(image.Rect(0, 0, 960, 480))
	p := &paint{dst: img, w: 960, h: 480, sNum: 960, sDen: 960}
	_, _, content := p.addonPageRooms()
	sc, _ := h.Scene("meals", addonapi.OnPage)
	lay := p.layoutScene(&sc.Root, content, false, func(b *addonapi.Block) ([]byte, string, bool) { return h.Image("meals", b) })
	col := lay.lists[0] // the left column, laid out before the list beside it
	if lay.maxScroll(0) <= 0 {
		t.Fatal("the column does not scroll on a Show 5")
	}
	for step := 0; step <= lay.maxScroll(0); step += 7 {
		scrolls := []int{step}
		for i, b := range lay.boxes {
			if b.list != 1 {
				continue
			}
			r, seen := lay.shown(i, scrolls, image.Point{})
			if b.tap != "" && r.Max.X > col.view.Max.X-scrollGutter(p) {
				t.Errorf("a tap target at %v reaches the thumb's gutter of %v", r, col.view)
			}
			if seen.Empty() || b.b == nil || b.b.Type == addonapi.BlockImage {
				continue
			}
			if seen != r {
				t.Errorf("scrolled %d: a %s is shown cut, %v of %v", step, b.b.Type, seen, r)
			}
		}
	}
}
