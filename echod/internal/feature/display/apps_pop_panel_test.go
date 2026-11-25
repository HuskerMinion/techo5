//go:build panel && !dot

package display

import (
	"image"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
	esphome "github.com/ygelfand/go-esphome-device"
)

// appsListFor is a panel the size of a Fire HD 8 with the apps list up over its idle clock, and the app
// having sent two apps; what the screen tells the app is returned.
func appsListFor(t *testing.T) (*Display, *[]string) {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Screen().Welcomed(true); err != nil {
		t.Fatal(err)
	}
	told := tabletAppsFor(t)
	appSaysApps([]string{"begin"})
	appSaysApps([]string{"item", "org.example.notes", "Notes"})
	appSaysApps([]string{"item", "org.example.cam", "Camera"})
	appSaysApps([]string{"end"})
	d := &Display{
		poke: make(chan struct{}, 1),
		view: voice.State{Phase: "idle"},
		light: &esphome.Light{
			Base:                esphome.Base{ObjectID: "screen", Name: "Screen", Icon: "mdi:monitor"},
			SupportedColorModes: []esphome.ColorMode{esphome.ColorModeBrightness},
		},
		on: true, ceiling: 60,
	}
	d.r = newRenderer(image.NewRGBA(image.Rect(0, 0, show8Wide, show8High)))
	d.appsPop, d.appsPopAt = true, time.Now()
	return d, told
}

// appsListScene is the idle clock in the style given, with the list up over it.
func appsListScene(style string) scene {
	at := time.Date(2026, 11, 23, 9, 41, 0, 0, time.Local)
	return scene{now: at, phase: "idle", appsPop: true, style: styleFacts{kind: style, chosen: true}}
}

// appZone is where the list drew the i'th app.
func appZone(t *testing.T, r *renderer, i int) image.Point {
	t.Helper()
	r.zmu.Lock()
	defer r.zmu.Unlock()
	for _, z := range r.zones {
		if z.kind == zoneOption && z.opt == i {
			return image.Pt((z.r.Min.X+z.r.Max.X)/2, (z.r.Min.Y+z.r.Max.Y)/2)
		}
	}
	t.Fatalf("the list drew no app %d", i)
	return image.Point{}
}

// Over every clock style, the Dashboard the one it was found on, a frame with the list up draws it and
// gives it the fingers, and a tap on an app opens that app rather than putting the list away and going
// to the clock under it.
func TestTheAppsListTakesItsTapOverEveryClockStyle(t *testing.T) {
	for _, cs := range clockStyles {
		t.Run(cs.label, func(t *testing.T) {
			d, told := appsListFor(t)
			d.r.draw(appsListScene(cs.value))
			if !d.r.appsDrawn.Load() {
				t.Fatal("the list was drawn over the clock and does not have the fingers")
			}
			at := appZone(t, d.r, 1)
			d.gesture(touch.Gesture{Kind: touch.Tap, X: at.X, Y: at.Y})
			if want := []string{"apps open org.example.cam"}; !slices.Equal(*told, want) {
				t.Errorf("the app was told %q; want %q", *told, want)
			}
			if d.appsPop {
				t.Error("the list is still up after a pick")
			}
		})
	}
}

// The list keeps its fingers while the next frame is being drawn: a finger coming down asks for a frame,
// and on a tablet the Dashboard style's takes longer to draw than a tap takes to lift. Before, each frame
// began by taking them away, so every tap found the list not drawn, put it away and opened it again.
func TestTheAppsListKeepsItsFingersWhileAFrameIsDrawn(t *testing.T) {
	d, _ := appsListFor(t)
	s := appsListScene(styleDashboard)
	d.r.draw(s)
	var done atomic.Bool
	drawn := make(chan struct{})
	go func() {
		defer close(drawn)
		defer done.Store(true)
		for range 20 {
			d.r.draw(s)
		}
	}()
	lost := false
	for !done.Load() && !lost {
		lost = !d.r.appsDrawn.Load()
	}
	<-drawn // the frames end before the test's cleanup does
	if lost {
		t.Fatal("while a frame with the list was drawn, the list on the screen had no fingers")
	}
}

// A page drawn in the list's place takes the fingers from it once that frame is done: the list is not
// on the screen, and a tap there is the page's.
func TestTheAppsListHasNoFingersUnderAPage(t *testing.T) {
	for name, s := range map[string]scene{
		"calendar": {showCalendar: true},
		"weather":  {showWeather: true},
	} {
		t.Run(name, func(t *testing.T) {
			d, _ := appsListFor(t)
			d.r.draw(appsListScene(styleDashboard))
			page := appsListScene(styleDashboard)
			page.showCalendar, page.showWeather = s.showCalendar, s.showWeather
			d.r.draw(page)
			if d.r.appsDrawn.Load() {
				t.Errorf("the %s page was drawn in the list's place and the list kept the fingers", name)
			}
		})
	}
}
