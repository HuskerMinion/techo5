//go:build !dot && !spot

package display

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// A settings file from before there were modes wears its theme as it did: no mode is Dark, and Dark is
// Theme, whatever the room.
func TestNoModeIsTheThemeAsBefore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte(`{"screen":{"on":true,"brightness":60,"theme":"Ocean"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	config.Use(path)
	room.forget()
	t.Cleanup(room.forget)
	room.step(5000, time.Now()) // a bright room changes nothing without Auto
	if got := current().name; got != "Ocean" {
		t.Errorf("an old file wears %s, not its Ocean", got)
	}
	if themeModeIndex() != 0 || lightShown(config.Get().Screen) {
		t.Error("an old file is not in Dark mode")
	}
}

// Light wears the light theme, Paper until one is chosen; the screen's own choices then set the light
// theme and leave the dark one alone.
func TestLightModeWearsTheLightTheme(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	_ = config.Set().Screen().Theme("Ocean")
	_ = config.Set().Screen().ThemeMode(config.ThemeModeLight)
	if got := current().name; got != config.DefaultLightTheme {
		t.Errorf("Light mode wears %s, not %s", got, config.DefaultLightTheme)
	}
	(&Display{}).SetTheme("Linen")
	sc := config.Get().Screen
	if sc.LightTheme != "Linen" || sc.Theme != "Ocean" || current().name != "Linen" {
		t.Errorf("choosing in Light mode left light %q, dark %q", sc.LightTheme, sc.Theme)
	}
	setRole(roleAccent, rgb(0x123456))
	sc = config.Get().Screen
	if sc.LightTheme != customName || sc.Theme != "Ocean" || current().colors[roleAccent] != rgb(0x123456) {
		t.Errorf("a color changed in Light mode left light %q, dark %q", sc.LightTheme, sc.Theme)
	}
	if sc.Palette != (config.Palette{}) || sc.LightPalette.Accent != "#123456" {
		t.Errorf("a color changed in Light mode went to the dark palette %+v, light %+v", sc.Palette, sc.LightPalette)
	}
	_ = config.Set().Screen().ThemeMode("")
	if current().name != "Ocean" {
		t.Error("back in Dark mode the dark theme is not worn")
	}
}

// Each mode keeps a Custom of its own: one saved for either leaves the other's as it was, and a file
// from before there were modes, with the one palette, still wears it as the dark theme.
func TestEachModeKeepsItsOwnCustom(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	old := `{"screen":{"on":true,"theme":"Custom","palette":{"ground":"#101010","accent":"#ff0000","text":"#eeeeee","dim":"#888888","rules":"#303030"}}}`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	config.Use(path)
	room.forget()
	if c := current(); c.name != customName || c.colors[roleAccent] != rgb(0xff0000) {
		t.Fatalf("an old file's Custom is worn as %s, accent %v", c.name, c.colors[roleAccent])
	}
	if _, ok := savedCustom(true); ok || modeThemeName(true) != config.DefaultLightTheme {
		t.Error("the dark mode's palette is offered as the light mode's")
	}

	light := config.Palette{Ground: "#f0f0f0", Accent: "#0000ff", Text: "#101010", Dim: "#707070", Rules: "#d0d0d0"}
	_ = config.Set().Screen().CustomFor(true, light)
	_ = config.Set().Screen().ThemeMode(config.ThemeModeLight)
	if c := current(); c.colors[roleAccent] != rgb(0x0000ff) {
		t.Errorf("Light mode wears accent %v, not its own palette's", c.colors[roleAccent])
	}
	_ = config.Set().Screen().ThemeMode("")
	if c := current(); c.colors[roleAccent] != rgb(0xff0000) {
		t.Errorf("saving the light palette changed the dark one: accent %v", c.colors[roleAccent])
	}

	dark := light
	dark.Accent = "#00ff00"
	_ = config.Set().Screen().CustomFor(false, dark)
	if sc := config.Get().Screen; sc.LightPalette != light || sc.Palette != dark {
		t.Errorf("saving the dark palette changed the light one: %+v", sc.LightPalette)
	}
}

// Theme is the dark mode's theme and Light theme the light mode's, whatever is worn: a choice in Home
// Assistant sets its own mode's theme in any mode, and a change of mode or of the room changes neither
// select, so nothing they are linked to is told of one.
func TestThemeSelectsHoldTheirOwnMode(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	room.forget()
	t.Cleanup(room.forget)
	_ = config.Set().Screen().Themes("Ocean", "Linen", config.ThemeModeLight)
	d := &Display{themeSel: themeSelect(false, func() {}), lightThemeSel: themeSelect(true, func() {})}
	d.syncThemeSelects()
	if d.themeSel.Get() != "Ocean" || d.lightThemeSel.Get() != "Linen" {
		t.Fatalf("the selects show %q and %q", d.themeSel.Get(), d.lightThemeSel.Get())
	}

	d.themeSel.OnCommand("Forest") // in Light mode, still the dark theme
	if sc := config.Get().Screen; sc.Theme != "Forest" || sc.LightTheme != "Linen" {
		t.Errorf("Theme chosen in Light mode saved dark %q, light %q", sc.Theme, sc.LightTheme)
	}
	d.lightThemeSel.OnCommand("Paper")
	d.lightThemeSel.OnCommand(customName) // none saved for the light mode: refused
	if sc := config.Get().Screen; sc.LightTheme != "Paper" || sc.Theme != "Forest" || d.lightThemeSel.Get() != "Paper" {
		t.Errorf("Light theme saved light %q, dark %q, shows %q", sc.LightTheme, sc.Theme, d.lightThemeSel.Get())
	}

	for _, mode := range []string{"", config.ThemeModeAuto, config.ThemeModeLight} {
		_ = config.Set().Screen().ThemeMode(mode)
		room.step(5000, time.Now())
		d.syncThemeSelects()
		if d.themeSel.Get() != "Forest" || d.lightThemeSel.Get() != "Paper" {
			t.Errorf("mode %q moved the selects to %q and %q", mode, d.themeSel.Get(), d.lightThemeSel.Get())
		}
	}
}

// Auto follows the room: the first reading decides at once, and after it the room has to stay past the
// other threshold for themeHold before the theme turns, so a lamp or a shadow does not flip it.
func TestAutoFollowsTheRoomWithAHold(t *testing.T) {
	withLightSensor(t)
	room.forget()
	t.Cleanup(room.forget)
	at := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	if !room.step(themeLightLux+50, at) {
		t.Fatal("the first reading of a lit room did not choose light")
	}
	steps := []struct {
		lux   float64
		after time.Duration
		lit   bool
	}{
		{themeDarkLux - 1, 0, true},                       // dim: the hold starts
		{themeDarkLux - 1, themeHold / 2, true},           // not long enough
		{(themeDarkLux + themeLightLux) / 2, 0, true},     // between the two: the hold is dropped
		{themeDarkLux - 1, time.Second, true},             // dim again: it starts over
		{themeDarkLux - 1, themeHold - time.Second, true}, // past a minute since the first dim reading, but the hold started over
		{themeDarkLux - 1, time.Second, false},            // held a minute: dark
		{themeLightLux - 1, time.Hour, false},             // under the light threshold stays dark
		{themeLightLux, time.Second, false},               // lit: the hold starts
		{themeLightLux, themeHold, true},                  // held: light
	}
	for i, s := range steps {
		at = at.Add(s.after)
		if got := room.step(s.lux, at); got != s.lit {
			t.Fatalf("step %d (%.0f lux): lit %v, want %v", i, s.lux, got, s.lit)
		}
	}

	config.Use(filepath.Join(t.TempDir(), "state.json"))
	_ = config.Set().Screen().Themes("Ocean", "Linen", config.ThemeModeAuto)
	if current().name != "Linen" {
		t.Error("Auto in a lit room does not wear the light theme")
	}
	room.forget()
	if current().name != "Ocean" {
		t.Error("Auto with no reading does not wear the dark theme")
	}
}

// withLightSensor is a screen with a light sensor, as a Show has, on the panel build too.
func withLightSensor(t *testing.T) {
	was := hasLightSensor
	hasLightSensor = func() bool { return true }
	t.Cleanup(func() { hasLightSensor = was })
}

// Where there is no light sensor (a panel) there is no Auto: Home Assistant is not offered it, Auto
// shared from another device is saved as Dark, and Auto already in the file wears the dark theme
// whatever the room.
func TestNoLightSensorNoAuto(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	was := hasLightSensor
	hasLightSensor = func() bool { return false }
	t.Cleanup(func() { hasLightSensor = was })
	room.forget()
	t.Cleanup(room.forget)

	s := themeModeSelect(func() {})
	if len(s.Options) != 2 || s.Options[0] != "Dark" || s.Options[1] != "Light" {
		t.Errorf("options %q, want Dark and Light", s.Options)
	}
	_ = config.Set().Screen().ThemeMode(config.ThemeModeLight)
	s.OnCommand("Auto")
	if m := config.Get().Screen.ThemeMode; m != "" {
		t.Errorf("a shared Auto saved %q, want Dark", m)
	}

	_ = config.Set().Screen().ThemeMode(config.ThemeModeAuto)
	room.step(5000, time.Now())
	if themeModeIndex() != 0 || lightShown(config.Get().Screen) {
		t.Error("Auto from the file is not Dark on a screen with no light sensor")
	}
}

// The Display card's theme row says which theme it changes: Theme in Dark mode, as it always did, and
// in Light or Auto the one worn now.
func TestTheThemeRowSaysWhichItChanges(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	withLightSensor(t)
	room.forget()
	t.Cleanup(room.forget)
	label := func() string { return themeRows()[0].label }
	if got := label(); got != "Theme" {
		t.Errorf("Dark mode: %q", got)
	}
	_ = config.Set().Screen().ThemeMode(config.ThemeModeLight)
	if got := label(); got != "Light theme" {
		t.Errorf("Light mode: %q", got)
	}
	_ = config.Set().Screen().ThemeMode(config.ThemeModeAuto)
	if got := label(); got != "Dark theme" {
		t.Errorf("Auto, no reading yet: %q", got)
	}
	room.step(5000, time.Now())
	if got := label(); got != "Light theme" {
		t.Errorf("Auto in a lit room: %q", got)
	}
	if p, _ := devicePicker("theme", sheetView{}); p.title != "Light theme" {
		t.Errorf("the picker is %q", p.title)
	}
	hasLightSensor = func() bool { return false }
	if got := label(); got != "Theme" {
		t.Errorf("Auto with no light sensor, which is Dark: %q", got)
	}
}
