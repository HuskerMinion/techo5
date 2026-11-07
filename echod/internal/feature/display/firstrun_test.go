//go:build !dot && !spot

package display

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/firstrun"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
)

// firstRunDisplay is a Display on a fresh config with first run still to do, and first run's view of
// the device made up: a network joined or not, Home Assistant connected or not.
func firstRunDisplay(t *testing.T, joined, home bool) (*Display, *frFake) {
	t.Helper()
	d := updateDisplay(t)
	d.r = newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	f, st := stubFirstRun(t, joined, home)
	// The status as the frame's read would leave it, without waiting on its goroutine.
	d.firstRun.wifi, d.firstRun.wifiAt = st, time.Now()
	return d, f
}

func setStep(t *testing.T, step string) {
	t.Helper()
	if err := config.Set().FirstRun().Step(step); err != nil {
		t.Fatal(err)
	}
}

// tapButton taps the step's button that does act.
func tapButton(t *testing.T, d *Display, act string) {
	t.Helper()
	v := d.firstRunView(time.Now())
	buttons := firstRunButtons(v)
	for i, b := range d.r.frButtonRects(buttons) {
		if buttons[i].act == act {
			frTapIn(d, b)
			return
		}
	}
	t.Fatalf("step %q has no %q button: %+v", v.step, act, buttons)
}

func frTapIn(d *Display, b image.Rectangle) {
	c := b.Min.Add(b.Size().Div(2))
	d.gesture(touch.Gesture{Kind: touch.Tap, X: c.X, Y: c.Y})
}

// waitFor waits a moment for what a goroutine does.
func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatal("it did not happen")
}

// A device installed the usual way, on a network and in Home Assistant, gets through in a tap a step,
// in order, and is welcomed at the end.
func TestFirstRunGoesThroughItsStepsInOrder(t *testing.T) {
	d, _ := firstRunDisplay(t, true, true)
	tap := func() { d.gesture(touch.Gesture{Kind: touch.Tap, X: 10, Y: 10}) }

	if !d.firstRunActive() || firstRunStep() != frHello {
		t.Fatalf("a fresh device starts at %q, active %v", firstRunStep(), d.firstRunActive())
	}
	tap() // anywhere on the hello card
	steps := firstrun.Steps()
	for _, want := range steps[1 : len(steps)-1] {
		if got := firstRunStep(); got != want {
			t.Fatalf("step %q, want %q", got, want)
		}
		tapButton(t, d, "next")
	}
	if got := firstRunStep(); got != frDone {
		t.Fatalf("after the sound check: %q, want done", got)
	}
	tap()
	if config.Get().FirstRunPending() || config.Get().FirstRun.Step != "" || d.firstRunActive() {
		t.Fatalf("finishing left welcomed %v, step %q", config.Get().Screen.Welcomed, config.Get().FirstRun.Step)
	}
}

// The step is saved as it is done, so a restart picks up there; an unknown one starts again.
func TestFirstRunResumesWhereItStopped(t *testing.T) {
	firstRunDisplay(t, true, true)
	setStep(t, frHome)
	if got := firstRunStep(); got != frHome {
		t.Fatalf("resumed at %q, want home", got)
	}
	setStep(t, "gone")
	if got := firstRunStep(); got != frHello {
		t.Fatalf("an unknown step resumed at %q, want the start", got)
	}
}

// What each step offers depends on what is done already.
func TestFirstRunStepsThatAreDoneOfferNext(t *testing.T) {
	cases := []struct {
		step         string
		joined, home bool
		want         []string
	}{
		{frWifi, true, true, []string{"wifi", "next"}},
		{frWifi, false, false, []string{"next", "wifi"}}, // "Not now", then Choose Wi-Fi
		{frZone, true, true, []string{"zone", "next"}},
		{frName, true, true, []string{"rename", "next"}},
		{frHome, true, true, []string{"next"}},
		{frHome, true, false, []string{"next", "key", "adopt"}}, // on its own, type the key, add it
		{frHome, false, false, []string{"next"}},
		{frUpdates, true, true, []string{"auto", "next"}},
		{frSound, true, true, []string{"again", "next"}},
	}
	for _, c := range cases {
		if !hasFirstRunStep(c.step) {
			continue // a Panel's first run has no Wi-Fi or Updates step
		}
		d, _ := firstRunDisplay(t, c.joined, c.home)
		setStep(t, c.step)
		var got []string
		for _, b := range firstRunButtons(d.firstRunView(time.Now())) {
			got = append(got, b.act)
		}
		if len(got) != len(c.want) {
			t.Errorf("%s joined=%v home=%v: %v, want %v", c.step, c.joined, c.home, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s joined=%v home=%v: %v, want %v", c.step, c.joined, c.home, got, c.want)
			}
		}
	}
}

