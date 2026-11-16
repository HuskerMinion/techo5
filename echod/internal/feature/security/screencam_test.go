package security

import (
	"path/filepath"
	"slices"
	"testing"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The Screen camera is a switch of its own: off on a new device, and turning it or Screen web access
// on leaves the other where it was, so the picture can go to Home Assistant with the page closed.
func TestTheScreenCameraHasItsOwnSwitch(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	f := build()
	f.Restore(config.Get())

	if c := config.Get().Security; c.ScreenCamera || f.screenCam.Get() {
		t.Fatal("the Screen camera is on on a new device")
	}

	f.SetScreenCamera(true)
	if c := config.Get().Security; !c.ScreenCamera || c.Screen {
		t.Errorf("after switching the Screen camera on: screen_camera=%t screen_web=%t", c.ScreenCamera, c.Screen)
	}
	if st := f.State(); !st.ScreenCamera || st.Screen {
		t.Errorf("the Security tab sees screen_camera=%t screen_web=%t", st.ScreenCamera, st.Screen)
	}

	f.SetScreen(true)
	f.SetScreenCamera(false)
	if c := config.Get().Security; c.ScreenCamera || !c.Screen {
		t.Errorf("after switching the Screen camera off: screen_camera=%t screen_web=%t", c.ScreenCamera, c.Screen)
	}

	// It survives a restart the way the other switches do.
	f.SetScreenCamera(true)
	g := build()
	g.Restore(config.Get())
	if !g.screenCam.Get() {
		t.Error("the Screen camera was off after a restart")
	}
}

// The switch is offered wherever the screen page is, and not on a device with no screen.
func TestTheScreenCameraSwitchIsOfferedWithTheScreen(t *testing.T) {
	ids := []string{}
	for _, e := range build().Entities() {
		if s, ok := e.(*esphome.Switch); ok {
			ids = append(ids, s.ObjectID)
		}
	}
	if got := slices.Contains(ids, "screen_camera"); got != webPages {
		t.Errorf("screen_camera offered = %t on a device with screen pages = %t (%v)", got, webPages, ids)
	}
}
