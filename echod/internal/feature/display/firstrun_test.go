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
	"github.com/HuskerMinion/techo5/echod/internal/lib/fonts"
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

// The language step: a tap on a language makes it the screen's and picks its clock, the clock button
// turns it, and Next goes on with both as they are. An unset language shows English chosen.
func TestFirstRunLanguageStep(t *testing.T) {
	d, _ := firstRunDisplay(t, true, true)
	was := clock24.Load()
	t.Cleanup(func() { clock24.Store(was) })
	setClockFormat(nil, false, false)
	setStep(t, frLanguage)
	if v := d.firstRunView(time.Now()); v.lang != "en" {
		t.Fatalf("an unset language shows %q chosen", v.lang)
	}
	de := -1
	for i, l := range frLangs {
		if l.Code == "de" {
			de = i
		}
	}
	frTapIn(d, d.r.frLangRects()[de])
	if c := config.Get().Screen; c.Language != "de" || !c.Clock24 || !clock24.Load() {
		t.Fatalf("a tap on Deutsch: language %q, 24 hours %v, drawn %v", c.Language, c.Clock24, clock24.Load())
	}
	if v := d.firstRunView(time.Now()); v.lang != "de" {
		t.Fatalf("Deutsch tapped, %q shown chosen", v.lang)
	}
	tapButton(t, d, "clock")
	if config.Get().Screen.Clock24 || clock24.Load() {
		t.Fatal("the clock button left 24 hours")
	}
	tapButton(t, d, "next")
	if got, want := firstRunStep(), stepAfter(frLanguage); got != want {
		t.Fatalf("Next went to %q, want %q", got, want)
	}
	if c := config.Get().Screen; c.Language != "de" || c.Clock24 {
		t.Fatalf("Next changed the language step's choices: %q, %v", c.Language, c.Clock24)
	}
}

// A tap on the language already lit changes nothing: on Match all, English is lit, and tapping it to
// confirm keeps Match all and the clock chosen, rather than making the screen English-only at 12 hours.
func TestFirstRunTapOnTheLitLanguageKeepsMatchAll(t *testing.T) {
	d, _ := firstRunDisplay(t, true, true)
	was := clock24.Load()
	t.Cleanup(func() { clock24.Store(was) })
	if err := config.Set().Screen().Clock24(true); err != nil {
		t.Fatal(err)
	}
	setClockFormat(nil, true, false)
	setStep(t, frLanguage)
	v := d.firstRunView(time.Now())
	if v.lang != "en" || config.Get().Screen.Language != "" {
		t.Fatalf("want Match all shown as English, got %q (saved %q)", v.lang, config.Get().Screen.Language)
	}
	en := -1
	for i, l := range frLangs {
		if l.Code == "en" {
			en = i
		}
	}
	frTapIn(d, d.r.frLangRects()[en])
	if c := config.Get().Screen; c.Language != "" || !c.Clock24 || !clock24.Load() {
		t.Fatalf("a tap on the lit English: language %q, 24 hours %v, drawn %v", c.Language, c.Clock24, clock24.Load())
	}
}

// A 12-hour clock set to go without its AM/PM keeps that through the language step: a language
// picking its clock, and the clock button, change only 12 or 24 hours.
func TestFirstRunKeepsNoAMPM(t *testing.T) {
	d, _ := firstRunDisplay(t, true, true)
	was24, wasBare := clock24.Load(), noAMPM.Load()
	t.Cleanup(func() { clock24.Store(was24); noAMPM.Store(wasBare) })
	if err := config.Set().Screen().ClockFormat(false, true); err != nil {
		t.Fatal(err)
	}
	setClockFormat(nil, false, true)
	setStep(t, frLanguage)
	kept := func(what string, want24 bool) {
		t.Helper()
		if c := config.Get().Screen; c.Clock24 != want24 || !c.NoAMPM || clock24.Load() != want24 || !noAMPM.Load() {
			t.Fatalf("%s: 24 hours %v, no AM/PM %v (drawn %v, %v); want 24 hours %v and no AM/PM kept",
				what, c.Clock24, c.NoAMPM, clock24.Load(), noAMPM.Load(), want24)
		}
	}
	de := -1
	for i, l := range frLangs {
		if l.Code == "de" {
			de = i
		}
	}
	frTapIn(d, d.r.frLangRects()[de])
	kept("a tap on Deutsch", true)
	tapButton(t, d, "clock")
	kept("the clock button", false)
}

