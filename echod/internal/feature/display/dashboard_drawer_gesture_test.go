//go:build !dot && !spot

package display

import (
	"image"
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
	esphome "github.com/ygelfand/go-esphome-device"
)

// With the drawer in over a dashboard, a finger is the drawer's, not the dashboard's: a swipe to the
// right closes the drawer and leaves the dashboard up.
func TestTheDrawerOverADashboardTakesTheFingers(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "config.json"))
	if err := config.Set().Screen().Welcomed(true); err != nil {
		t.Fatal(err)
	}
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
	d.dash, d.dashShowing, d.drawer = true, true, true

	d.gesture(touch.Gesture{Kind: touch.SwipeRight, X: 600, Y: 400})
	d.mu.Lock()
	drawer, dash := d.drawer, d.dashShowing
	d.mu.Unlock()
	if drawer {
		t.Fatal("a swipe to the right over the drawer did not close it: the dashboard took the finger")
	}
	if !dash {
		t.Fatal("closing the drawer took the dashboard away too")
	}
}
