package serve

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/png"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/pkg/addonapi"
)

// screen.open brings a page up only in answer to a person: within ScreenOpenWindow of one of the
// add-on's commands, or of a tap on its tile, its card or its page. At any other time, 3 a.m. included,
// it is refused busy, before its rate is counted.
func TestScreenOpenNeedsAPerson(t *testing.T) {
	r := newRig(t, everything)
	r.freeze()
	k := &sink{}
	r.s.SetScreen(k)
	c := r.dial()
	if err := c.SetScene(ctx(t), addonapi.OnPage, page); err != nil {
		t.Fatal(err)
	}
	for range addonapi.ScreenOpenBurst + 1 {
		if err := c.OpenScreen(ctx(t)); code(err) != addonapi.CodeBusy {
			t.Fatalf("an open nobody asked for: %v", err)
		}
	}
	if k.count("open meals") != 0 {
		t.Fatal("the display was asked to open a page nobody asked for")
	}

	// A command sent to it from Home Assistant or by voice.
	if _, err := r.s.Command(ctx(t), "meals", "next", nil, "action", false); err != nil {
		t.Fatal(err)
	}
	r.advance(addonapi.ScreenOpenWindow - time.Second)
	if err := c.OpenScreen(ctx(t)); err != nil || k.count("open meals") != 1 {
		t.Errorf("an open just after a command: %v", err)
	}
	r.advance(2 * time.Second)
	if err := c.OpenScreen(ctx(t)); code(err) != addonapi.CodeBusy {
		t.Errorf("an open past the window: %v", err)
	}

	// A tap on one of its buttons in view.
	r.s.Shown("meals", addonapi.OnPage, true)
	if !touch(r, "meals", addonapi.OnPage, "next") {
		t.Fatal("the tap")
	}
	if err := c.OpenScreen(ctx(t)); err != nil {
		t.Errorf("an open after a tap: %v", err)
	}
	// A tap on its tile or its card, which the display says.
	r.advance(time.Minute)
	r.s.Asked("meals")
	if err := c.OpenScreen(ctx(t)); err != nil {
		t.Errorf("an open after a tap on the tile: %v", err)
	}
	// A tap that reached nothing of it asks nothing.
	r.advance(time.Minute)
	if touch(r, "meals", addonapi.OnPage, "step") {
		t.Fatal("a tap on words was sent")
	}
	if err := c.OpenScreen(ctx(t)); code(err) != addonapi.CodeBusy {
		t.Errorf("an open after a tap on nothing: %v", err)
	}
}

// A list's item with an id is tapped as a whole; one without is not.
func TestListItemsAreTapped(t *testing.T) {
	r, _, c := screenRig(t)
	ev := c.WatchScreen()
	list := addonapi.List(addonapi.Section{Heading: "Recipes", Items: []addonapi.Block{
		{Type: addonapi.BlockRow, ID: "soup", Children: []addonapi.Block{addonapi.Text("Soup"), addonapi.Button("fav", "", "mdi:star")}},
		addonapi.Text("Bread"),
		{Type: addonapi.BlockText, ID: "plain", Text: "Plain"},
	}})
	inner := addonapi.Column(addonapi.Block{Type: addonapi.BlockText, ID: "words", Text: "Not in a list"}, list)
	if err := c.SetScene(ctx(t), addonapi.OnPage, inner); err != nil {
		t.Fatal(err)
	}
	r.s.Shown("meals", addonapi.OnPage, true)
	nextScreen(t, ev)
	for _, id := range []string{"soup", "plain", "fav"} {
		if !touch(r, "meals", addonapi.OnPage, id) {
			t.Errorf("a tap on %s", id)
			continue
		}
		if e := nextScreen(t, ev); e.Touch.ID != id {
			t.Errorf("tap on %s: %+v", id, e)
		}
	}
	if touch(r, "meals", addonapi.OnPage, "words") || touch(r, "meals", addonapi.OnPage, "") {
		t.Error("a tap on words outside a list, or on no id, was sent")
	}
}