// Japanese is named in its own script once a font here draws it, and romanized until then, rather
// than as boxes: its font is fetched only once Japanese is chosen, and maybe not before the Wi-Fi.
func TestFirstRunNamesJapaneseItCanDraw(t *testing.T) {
	want := "日本語"
	if !fonts.OwnDraws('日', "") {
		want = "Nihongo"
	}
	labels := frLangLabels()
	for i, l := range frLangs {
		switch {
		case l.Code == "ja" && labels[i] != want:
			t.Errorf("Japanese is labeled %q, want %q", labels[i], want)
		case l.Code != "ja" && labels[i] != l.Name:
			t.Errorf("%s is labeled %q", l.Code, labels[i])
		}
	}
}

// Next on the language step saves nothing: a screen matching every language's words keeps doing so.
func TestFirstRunLanguageNextKeepsMatchAll(t *testing.T) {
	d, _ := firstRunDisplay(t, true, true)
	setStep(t, frLanguage)
	tapButton(t, d, "next")
	if c := config.Get().Screen; c.Language != "" {
		t.Fatalf("Next set the language to %q", c.Language)
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
		{frLanguage, true, true, []string{"clock", "next"}},
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

// Back walks the steps the other way, down to the first counted one, which has none; it is not there
// while the keyboard is up.
func TestFirstRunBack(t *testing.T) {
	d, _ := firstRunDisplay(t, true, true)
	steps := firstrun.Steps()
	setStep(t, frSound)
	for i := len(steps) - 3; i >= 1; i-- {
		frTapIn(d, d.r.frBackRect())
		if got := firstRunStep(); got != steps[i] {
			t.Fatalf("after Back: %q, want %q", got, steps[i])
		}
	}
	if frShowsBack(d.firstRunView(time.Now())) {
		t.Fatalf("the first step %q offers Back", firstRunStep())
	}
	frTapIn(d, d.r.frBackRect())
	if got := firstRunStep(); got != steps[1] {
		t.Fatalf("a tap where Back would be on the first step went to %q", got)
	}
	setStep(t, frName)
	d.firstRun.typing = "name"
	if frShowsBack(d.firstRunView(time.Now())) {
		t.Fatal("Back shows over the keyboard")
	}
	d.firstRun.typing = ""
	key := firstRunView{step: frHome, showKey: true, key: "a key", wifi: d.firstRun.wifi}
	if !frKeyView(key) || frShowsBack(key) {
		t.Fatalf("Back over the key: key view %v, Back %v", frKeyView(key), frShowsBack(key))
	}
	if key.showKey = false; !frShowsBack(key) {
		t.Fatal("no Back on the Home Assistant step with the key put away")
	}
}

// Back to the Home Assistant step after it went on by itself shows it connected, with Next, and does
// not go on again at once.
func TestFirstRunBackToHomeAssistantStays(t *testing.T) {
	d, f := firstRunDisplay(t, true, false)
	setStep(t, frHome)
	now := time.Now()
	d.firstRunView(now)
	f.get(func() { f.home = true })
	d.firstRunView(now.Add(time.Second))
	d.firstRunView(now.Add(time.Second + frHomeLinger))
	if got := firstRunStep(); got != stepAfter(frHome) {
		t.Fatalf("after it connected: %q", got)
	}
	d.firstRunView(now.Add(2*time.Second + frHomeLinger))
	frTapIn(d, d.r.frBackRect())
	v := d.firstRunView(now.Add(time.Minute))
	if got := firstRunStep(); got != frHome || v.homeJust {
		t.Fatalf("back on Home Assistant: step %q, just connected %v", got, v.homeJust)
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
			{"language", frLanguage, true, false, nil},
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
