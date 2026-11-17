package config

import (
	"strings"
	"testing"
)

// /screen.png's key is this device's secret: a settings file neither carries it to another device nor
// replaces the one this device has, so a restore cannot open the page to a key somebody else knows.
func TestTheScreenKeyStaysOnTheDevice(t *testing.T) {
	from := Defaults()
	from.Security.Screen, from.Security.ScreenKey = true, "the-file-s-screen-key"
	file, err := Export(from, "m", "v0")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(file), "the-file-s-screen-key") {
		t.Error("the screen page's key travels in a settings file")
	}

	here := Defaults()
	here.Security.Screen, here.Security.ScreenKey = true, "mine"
	r, err := Restored(here, file, "m")
	if err != nil {
		t.Fatal(err)
	}
	if r.Config.Security.ScreenKey != "mine" {
		t.Errorf("a restore changed this device's screen key to %q", r.Config.Security.ScreenKey)
	}
}