// image.put keeps an image by name in RAM, within its limits and the add-on's 8 MB: a put past the
// budget is refused, and nothing is let go to make room for it but images fetched by address.
func TestImagePutAndTheBudget(t *testing.T) {
	r, k, c := screenRig(t)
	small := pngBytes(t, 4, 4)
	for name, b := range map[string][]byte{
		"Bad Name": small,
		"notimage": []byte("GIF89a"),
	} {
		if _, err := putRaw(ctx(t), c, name, b); code(err) != addonapi.CodeBadRequest {
			t.Errorf("put %q: %v", name, err)
		}
	}
	if _, err := putRaw(ctx(t), c, "wide", pngBytes(t, addonapi.MaxImageLong+1, 10)); code(err) != addonapi.CodeTooLarge {
		t.Errorf("an image wider than a Show 8: %v", err)
	}

	// Each 1000 by 500 counts 2 000 000 bytes: four fit in 8 MB, a fifth does not.
	photo := pngBytes(t, 1000, 500)
	cost := addonapi.ImageCost(1000, 500, len(photo))
	for i, name := range []string{"a", "b", "c", "d"} {
		got, err := c.PutImage(ctx(t), name, photo)
		if err != nil || got.Used != (i+1)*cost || got.Budget != addonapi.MaxImageBudget {
			t.Fatalf("put %s: %+v %v", name, got, err)
		}
	}
	if _, err := c.PutImage(ctx(t), "e", photo); code(err) != addonapi.CodeTooLarge || !strings.Contains(err.Error(), "image.drop") {
		t.Errorf("a put past the budget: %v", err)
	}
	// A put of a name again takes its old room.
	if got, err := c.PutImage(ctx(t), "d", photo); err != nil || got.Used != 4*cost {
		t.Errorf("a put of a name again: %+v %v", got, err)
	}
	if got, err := c.DropImage(ctx(t), "d"); err != nil || got.Used != 3*cost {
		t.Errorf("drop: %+v %v", got, err)
	}
	if _, err := c.DropImage(ctx(t), "d"); code(err) != addonapi.CodeBadRequest {
		t.Errorf("a drop of what is not there: %v", err)
	}
	if _, err := c.PutImage(ctx(t), "e", photo); err != nil {
		t.Errorf("a put once there is room: %v", err)
	}

	// A scene shows a put image by name; a change to the image has the page laid out again.
	if err := c.SetScene(ctx(t), addonapi.OnPage, addonapi.Column(addonapi.ImageNamed("a"))); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the page", func() bool { return k.count("page meals") == 1 })
	sc, _ := r.s.Scene("meals", addonapi.OnPage)
	data, key, ok := r.s.Image("meals", &sc.Root.Children[0])
	if !ok || len(data) != len(photo) || !strings.HasPrefix(key, "meals/n:") {
		t.Fatalf("the image by name: %d %q %v", len(data), key, ok)
	}
	if _, err := c.PutImage(ctx(t), "a", small); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the page told of the image", func() bool { return k.count("page meals") == 2 })
	after, _ := r.s.Scene("meals", addonapi.OnPage)
	_, key2, _ := r.s.Image("meals", &after.Root.Children[0])
	if after.Images == sc.Images || key2 == key {
		t.Errorf("a put of the name again is the same image: %d %d %q %q", sc.Images, after.Images, key, key2)
	}

	// Gone with the add-on's last connection, as its scenes are.
	c.Close()
	eventually(t, "the images to go", func() bool { return r.s.ImageUsed("meals") == 0 })
}

// putRaw puts an image without the client's own check, so the device's is what refuses it.
func putRaw(ctx context.Context, c *addonapi.Client, name string, b []byte) (addonapi.ImageBudget, error) {
	var out addonapi.ImageBudget
	err := c.Call(ctx, addonapi.OpImagePut, addonapi.ImagePut{Name: name, Data: b}, &out)
	return out, err
}

