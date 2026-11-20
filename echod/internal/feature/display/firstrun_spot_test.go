//go:build spot

package display

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/firstrun"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
)

func spotFirstRun(t *testing.T, joined, home bool) (*Display, *frFake) {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "config.json"))
	d := &Display{
		r: newRoundRenderer(image.NewRGBA(image.Rect(0, 0, side, side))), on: true, poke: make(chan struct{}, 1),
		view: voice.State{Phase: "idle"},
		light: &esphome.Light{
			Base:                esphome.Base{ObjectID: "screen", Name: "Screen"},
			SupportedColorModes: []esphome.ColorMode{esphome.ColorModeBrightness},
		},
	}
	f, st := stubFirstRun(t, joined, home)
	d.firstRun.wifi, d.firstRun.wifiAt = st, time.Now()
	return d, f
}

func spotSetStep(t *testing.T, s string) {
	t.Helper()
	if err := config.Set().FirstRun().Step(s); err != nil {
		t.Fatal(err)
	}
}

func spotFRTap(d *Display, act string) bool {
	v := d.firstRunView(time.Now())
	buttons := spotFirstRunButtons(v)
	for i, b := range spotFirstRunRects(len(buttons)) {
		if buttons[i].act == act {
			c := b.Min.Add(b.Size().Div(2))
			d.gesture(touch.Gesture{Kind: touch.Tap, X: c.X, Y: c.Y})
			return true
		}
	}
	return false
}

// The Spot's language step: the language button goes round the languages without saving them (so a
// language passed on the way fetches no font), the clock follows each language until the clock button
// picks one, and Next saves what was picked.
func TestSpotFirstRunLanguage(t *testing.T) {
	d, _ := spotFirstRun(t, true, true)
	was := clock24.Load()
	t.Cleanup(func() { clock24.Store(was) })
	setClockFormat(nil, false, false)
	spotSetStep(t, frLanguage)
	before := config.Get().Screen
	unsaved := func(what string) {
		t.Helper()
		if c := config.Get().Screen; c.Language != before.Language || c.Clock24 != before.Clock24 || clock24.Load() {
			t.Fatalf("%s saved: language %q, 24 hours %v", what, c.Language, c.Clock24)
		}
	}
	tap := func(act string) firstRunView {
		t.Helper()
		if !spotFRTap(d, act) {
			t.Fatalf("no %q button", act)
		}
		return d.firstRunView(time.Now())
	}
	for _, want := range []string{frLangs[1].Code, frLangs[2].Code} {
		if v := tap("lang"); v.lang != want || !v.clock24 {
			t.Fatalf("picked %q, 24 hours %v; want %q, 24 hours", v.lang, v.clock24, want)
		}
		unsaved("a language passed")
	}
	if v := tap("clock"); v.lang != frLangs[2].Code || v.clock24 {
		t.Fatalf("the clock button left %q at 24 hours %v", v.lang, v.clock24)
	}
	unsaved("the clock")
	if v := tap("lang"); v.lang != frLangs[3].Code || v.clock24 {
		t.Fatalf("the next language %q took its clock (24 hours %v), not the one chosen", v.lang, v.clock24)
	}
	unsaved("a language passed")
	tap("next")
	if step := firstRunStep(); step != stepAfter(frLanguage) {
		t.Fatalf("Next went to %q", step)
	}
	if c := config.Get().Screen; c.Language != frLangs[3].Code || c.Clock24 || clock24.Load() {
		t.Fatalf("Next saved language %q, 24 hours %v; want %q, 12 hours", c.Language, c.Clock24, frLangs[3].Code)
	}
}

// Next on the Spot's language step with nothing picked saves nothing: a screen on Match all keeps it.
func TestSpotFirstRunLanguageNextKeepsMatchAll(t *testing.T) {
	d, _ := spotFirstRun(t, true, true)
	spotSetStep(t, frLanguage)
	before := config.Get().Screen
	if !spotFRTap(d, "next") || firstRunStep() != stepAfter(frLanguage) {
		t.Fatalf("Next went to %q", firstRunStep())
	}
	if c := config.Get().Screen; c.Language != before.Language || c.Clock24 != before.Clock24 {
		t.Fatalf("Next saved language %q, 24 hours %v", c.Language, c.Clock24)
	}
}

