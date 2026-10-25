//go:build !dot && !spot && !panel

package tessera

import (
	"path/filepath"
	"strings"
	"testing"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The editor parses the Screen layout sensor as "<w>x<h> <c>x<r>", then an optional density and look.
func TestScreenLayouts(t *testing.T) {
	for _, c := range []struct {
		show8, skylight bool
		want            string
	}{
		{false, false, "960x480 4x2 standard"},
		{true, false, "1280x800 4x3 standard"},
		{false, true, "1920x1080 5x3 standard"},
	} {
		if got := gridFor(c.show8, c.skylight).String(); got != c.want {
			t.Errorf("show8 %v skylight %v: %q, want %q", c.show8, c.skylight, got, c.want)
		}
	}
}

// useTemp keeps the settings and the layout in a test's own folder.
func useTemp(t *testing.T) {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	was := path
	path = filepath.Join(t.TempDir(), "tessera-layout.json")
	t.Cleanup(func() { path = was })
}

// ids is the object ids of entities.
func ids(ents []esphome.Entity) []string {
	var out []string
	for _, e := range ents {
		out = append(out, e.Object())
	}
	return out
}

// Off, there is only the switch: no inbox, so the add-on does not take the device for a screen, no action
// and no sub-device. On, the add-on finds everything it looks for, on the screen's own sub-device.
func TestEntitiesFollowTheSwitch(t *testing.T) {
	useTemp(t)
	tt := build()

	tt.Restore(config.Get())
	if got := ids(tt.Entities()); len(got) != 1 || got[0] != "tessera_screen" {
		t.Errorf("off: entities %v", got)
	}
	if len(tt.Actions()) != 0 {
		t.Errorf("off: %d actions", len(tt.Actions()))
	}
	for _, d := range component.OfferedDevices("Kitchen") {
		if d.ID == component.DeviceTessera {
			t.Error("off: the sub-device is offered")
		}
	}

	if err := config.Set().Tessera().On(true); err != nil {
		t.Fatal(err)
	}
	tt.Restore(config.Get())
	want := []string{"tessera_screen", "tessera_grid", "tessera_tile_settings", "tessera_screen_firmware", "tessera_device_name", "tessera_screen_layout"}
	got := ids(tt.Entities())
	if len(got) != len(want) {
		t.Fatalf("on: entities %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("on: entities %v, want %v", got, want)
		}
	}
	if tt.inbox.Name != "Tile settings" || tt.inbox.DeviceID != component.DeviceTessera || tt.inbox.Get() != StatusReady {
		t.Errorf("inbox %q on device %d says %q", tt.inbox.Name, tt.inbox.DeviceID, tt.inbox.Get())
	}
	if tt.firmware.Get() != firmwareClaim || tt.shape.Get() == "" {
		t.Errorf("firmware %q layout %q", tt.firmware.Get(), tt.shape.Get())
	}
	acts := tt.Actions()
	if len(acts) != 1 || acts[0].Name != "screen_message" || !acts[0].Answers || len(acts[0].Args) != 1 || acts[0].Args[0].Name != "message" {
		t.Fatalf("on: actions %+v", acts)
	}
	offered := false
	for _, d := range component.OfferedDevices("Kitchen") {
		if d.ID == component.DeviceTessera && d.Name == "Kitchen tessera" {
			offered = true
		}
	}
	if !offered {
		t.Error("on: the sub-device is not offered")
	}
}

// Turning it on saves it and restarts; the same again does neither.
func TestTheSwitchRestarts(t *testing.T) {
	useTemp(t)
	var restarts []string
	was := restart
	restart = func(why string) { restarts = append(restarts, why) }
	t.Cleanup(func() { restart = was })
	tt := build()
	tt.Restore(config.Get())

	tt.set(true)
	if !config.Get().Tessera.On || len(restarts) != 1 || !tt.on.Get() {
		t.Errorf("on: saved %v, %d restarts", config.Get().Tessera.On, len(restarts))
	}
	tt.set(true)
	if len(restarts) != 1 {
		t.Errorf("on again: %d restarts", len(restarts))
	}
}

// The sizes a hello names are every rectangle the grid has room for: the five names where they fit,
// then the spans past two by two, short of the whole page, which is "full".
func TestSizesFollowTheGrid(t *testing.T) {
	for _, c := range []struct {
		g    grid
		want string
	}{
		{grid{960, 480, 4, 2}, "single wide full tall square 3x1 3x2 4x1"},
		{grid{1280, 800, 4, 3}, "single wide full tall square 1x3 2x3 3x1 3x2 3x3 4x1 4x2"},
		{grid{1920, 1080, 5, 3}, "single wide full tall square 1x3 2x3 3x1 3x2 3x3 4x1 4x2 4x3 5x1 5x2"},
	} {
		if got := strings.Join(sizes(c.g), " "); got != c.want {
			t.Errorf("%v: %s, want %s", c.g, got, c.want)
		}
	}
	if caps := capabilities(gridFor(false, false)); strings.Join(caps.TileSizes, " ") != "single wide full tall square 3x1 3x2 4x1" {
		t.Errorf("the Show 5 says it takes %v", caps.TileSizes)
	}
}