// An image.put may be longer than any other message, to MaxImageMessage; another message that long is
// too_large, and so is an image.put from an add-on without the screen.
func TestOnlyAnImagePutIsLong(t *testing.T) {
	r, _, c := screenRig(t)
	photo := noisePNG(t, 300, 200)
	if len(photo) <= addonapi.MaxMessage || len(photo) > addonapi.MaxImage {
		t.Fatalf("the test image is %d bytes", len(photo))
	}
	if _, err := c.PutImage(ctx(t), "photo", photo); err != nil {
		t.Fatalf("a put longer than a message: %v", err)
	}
	w := r.raw()
	w.send(`{"id":1,"op":"hello","args":{"api":1}}`)
	w.read()
	pad := strings.Repeat("a", addonapi.MaxMessage)
	w.send(`{"id":2,"op":"speak","args":{"text":"` + pad + `"}}`)
	if got := w.read(); !strings.Contains(got, `"too_large"`) {
		t.Errorf("a long speak: %s", got)
	}
	w.send(`{"id":3,"op":"image.put","args":{"name":"p","data":"` + base64.StdEncoding.EncodeToString(photo) + `"}}`)
	if got := w.read(); !strings.Contains(got, `"ok":true`) {
		t.Errorf("a long put by hand: %s", got)
	}
}