// The key is read on the Home Assistant step while nothing is connected, in first run only.
func TestFirstRunShowsTheKeyOnlyInFirstRun(t *testing.T) {
	d, _ := firstRunDisplay(t, true, false)
	setStep(t, frHome)
	if v := d.firstRunView(time.Now()); v.key == "" {
		t.Error("the Home Assistant step did not read the key")
	}
	setStep(t, frZone)
	if v := d.firstRunView(time.Now()); v.key != "" {
		t.Error("the key was read on another step")
	}
	setStep(t, frHome)
	if err := config.Set().Screen().Welcomed(true); err != nil {
		t.Fatal(err)
	}
	if v := d.firstRunView(time.Now()); v.key != "" {
		t.Error("the key was read after first run")
	}
}

// Home Assistant connecting while its step waits is said, and first run goes on by itself.
func TestFirstRunGoesOnOnceHomeAssistantConnects(t *testing.T) {
	d, f := firstRunDisplay(t, true, false)
	setStep(t, frHome)
	now := time.Now()
	d.firstRunView(now)
	f.get(func() { f.home = true })
	if v := d.firstRunView(now.Add(time.Second)); !v.homeJust || firstRunStep() != frHome {
		t.Fatalf("connecting: just %v, step %q", v.homeJust, firstRunStep())
	}
	d.firstRunView(now.Add(time.Second + frHomeLinger))
	if got := firstRunStep(); got != stepAfter(frHome) {
		t.Fatalf("after it connected: %q, want %q", got, stepAfter(frHome))
	}
}

// Add it opens the window for a Home Assistant to add the device, with nothing typed; Type the key
// shows the key; the window shown leaves only "Skip for now".
func TestFirstRunHomeAssistantPaths(t *testing.T) {
	d, f := firstRunDisplay(t, true, false)
	setStep(t, frHome)
	tapButton(t, d, "key")
	if v := d.firstRunView(time.Now()); !v.showKey {
		t.Fatal("Type the key did not show the key")
	}
	tapButton(t, d, "adopt")
	var opened int
	f.get(func() { opened = f.opened })
	v := d.firstRunView(time.Now())
	if opened != 1 || v.adopt.IsZero() || v.showKey {
		t.Fatalf("Add it: opened %d, window %v, key shown %v", opened, v.adopt, v.showKey)
	}
	if b := firstRunButtons(v); len(b) != 1 || b[0].act != "next" {
		t.Fatalf("with the window open: %+v", b)
	}
}

// The name step: a room tapped is the suggested area; Other and Change name type on the keyboard.
func TestFirstRunNameAndRoom(t *testing.T) {
	d, f := firstRunDisplay(t, true, false)
	setStep(t, frName)
	frTapIn(d, d.r.frRoomRects()[0])
	if got := config.Get().Home.Area; got != frRooms[0] {
		t.Fatalf("the room tapped: area %q, want %q", got, frRooms[0])
	}
	frTapIn(d, d.r.frRoomRects()[len(frRooms)-1]) // Other
	if v := d.firstRunView(time.Now()); v.typing != "room" {
		t.Fatalf("Other: typing %q", v.typing)
	}
	d.mu.Lock()
	d.firstRun.text = "Sunroom"
	d.mu.Unlock()
	d.firstRunKey("join")
	if got := config.Get().Home.Area; got != "Sunroom" {
		t.Fatalf("a typed room: %q", got)
	}
	tapButton(t, d, "rename")
	d.mu.Lock()
	d.firstRun.text = "Hall Show"
	d.mu.Unlock()
	d.firstRunKey("join")
	var renamed string
	f.get(func() { renamed = f.renamed })
	v := d.firstRunView(time.Now())
	if renamed != "Hall Show" || !v.renaming || firstRunStep() != frName {
		t.Fatalf("renaming: %q, renaming %v, step %q", renamed, v.renaming, firstRunStep())
	}
}

