package serve

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/pkg/addonapi"
)

// sink is a display that records what it was told.
type sink struct {
	mu    sync.Mutex
	calls []string
}

func (k *sink) note(s string)         { k.mu.Lock(); k.calls = append(k.calls, s); k.mu.Unlock() }
func (k *sink) PageChanged(id string) { k.note("page " + id) }
func (k *sink) CardChanged(id string) { k.note("card " + id) }
func (k *sink) Open(id string)        { k.note("open " + id) }
func (k *sink) Close(id string)       { k.note("close " + id) }

func (k *sink) count(call string) int {
	k.mu.Lock()
	defer k.mu.Unlock()
	n := 0
	for _, c := range k.calls {
		if c == call {
			n++
		}
	}
	return n
}

func screenRig(t *testing.T) (*rig, *sink, *addonapi.Client) {
	t.Helper()
	r := newRig(t, everything)
	k := &sink{}
	r.s.SetScreen(k)
	return r, k, r.dial()
}

var page = addonapi.Column(
	addonapi.Block{Type: addonapi.BlockText, ID: "step", Text: "Step 1"},
	addonapi.Row(addonapi.Button("back", "Back", ""), addonapi.Button("next", "Next", "mdi:arrow-right")),
)

func TestScreenNeedsTheCapability(t *testing.T) {
	r := newRig(t, func(dir string) Grant {
		g := everything(dir)
		g.Caps = []string{addonapi.CapTimers}
		return g
	})
	r.s.SetScreen(&sink{})
	c := r.dial()
	for op, a := range map[string]any{
		addonapi.OpSceneSet:    addonapi.SceneSet{On: addonapi.OnPage, Scene: page},
		addonapi.OpScenePatch:  addonapi.ScenePatch{On: addonapi.OnPage, ID: "step", Block: addonapi.Text("x")},
		addonapi.OpScreenOpen:  addonapi.None{},
		addonapi.OpScreenClose: addonapi.None{},
		addonapi.OpCardClear:   addonapi.None{},
	} {
		if err := c.Call(ctx(t), op, a, nil); code(err) != addonapi.CodeDenied {
			t.Errorf("%s without the screen capability: %v", op, err)
		}
	}
	if got := r.s.Screens(); len(got) != 0 {
		t.Errorf("an add-on without the screen has a tile: %v", got)
	}
}

// A device without a screen (a Dot) refuses the screen's ops, whatever was granted.
func TestScreenUnavailableWithoutOne(t *testing.T) {
	r := newRig(t, everything)
	c := r.dial()
	if err := c.SetScene(ctx(t), addonapi.OnPage, page); code(err) != addonapi.CodeUnavailable {
		t.Errorf("a scene on a device without a screen: %v", err)
	}
	if err := c.OpenScreen(ctx(t)); code(err) != addonapi.CodeUnavailable {
		t.Errorf("screen.open on a device without a screen: %v", err)
	}
}

// A device whose server speaks only stage 1 answers the screen's ops unknown_op; here, every op the
// level knows is answered as an op, so a new one cannot be added and forgotten in handle.
func TestEveryScreenOpIsHandled(t *testing.T) {
	_, _, c := screenRig(t)
	for _, op := range []string{addonapi.OpSceneSet, addonapi.OpScenePatch, addonapi.OpScreenOpen, addonapi.OpScreenClose, addonapi.OpCardClear} {
		if err := c.Call(ctx(t), op, nil, nil); code(err) == addonapi.CodeUnknownOp {
			t.Errorf("%s is unknown", op)
		}
	}
}