// Images by address are fetched by the daemon, off the draw path, at most fetchesAtOnce at a time, each
// once until it fails, and then not again for imageRetry; they count toward the budget, and are let go,
// the least recently drawn first, to make room for a put.
func TestImagesByAddress(t *testing.T) {
	was := keptFor
	keptFor = 300 * time.Millisecond
	t.Cleanup(func() { keptFor = was })
	r, k, c := screenRig(t)
	var mu sync.Mutex
	served := map[string][]byte{}
	var asked []string
	release := make(chan struct{})
	r.s.FetchImage = func(ctx context.Context, u string) ([]byte, error) {
		mu.Lock()
		asked = append(asked, u)
		b, ok := served[u]
		mu.Unlock()
		if strings.Contains(u, "slow") {
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if !ok {
			return nil, errors.New("404")
		}
		return b, nil
	}
	photo := pngBytes(t, 1000, 500)
	cost := addonapi.ImageCost(1000, 500, len(photo))
	mu.Lock()
	served["http://192.168.1.20/a.png"] = photo
	served["http://192.168.1.20/b.png"] = photo
	mu.Unlock()
	scene := addonapi.Column(addonapi.Image("http://192.168.1.20/a.png"), addonapi.Image("http://192.168.1.20/b.png"),
		addonapi.Image("http://192.168.1.20/missing.png"))
	if err := c.SetScene(ctx(t), addonapi.OnPage, scene); err != nil {
		t.Fatal(err)
	}
	sc, _ := r.s.Scene("meals", addonapi.OnPage)
	img := func(i int) bool {
		cur, _ := r.s.Scene("meals", addonapi.OnPage)
		_, _, ok := r.s.Image("meals", &cur.Root.Children[i])
		return ok
	}
	if img(0) {
		t.Fatal("an address was there before it was fetched")
	}
	img(1)
	img(2) // a third waits: two at once
	eventually(t, "two fetched", func() bool { return img(0) && img(1) })
	eventually(t, "the display told", func() bool { return k.count("page meals") >= 2 })
	if cur, _ := r.s.Scene("meals", addonapi.OnPage); cur.Images == sc.Images {
		t.Error("a fetched image did not have the page laid out again")
	}
	img(2)
	eventually(t, "the missing one asked for", func() bool { mu.Lock(); defer mu.Unlock(); return len(asked) == 3 })
	eventually(t, "the missing one done", func() bool { return len(r.s.fetching("meals")) == 0 })
	for range 5 {
		img(2)
	}
	r.advance(0)
	mu.Lock()
	n := len(asked)
	mu.Unlock()
	if n != 3 {
		t.Errorf("an image that failed was fetched again at once: %d fetches", n)
	}
	if used := r.s.ImageUsed("meals"); used != 2*cost {
		t.Errorf("fetched images count %d, want %d", used, 2*cost)
	}

	// The fetched images the page shows are not let go for a put: that would leave holes in the page.
	for _, name := range []string{"p", "q"} {
		if _, err := c.PutImage(ctx(t), name, photo); err != nil {
			t.Fatalf("put %s: %v", name, err)
		}
	}
	if _, err := c.PutImage(ctx(t), "s", photo); code(err) != addonapi.CodeTooLarge {
		t.Errorf("a put that would let go an image the page shows: %v", err)
	}
	// Puts make room by letting the fetched ones go that nothing shows now, the one asked for least
	// recently first, once the display has not asked for them for a moment.
	if err := c.SetScene(ctx(t), addonapi.OnPage, addonapi.Column(addonapi.Image("http://192.168.1.20/b.png"))); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PutImage(ctx(t), "s", photo); code(err) != addonapi.CodeTooLarge {
		t.Errorf("a put that would let go an image the display asked for a moment ago: %v", err)
	}
	time.Sleep(keptFor + 50*time.Millisecond)
	if err := c.SetScene(ctx(t), addonapi.OnPage, addonapi.Column(addonapi.Text("no pictures"))); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PutImage(ctx(t), "s", photo); err != nil {
		t.Fatalf("a put that lets go an image nothing shows: %v", err)
	}
	if r.s.ImageUsed("meals") != 4*cost {
		t.Errorf("the room is wrong: %d", r.s.ImageUsed("meals"))
	}
	if _, err := c.PutImage(ctx(t), "u", photo); err != nil {
		t.Fatalf("a put that needs the last fetched image's room: %v", err)
	}
	if _, err := c.PutImage(ctx(t), "v", photo); code(err) != addonapi.CodeTooLarge {
		t.Errorf("a put with nothing fetched left to let go: %v", err)
	}

	// A fetch on its way when the add-on's last connection closes is not kept.
	if err := c.SetScene(ctx(t), addonapi.OnPage, addonapi.Column(addonapi.Image("http://192.168.1.20/slow.png"))); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	served["http://192.168.1.20/slow.png"] = pngBytes(t, 2, 2)
	mu.Unlock()
	img(0)
	c.Close()
	eventually(t, "the images to go", func() bool { return r.s.ImageUsed("meals") == 0 })
	close(release)
	time.Sleep(50 * time.Millisecond)
	if r.s.ImageUsed("meals") != 0 {
		t.Error("a fetch that was on its way was kept after the add-on's connection closed")
	}
}

// noisePNG is a PNG of noise, which does not compress: longer than a message.
func noisePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	x := uint32(1)
	for i := range img.Pix {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		img.Pix[i] = byte(x)
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// An image's key names the add-on as well as its bytes: one add-on's never stands for another's, and the
// same bytes put again, or fetched again, are the same key, so the display decodes them once.
func TestImageKeys(t *testing.T) {
	r, _, c := screenRig(t)
	icon := pngBytes(t, 8, 8)
	b := addonapi.ImageData(icon)
	_, mine, _ := r.s.Image("meals", &b)
	_, theirs, _ := r.s.Image("plex", &b)
	if mine == theirs || !strings.HasPrefix(mine, "meals/d:") || len(mine) != len("meals/d:")+64 {
		t.Errorf("inline keys %q and %q", mine, theirs)
	}
	if _, err := c.PutImage(ctx(t), "a", icon); err != nil {
		t.Fatal(err)
	}
	named := addonapi.ImageNamed("a")
	_, k1, _ := r.s.Image("meals", &named)
	if _, err := c.PutImage(ctx(t), "a", icon); err != nil {
		t.Fatal(err)
	}
	_, k2, _ := r.s.Image("meals", &named)
	if k1 != k2 || !strings.HasPrefix(k1, "meals/n:") {
		t.Errorf("the same bytes put again: %q %q", k1, k2)
	}
}

// A page whose images by address are more than the add-on's 8 MB does not have them let go and fetched
// again round and round: those it shows stay, the one with no room is not kept and not fetched again for
// a minute, and a refetch of the same bytes is the same key.
func TestImagesThatDoNotFitDoNotThrash(t *testing.T) {
	r, _, c := screenRig(t)
	photo := pngBytes(t, 1280, 700) // 3.5 MB decoded, so two fit and a third does not
	var mu sync.Mutex
	fetches := map[string]int{}
	r.s.FetchImage = func(ctx context.Context, u string) ([]byte, error) {
		mu.Lock()
		fetches[u]++
		mu.Unlock()
		return photo, nil
	}
	urls := []string{"http://192.168.1.20/a.png", "http://192.168.1.20/b.png", "http://192.168.1.20/c.png"}
	scene := addonapi.Column(addonapi.Image(urls[0]), addonapi.Image(urls[1]), addonapi.Image(urls[2]))
	if err := c.SetScene(ctx(t), addonapi.OnPage, scene); err != nil {
		t.Fatal(err)
	}
	// The display, laying the page out each time it is told, as it does.
	layout := func() int {
		cur, _ := r.s.Scene("meals", addonapi.OnPage)
		n := 0
		for i := range cur.Root.Children {
			if _, _, ok := r.s.Image("meals", &cur.Root.Children[i]); ok {
				n++
			}
		}
		return n
	}
	for end := time.Now().Add(time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		layout()
	}
	mu.Lock()
	total := 0
	for _, n := range fetches {
		total += n
	}
	mu.Unlock()
	if total != 3 {
		t.Errorf("%d fetches for three images in a second of layouts: %v", total, fetches)
	}
	if n := layout(); n != 2 {
		t.Errorf("%d of the images are there, not the two that fit", n)
	}
}

// A fetch that fails lays the page out again, so the images waiting behind it are asked for, and once its
// minute is up the page is laid out again so it is tried again.
func TestAFailedFetchMakesWay(t *testing.T) {
	was := imageRetry
	imageRetry = 200 * time.Millisecond
	t.Cleanup(func() { imageRetry = was })
	r, k, c := screenRig(t)
	var mu sync.Mutex
	asked := map[string]int{}
	r.s.FetchImage = func(ctx context.Context, u string) ([]byte, error) {
		mu.Lock()
		asked[u]++
		mu.Unlock()
		return nil, errors.New("404")
	}
	scene := addonapi.Column(addonapi.Image("http://192.168.1.20/a.png"), addonapi.Image("http://192.168.1.20/b.png"),
		addonapi.Image("http://192.168.1.20/c.png"))
	if err := c.SetScene(ctx(t), addonapi.OnPage, scene); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the page", func() bool { return k.count("page meals") == 1 })
	layout := func() {
		cur, _ := r.s.Scene("meals", addonapi.OnPage)
		for i := range cur.Root.Children {
			r.s.Image("meals", &cur.Root.Children[i])
		}
	}
	layout() // two start; the third waits for a place
	eventually(t, "the display told the two failed", func() bool { return k.count("page meals") >= 2 && len(r.s.fetching("meals")) == 0 })
	layout()
	eventually(t, "the third asked for", func() bool { mu.Lock(); defer mu.Unlock(); return asked["http://192.168.1.20/c.png"] == 1 })
	told := k.count("page meals")
	eventually(t, "the page laid out again once the minute is up", func() bool { return k.count("page meals") > told })
	time.Sleep(imageRetry)
	layout()
	eventually(t, "a failed one tried again", func() bool { mu.Lock(); defer mu.Unlock(); return asked["http://192.168.1.20/a.png"] == 2 })
}

// What an add-on's store remembers not to fetch yet is held to maxNotBefore, whatever it names.
func TestNotBeforeIsHeld(t *testing.T) {
	var st imageStore
	st.init()
	now := time.Now()
	for i := range 3 * maxNotBefore {
		st.wait(fmt.Sprintf("http://192.168.1.20/%d.png", i), now.Add(time.Duration(i)*time.Second), now)
	}
	if len(st.notBefore) > maxNotBefore {
		t.Errorf("%d addresses remembered", len(st.notBefore))
	}
	if _, ok := st.notBefore[fmt.Sprintf("http://192.168.1.20/%d.png", 3*maxNotBefore-1)]; !ok {
		t.Error("the newest was forgotten")
	}
}