// Arriving at updates turns automatic installs on and checks once; the button leaves them off.
func TestFirstRunUpdates(t *testing.T) {
	if !hasFirstRunStep(frUpdates) {
		t.Skip("a Panel's app updates it: no Updates step")
	}
	d, f := firstRunDisplay(t, true, true)
	f.get(func() { f.offered = "v9.9.9" })
	setStep(t, frUpdates)
	d.firstRunView(time.Now())
	d.firstRunView(time.Now())
	waitFor(t, firstrun.UpdatesChecked)
	var checks int
	var auto bool
	f.get(func() { checks, auto = f.checks, f.auto })
	if checks != 1 || !auto {
		t.Fatalf("arriving: %d checks, automatic %v", checks, auto)
	}
	if v := d.firstRunView(time.Now()); v.offered != "v9.9.9" || !v.checked {
		t.Fatalf("view: %+v", v)
	}
	tapButton(t, d, "auto")
	f.get(func() { auto = f.auto })
	if auto {
		t.Fatal("Leave them off left them on")
	}
}

// The sound plays once on arriving at the check, and again on Play again.
func TestFirstRunSoundCheckPlaysOnArriving(t *testing.T) {
	d, f := firstRunDisplay(t, true, true)
	setStep(t, frSound)
	d.firstRunView(time.Now())
	d.firstRunView(time.Now())
	chimes := func() (n int) { f.get(func() { n = f.chimes }); return }
	waitFor(t, func() bool { return chimes() == 1 })
	tapButton(t, d, "again")
	waitFor(t, func() bool { return chimes() == 2 })
}

// Choose Wi-Fi and Change (time zone) open the pages first run steps aside for, and it is back once
// they close.
func TestFirstRunStepsAsideForWifiAndTheZoneList(t *testing.T) {
	if !hasFirstRunStep(frWifi) {
		t.Skip("a Panel's tablet has the Wi-Fi: no Wi-Fi step")
	}
	d, _ := firstRunDisplay(t, false, false)
	setStep(t, frWifi)
	tapButton(t, d, "wifi")
	if d.firstRunActive() {
		t.Fatal("first run still had the screen over the Wi-Fi page")
	}
	d.closeWifi()
	if !d.firstRunActive() || firstRunStep() != frWifi {
		t.Fatal("first run did not come back at Wi-Fi")
	}
	setStep(t, frZone)
	tapButton(t, d, "zone")
	d.mu.Lock()
	sheet, picker := d.sheet, d.picker
	d.mu.Unlock()
	if !sheet || picker != "timezone" || d.firstRunActive() {
		t.Fatalf("Change: sheet %v, picker %q", sheet, picker)
	}
}

// Every step at every panel size; with SHOW_PREVIEW set, each is written there to look at.
func TestFirstRunDrawsAtEverySize(t *testing.T) {
	previewFont(t)
	dir := os.Getenv("SHOW_PREVIEW")
	at := time.Date(2026, 11, 7, 9, 30, 0, 0, time.UTC)
	for _, panel := range []struct {
		name       string
		wide, high int
	}{
		{"", showWide, showHigh},
		{"-show8", show8Wide, show8High},
		{"-skylight", skylightWide, skylightHigh},
	} {
		for _, c := range []struct {
			name         string
			step         string
			joined, home bool
			set          func(d *Display, f *frFake)
		}{
			{"hello", frHello, true, false, nil},
			{"wifi-none", frWifi, false, false, nil},
			{"wifi", frWifi, true, false, nil},
			{"zone", frZone, true, false, nil},
			{"name", frName, true, false, func(d *Display, f *frFake) { _ = config.Set().Home().Area("Kitchen") }},
			{"name-typing", frName, true, false, func(d *Display, f *frFake) {
				d.firstRun.typing, d.firstRun.text = "name", "Kitchen Show"
			}},
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
			d, f := firstRunDisplay(t, c.joined, c.home)
			setStep(t, c.step)
			if c.set != nil {
				c.set(d, f)
			}
			img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
			r := newRenderer(img)
			r.firstRun(scene{now: at}, d.firstRunView(at))
			if dir == "" {
				continue
			}
			out, err := os.Create(filepath.Join(dir, "firstrun-"+c.name+panel.name+".png"))
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
}

// hasFirstRunStep is whether this build's first run has step s.
func hasFirstRunStep(s string) bool {
	for _, k := range firstrun.Steps() {
		if k == s {
			return true
		}
	}
	return false
}