// The Spot goes through the same steps, a tap a step, and is done at the end.
func TestSpotFirstRunGoesThrough(t *testing.T) {
	d, _ := spotFirstRun(t, true, true)
	if !d.firstRunActive() {
		t.Fatal("first run is not up on a fresh Spot")
	}
	d.gesture(touch.Gesture{Kind: touch.Tap, X: center, Y: center})
	for _, want := range []string{frLanguage, frWifi, frZone, frName, frHome, frUpdates, frSound} {
		if got := firstRunStep(); got != want {
			t.Fatalf("step %q, want %q", got, want)
		}
		if !spotFRTap(d, "next") {
			t.Fatalf("no Next on %q", want)
		}
	}
	d.gesture(touch.Gesture{Kind: touch.Tap, X: center, Y: center})
	if firstrun.Pending() {
		t.Fatal("first run still pending after Done")
	}
}

// Back, over the step's count, walks the steps the other way, down to the first counted one, which
// has none; it is not there while the key is up.
func TestSpotFirstRunBack(t *testing.T) {
	d, _ := spotFirstRun(t, true, true)
	back := func() {
		c := spotBackRect.Min.Add(spotBackRect.Size().Div(2))
		d.gesture(touch.Gesture{Kind: touch.Tap, X: c.X, Y: c.Y})
	}
	spotSetStep(t, frSound)
	for _, want := range []string{frUpdates, frHome, frName, frZone, frWifi, frLanguage, frLanguage} {
		back()
		if got := firstRunStep(); got != want {
			t.Fatalf("after Back: %q, want %q", got, want)
		}
	}
	if spotShowsBack(firstRunView{step: frHome, showKey: true, key: "a key"}) {
		t.Fatal("Back shows over the key")
	}
}

// Back from Wi-Fi to the language step after the setup page's first run left it shows what was saved
// there, not a pick the Spot made before.
func TestSpotFirstRunBackShowsNoStalePick(t *testing.T) {
	d, _ := spotFirstRun(t, true, true)
	was := clock24.Load()
	t.Cleanup(func() { clock24.Store(was) })
	setClockFormat(nil, false, false)
	spotSetStep(t, frLanguage)
	if !spotFRTap(d, "lang") {
		t.Fatal("no language button")
	}
	phone := "fr"
	if d.firstRunView(time.Now()).lang == phone {
		phone = "it"
	}
	if err := firstrun.SaveLanguage(phone, false); err != nil {
		t.Fatal(err)
	}
	spotSetStep(t, frWifi)
	d.firstRunView(time.Now())
	c := spotBackRect.Min.Add(spotBackRect.Size().Div(2))
	d.gesture(touch.Gesture{Kind: touch.Tap, X: c.X, Y: c.Y})
	if got := firstRunStep(); got != frLanguage {
		t.Fatalf("Back from Wi-Fi went to %q", got)
	}
	if v := d.firstRunView(time.Now()); v.lang != phone || v.clock24 {
		t.Fatalf("after Back: %q, 24 hours %v; want the saved %q, 12 hours", v.lang, v.clock24, phone)
	}
}

// The room button goes round the rooms; Add it opens the window; Show the key shows it, and a tap
// puts it away.
func TestSpotFirstRunRoomAndHome(t *testing.T) {
	d, f := spotFirstRun(t, true, false)
	spotSetStep(t, frName)
	spotFRTap(d, "room")
	spotFRTap(d, "room")
	if got := config.Get().Home.Area; got != frRooms[1] {
		t.Fatalf("two taps on the room: %q", got)
	}
	spotSetStep(t, frHome)
	spotFRTap(d, "key")
	if v := d.firstRunView(time.Now()); !v.showKey {
		t.Fatal("Show the key did not")
	}
	d.gesture(touch.Gesture{Kind: touch.Tap, X: center, Y: center})
	if v := d.firstRunView(time.Now()); v.showKey {
		t.Fatal("a tap did not put the key away")
	}
	spotFRTap(d, "adopt")
	var opened int
	f.get(func() { opened = f.opened })
	if opened != 1 {
		t.Fatalf("Add it opened the window %d times", opened)
	}
}