func TestSceneSetAndPatch(t *testing.T) {
	r, k, c := screenRig(t)
	if err := c.PatchScene(ctx(t), addonapi.OnPage, "step", addonapi.Text("x")); code(err) != addonapi.CodeBadRequest {
		t.Errorf("a patch with no page: %v", err)
	}
	if err := c.SetScene(ctx(t), addonapi.OnPage, page); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the display to be told of the page", func() bool { return k.count("page meals") == 1 })
	sc, ok := r.s.Scene("meals", addonapi.OnPage)
	if !ok || sc.Root.Children[0].Text != "Step 1" {
		t.Fatalf("the page is %+v %v", sc, ok)
	}
	if err := c.PatchScene(ctx(t), addonapi.OnPage, "step", addonapi.Title("Step 2")); err != nil {
		t.Fatal(err)
	}
	after, _ := r.s.Scene("meals", addonapi.OnPage)
	if b, _ := after.Root.Find("step"); b.Text != "Step 2" || b.Size != addonapi.SizeTitle || after.Rev <= sc.Rev {
		t.Errorf("the patched page is %+v", after)
	}
	// What the display held is untouched by the patch.
	if sc.Root.Children[0].Text != "Step 1" {
		t.Error("a patch changed the scene the display was given")
	}
	eventually(t, "the display to be told of the patch", func() bool { return k.count("page meals") == 2 })

	for what, p := range map[string]addonapi.ScenePatch{
		"an unknown id":            {On: addonapi.OnPage, ID: "nothing", Block: addonapi.Text("x")},
		"another id":               {On: addonapi.OnPage, ID: "step", Block: addonapi.Block{Type: addonapi.BlockText, ID: "other", Text: "x"}},
		"a duplicate id":           {On: addonapi.OnPage, ID: "step", Block: addonapi.Column(addonapi.Button("next", "Again", ""))},
		"an unknown type":          {On: addonapi.OnPage, ID: "step", Block: addonapi.Block{Type: "html"}},
		"the card, which has none": {On: addonapi.OnCard, ID: "step", Block: addonapi.Text("x")},
		"nowhere":                  {On: "clock", ID: "step", Block: addonapi.Text("x")},
		"another's timer":          {On: addonapi.OnPage, ID: "step", Block: addonapi.Countdown("local:theirs")},
	} {
		if err := c.Call(ctx(t), addonapi.OpScenePatch, p, nil); code(err) != addonapi.CodeBadRequest {
			t.Errorf("a patch of %s: %v", what, err)
		}
	}
	// A patch that would take the page past its limits is refused, and the page stays as it was.
	long := addonapi.Column()
	for range addonapi.MaxBlocks {
		long.Children = append(long.Children, addonapi.Divider())
	}
	if err := c.PatchScene(ctx(t), addonapi.OnPage, "step", long); code(err) != addonapi.CodeTooLarge {
		t.Errorf("a patch past the blocks: %v", err)
	}
	if now, _ := r.s.Scene("meals", addonapi.OnPage); now.Rev != after.Rev {
		t.Error("a refused patch changed the page")
	}
}

