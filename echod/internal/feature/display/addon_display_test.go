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
	h.set("meals", addonapi.OnCard, addonapi.Text("Next: chop"), time.Now())
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
	h.set("meals", addonapi.OnCard, addonapi.Text("Next: stir"), time.Now())
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
	h.mu.Lock()
	sc := h.scenes["meals/page"]
	sc.Rev++
	h.scenes["meals/page"] = sc
	h.mu.Unlock()
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
	lay := p.laid(&kept, "meals", sc, image.Rect(20, 80, 940, 460), false, nil)
	if lay.rev != sc.Rev || sc.Rev != 2 {
		t.Errorf("the layout of revision %d says %d", sc.Rev, lay.rev)
	}
}
