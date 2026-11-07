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

// The Spot goes through the same steps, a tap a step, and is done at the end.
func TestSpotFirstRunGoesThrough(t *testing.T) {
	d, _ := spotFirstRun(t, true, true)
	if !d.firstRunActive() {
		t.Fatal("first run is not up on a fresh Spot")
	}
	d.gesture(touch.Gesture{Kind: touch.Tap, X: center, Y: center})
	for _, want := range []string{frWifi, frZone, frName, frHome, frUpdates, frSound} {
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