// Every limit, as the device answers it: the client checks first, so these go by hand.
func TestSceneLimitsOnTheWire(t *testing.T) {
	r, _, c := screenRig(t)
	set := func(on string, b addonapi.Block) error {
		return c.Call(ctx(t), addonapi.OpSceneSet, addonapi.SceneSet{On: on, Scene: b}, nil)
	}
	many := addonapi.Column()
	for range addonapi.MaxBlocks {
		many.Children = append(many.Children, addonapi.Divider())
	}
	deep := addonapi.Text("x")
	for range addonapi.MaxSceneDepth {
		deep = addonapi.Column(deep)
	}
	for what, b := range map[string]addonapi.Block{
		"too many blocks":  many,
		"too deep":         deep,
		"too many words":   addonapi.Text(strings.Repeat("a", addonapi.MaxText+1)),
		"an image too big": addonapi.ImageData(pngBytes(t, addonapi.MaxImageLong+1, 1)),
	} {
		if err := set(addonapi.OnPage, b); code(err) != addonapi.CodeTooLarge {
			t.Errorf("%s: %v", what, err)
		}
	}
	for what, b := range map[string]addonapi.Block{
		"an unknown block":      {Type: "iframe"},
		"a button with no id":   {Type: addonapi.BlockButton, Label: "Go"},
		"a bad id":              addonapi.Button("Go!", "Go", ""),
		"duplicate ids":         addonapi.Row(addonapi.Button("go", "Go", ""), addonapi.Chip("go", "Go", "")),
		"an image from outside": addonapi.Image("https://1.1.1.1/a.png"),
		"another's timer":       addonapi.Countdown("local:theirs"),
	} {
		if err := set(addonapi.OnPage, b); code(err) != addonapi.CodeBadRequest {
			t.Errorf("%s: %v", what, err)
		}
	}
	for what, b := range map[string]addonapi.Block{
		"a button":   addonapi.Row(addonapi.Text("x"), addonapi.Button("go", "Go", "")),
		"a list":     addonapi.List(addonapi.Section{Items: []addonapi.Block{addonapi.Text("x")}}),
		"two images": addonapi.Row(addonapi.Image("http://ha.local/a.png"), addonapi.Image("http://ha.local/b.png")),
	} {
		if err := set(addonapi.OnCard, b); err == nil {
			t.Errorf("a card of %s was taken", what)
		}
	}
	// A scene too big for a message is the message's limit.
	big := addonapi.Column()
	for range addonapi.MaxMessage/addonapi.MaxText + 1 {
		big.Children = append(big.Children, addonapi.Text(strings.Repeat("a", addonapi.MaxText)))
	}
	if err := set(addonapi.OnPage, big); err == nil {
		t.Error("a scene past a message's size was taken")
	}
	// A progress tied to the add-on's own timer is taken.
	tid, err := c.StartTimer(ctx(t), "rice", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := set(addonapi.OnCard, addonapi.Row(addonapi.Text("Rice"), addonapi.Countdown(tid))); err != nil {
		t.Errorf("a card tied to its own timer: %v", err)
	}
	if _, ok := r.s.Scene("meals", addonapi.OnPage); ok {
		t.Error("a refused scene was kept")
	}
}

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewGray(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// A sender faster than the screen draws has its frames merged: the display is told at most
// SceneUpdatesPerSecond times a second, and reads the latest.
func TestScenesAreMerged(t *testing.T) {
	r, k, c := screenRig(t)
	start := time.Now()
	const n = 30
	for i := range n {
		p := addonapi.Column(addonapi.Block{Type: addonapi.BlockText, ID: "step", Text: "Step " + string(rune('A'+i))})
		if err := c.SetScene(ctx(t), addonapi.OnPage, p); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(3 * sceneEvery)
	told := k.count("page meals")
	most := int(time.Since(start)/sceneEvery) + 1
	if told == 0 || told > most || told >= n {
		t.Errorf("the display was told %d times for %d scenes in %v (at most %d)", told, n, time.Since(start), most)
	}
	sc, _ := r.s.Scene("meals", addonapi.OnPage)
	if b, _ := sc.Root.Find("step"); b.Text != "Step "+string(rune('A'+n-1)) {
		t.Errorf("the display reads %q, not the latest", b.Text)
	}
}

// One add-on cannot touch another's scenes: whose a scene is comes from the socket, and so the uid.
func TestScenesAreTheAddonsOwn(t *testing.T) {
	r, _, meals := screenRig(t)
	g := everything(r.dir)
	g.ID = "plex"
	path, err := r.s.Open(g)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.s.Close("plex") })
	pctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	plex, err := addonapi.Dial(pctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer plex.Close()
	if err := meals.SetScene(ctx(t), addonapi.OnPage, page); err != nil {
		t.Fatal(err)
	}
	if err := plex.PatchScene(ctx(t), addonapi.OnPage, "step", addonapi.Text("mine now")); code(err) != addonapi.CodeBadRequest {
		t.Errorf("another add-on patched the page: %v", err)
	}
	if err := plex.ClearCard(ctx(t)); err != nil {
		t.Fatal(err)
	}
	if sc, _ := r.s.Scene("meals", addonapi.OnPage); sc.Root.Children[0].Text != "Step 1" {
		t.Error("the page changed")
	}
	if _, ok := r.s.Scene("plex", addonapi.OnPage); ok {
		t.Error("the other add-on has a page")
	}
	r.s.Shown("meals", addonapi.OnPage, true)
	if r.s.Touch("plex", addonapi.OnPage, "next") {
		t.Error("a tap on one add-on's button went to another")
	}
	if got := r.s.Screens(); !slices.Equal(got, []string{"meals", "plex"}) {
		t.Errorf("tiles %v", got)
	}
}

func nextScreen(t *testing.T, ch <-chan addonapi.ScreenEvent) addonapi.ScreenEvent {
	t.Helper()
	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatal("the connection ended")
		}
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("no screen event")
	}
	return addonapi.ScreenEvent{}
}