// Every step on the round panel; with SPOT_PREVIEW set, each is written there to look at.
func TestSpotFirstRunDraws(t *testing.T) {
	previewFont(t)
	dir := os.Getenv("SPOT_PREVIEW")
	at := time.Date(2026, 11, 7, 9, 30, 0, 0, time.UTC)
	for _, c := range []struct {
		name         string
		step         string
		joined, home bool
		set          func(d *Display, f *frFake)
	}{
		{"hello", frHello, true, false, nil},
		{"language", frLanguage, true, false, nil},
		{"wifi-none", frWifi, false, false, nil},
		{"wifi", frWifi, true, false, nil},
		{"zone", frZone, true, false, func(d *Display, f *frFake) { d.firstRun.phone = true }},
		{"name", frName, true, false, func(d *Display, f *frFake) { _ = config.Set().Home().Area("Kitchen") }},
		{"home", frHome, true, false, nil},
		{"home-key", frHome, true, false, func(d *Display, f *frFake) { d.firstRun.showKey = true }},
		{"home-adopt", frHome, true, false, func(d *Display, f *frFake) { f.adopt = at.Add(15 * time.Minute) }},
		{"home-connected", frHome, true, true, nil},
		{"updates", frUpdates, true, true, func(d *Display, f *frFake) {
			f.offered = "v1.7.0"
			firstrun.ArriveAtUpdates(nil)
			for !firstrun.UpdatesChecked() {
				time.Sleep(time.Millisecond)
			}
		}},
		{"sound", frSound, true, true, nil},
		{"done", frDone, true, true, nil},
	} {
		d, f := spotFirstRun(t, c.joined, c.home)
		spotSetStep(t, c.step)
		if c.set != nil {
			c.set(d, f)
		}
		img := image.NewRGBA(image.Rect(0, 0, side, side))
		newRoundRenderer(img).draw(roundScene{now: at, phase: "idle", firstRunUp: true, firstRun: d.firstRunView(at)})
		if dir == "" {
			continue
		}
		out, err := os.Create(filepath.Join(dir, "spot-firstrun-"+c.name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(out, img); err != nil {
			t.Fatal(err)
		}
		if err := out.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// A pick on the Spot is let go when the language step is left some other way, as the setup page's
// first run does: back on the step, it shows what was saved there, and Next does not put the pick back.
func TestSpotFirstRunPickGoesWhenTheStepIsLeft(t *testing.T) {
	d, _ := spotFirstRun(t, true, true)
	was := clock24.Load()
	t.Cleanup(func() { clock24.Store(was) })
	setClockFormat(nil, false, false)
	spotSetStep(t, frLanguage)
	if !spotFRTap(d, "lang") {
		t.Fatal("no language button")
	}
	picked := d.firstRunView(time.Now()).lang
	// The phone chooses another language, keeping 12 hours, and goes on.
	phone := "fr"
	if picked == phone {
		phone = "it"
	}
	if err := firstrun.SaveLanguage(phone, false); err != nil {
		t.Fatal(err)
	}
	spotSetStep(t, stepAfter(frLanguage))
	d.firstRunView(time.Now())
	// Back on the language step.
	spotSetStep(t, frLanguage)
	if v := d.firstRunView(time.Now()); v.lang != phone || v.clock24 {
		t.Fatalf("back on the step: %q, 24 hours %v; want the phone's %q, 12 hours", v.lang, v.clock24, phone)
	}
	if !spotFRTap(d, "next") {
		t.Fatal("no Next button")
	}
	if c := config.Get().Screen; c.Language != phone || c.Clock24 {
		t.Fatalf("Next saved %q, 24 hours %v; want the phone's %q, 12 hours", c.Language, c.Clock24, phone)
	}
}