func TestScreenEvents(t *testing.T) {
	r, k, c := screenRig(t)
	ev := c.WatchScreen()
	if err := c.OpenScreen(ctx(t)); code(err) != addonapi.CodeBadRequest {
		t.Errorf("screen.open with no page: %v", err)
	}
	if err := c.SetScene(ctx(t), addonapi.OnPage, page); err != nil {
		t.Fatal(err)
	}
	// Nothing reaches the add-on of a page that is not in view.
	if r.s.Touch("meals", addonapi.OnPage, "next") || r.s.Swipe("meals", addonapi.SwipeLeft) || r.s.Back("meals") {
		t.Error("a page out of view was touched")
	}
	if err := c.OpenScreen(ctx(t)); err != nil {
		t.Fatal(err)
	}
	if k.count("open meals") != 1 {
		t.Error("the display was not asked to open the page")
	}
	r.s.Shown("meals", addonapi.OnPage, true)
	r.s.Shown("meals", addonapi.OnPage, true) // said once
	if e := nextScreen(t, ev); e.Event != addonapi.EventScreenState || e.State != (addonapi.ScreenStateEvent{On: addonapi.OnPage, Shown: true}) {
		t.Errorf("shown: %+v", e)
	}
	if !r.s.Touch("meals", addonapi.OnPage, "next") {
		t.Fatal("a tap on a button in view")
	}
	if e := nextScreen(t, ev); e.Event != addonapi.EventTouch || e.Touch != (addonapi.TouchEvent{ID: "next", On: addonapi.OnPage}) {
		t.Errorf("touch: %+v", e)
	}
	if r.s.Touch("meals", addonapi.OnPage, "step") || r.s.Touch("meals", addonapi.OnPage, "nothing") || r.s.Touch("meals", addonapi.OnCard, "next") {
		t.Error("a tap on what is not a button or chip in view was sent")
	}
	if !r.s.Swipe("meals", addonapi.SwipeLeft) || r.s.Swipe("meals", "up") {
		t.Error("swipes")
	}
	if e := nextScreen(t, ev); e.Event != addonapi.EventSwipe || e.Swipe.Direction != addonapi.SwipeLeft {
		t.Errorf("swipe: %+v", e)
	}
	if !r.s.Back("meals") {
		t.Error("back")
	}
	if e := nextScreen(t, ev); e.Event != addonapi.EventBack {
		t.Errorf("back: %+v", e)
	}
	if err := c.CloseScreen(ctx(t)); err != nil || k.count("close meals") != 1 {
		t.Errorf("screen.close: %v", err)
	}
	r.s.Shown("meals", addonapi.OnPage, false)
	if e := nextScreen(t, ev); e.State.Shown {
		t.Errorf("hidden: %+v", e)
	}

	// The card: shown, tapped, swiped away.
	if err := c.SetScene(ctx(t), addonapi.OnCard, addonapi.Row(addonapi.Text("Stir"), addonapi.Chip("timer", "4:00", "mdi:timer"))); err != nil {
		t.Fatal(err)
	}
	r.s.Shown("meals", addonapi.OnCard, true)
	nextScreen(t, ev)
	if !r.s.Touch("meals", addonapi.OnCard, "timer") {
		t.Error("a tap on the card's chip")
	}
	if e := nextScreen(t, ev); e.Touch.On != addonapi.OnCard {
		t.Errorf("card touch: %+v", e)
	}
	r.s.DismissCard("meals")
	if e := nextScreen(t, ev); e.State != (addonapi.ScreenStateEvent{On: addonapi.OnCard, Dismissed: true}) {
		t.Errorf("dismissed: %+v", e)
	}
	if _, ok := r.s.Scene("meals", addonapi.OnCard); ok {
		t.Error("a dismissed card stayed")
	}
}

func TestScreenOpenIsLimited(t *testing.T) {
	// Frozen before the connection is made, so its own rate is counted on the same clock.
	r := newRig(t, everything)
	r.freeze()
	r.s.SetScreen(&sink{})
	c := r.dial()
	if err := c.SetScene(ctx(t), addonapi.OnPage, page); err != nil {
		t.Fatal(err)
	}
	for i := range addonapi.ScreenOpenBurst {
		if err := c.OpenScreen(ctx(t)); err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
	}
	if err := c.OpenScreen(ctx(t)); code(err) != addonapi.CodeRateLimited {
		t.Errorf("an open past the burst: %v", err)
	}
	r.advance(addonapi.ScreenOpenEvery)
	if err := c.OpenScreen(ctx(t)); err != nil {
		t.Errorf("an open after the wait: %v", err)
	}
}

func TestCardClear(t *testing.T) {
	r, k, c := screenRig(t)
	if err := c.SetScene(ctx(t), addonapi.OnCard, addonapi.Text("Stir")); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the card", func() bool { return k.count("card meals") == 1 })
	if err := c.ClearCard(ctx(t)); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.s.Scene("meals", addonapi.OnCard); ok {
		t.Error("the card stayed")
	}
	eventually(t, "the card to go", func() bool { return k.count("card meals") == 2 })
}

// The page and the card go when the add-on stops, and when its last connection closes, and the
// display is told.
func TestScenesGoWithTheAddon(t *testing.T) {
	r, k, c := screenRig(t)
	if err := c.SetScene(ctx(t), addonapi.OnPage, page); err != nil {
		t.Fatal(err)
	}
	if err := c.SetScene(ctx(t), addonapi.OnCard, addonapi.Text("Stir")); err != nil {
		t.Fatal(err)
	}
	second := r.dial()
	c.Close()
	time.Sleep(50 * time.Millisecond)
	if _, ok := r.s.Scene("meals", addonapi.OnPage); !ok {
		t.Fatal("the page went while a connection stayed")
	}
	second.Close()
	eventually(t, "the page to go with the last connection", func() bool {
		_, page := r.s.Scene("meals", addonapi.OnPage)
		_, card := r.s.Scene("meals", addonapi.OnCard)
		return !page && !card
	})
	if k.count("close meals") != 1 {
		t.Error("the display was not told to close the page")
	}

	c = r.dial()
	if err := c.SetScene(ctx(t), addonapi.OnPage, page); err != nil {
		t.Fatal(err)
	}
	r.s.Close("meals")
	if _, ok := r.s.Scene("meals", addonapi.OnPage); ok {
		t.Error("the page outlived the add-on")
	}
	if k.count("close meals") != 2 || len(r.s.Screens()) != 0 {
		t.Errorf("after the add-on stopped: %v %v", k.calls, r.s.Screens())
	}
}

// Nothing of a scene is written anywhere: after every screen op, the only thing in the server's folder
// is the socket, and the server writes nowhere else (the device's log aside, which says nothing of a
// scene: TestScreenSaysNothing).
func TestScenesAreNotWritten(t *testing.T) {
	r, _, c := screenRig(t)
	for range 5 {
		_ = c.SetScene(ctx(t), addonapi.OnPage, page)
		_ = c.PatchScene(ctx(t), addonapi.OnPage, "step", addonapi.Text("again"))
		_ = c.SetScene(ctx(t), addonapi.OnCard, addonapi.Text("Stir"))
		_ = c.ClearCard(ctx(t))
		_ = c.SetScene(ctx(t), addonapi.OnPage, addonapi.Block{Type: "bad"})
	}
	var files []string
	_ = filepath.WalkDir(r.dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			files = append(files, p)
		}
		return nil
	})
	if len(files) != 1 || files[0] != r.path {
		t.Errorf("the server's folder holds %v", files)
	}
}

// Scenes, good or refused, say nothing in the device's log.
func TestScreenSaysNothing(t *testing.T) {
	r, _, c := screenRig(t)
	l := captureLog(t)
	for range 10 { // within the connection's burst, so no refusal for the rate is logged either
		_ = c.SetScene(ctx(t), addonapi.OnPage, page)
		_ = c.Call(ctx(t), addonapi.OpSceneSet, addonapi.SceneSet{On: addonapi.OnPage, Scene: addonapi.Block{Type: "bad"}}, nil)
		_ = c.PatchScene(ctx(t), addonapi.OnPage, "nothing", addonapi.Text("x"))
	}
	r.s.Shown("meals", addonapi.OnPage, true)
	r.s.Touch("meals", addonapi.OnPage, "next")
	if n := l.count("addonapi"); n != 0 {
		t.Errorf("the log has %d lines about scenes", n)
	}
}
