//go:build !dot && !spot

package setup

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/buttons"
	"github.com/HuskerMinion/techo5/echod/internal/lib/palette"
)

// withThemes is a screen with themes, as the Show's display hands them in, counting the changes told.
func withThemes(t *testing.T, light bool) *int {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	told := 0
	SetScreen(&ScreenChoices{Themes: &ThemeChoices{LightNow: func() bool { return light }, Changed: func() { told++ }, Auto: true}})
	t.Cleanup(func() { SetScreen(nil) })
	return &told
}

// The section offers both themes, the mode, and a color picker with its #rrggbb beside it for each of
// the five colors, starting from the theme the screen wears now.
func TestTheThemesSectionOffersPickersAndHex(t *testing.T) {
	withThemes(t, true)
	_ = config.Set().Screen().Themes("Ocean", "Linen", config.ThemeModeLight)
	w := httptest.NewRecorder()
	themeSection(w, "tok")
	body := w.Body.String()
	linen := palette.Named("Linen").Hex()
	for _, want := range []string{`name="dark"`, `name="light"`, `name="mode"`, `type="color" id="pick-accent" value="` + linen[1] + `"`,
		`id="hex-ground" name="ground" value="` + linen[0] + `"`, "Match the accent", `<option value="light" selected>`} {
		if !strings.Contains(body, want) {
			t.Errorf("the section has no %s", want)
		}
	}
}

// A screen with no light sensor (a panel) offers no Auto, says nothing of it, and refuses it.
func TestNoLightSensorNoAuto(t *testing.T) {
	withThemes(t, false)
	themeChoices().Auto = false
	w := httptest.NewRecorder()
	themeSection(w, "tok")
	body := w.Body.String()
	if strings.Contains(body, `value="auto"`) || strings.Contains(body, "Auto wears") {
		t.Errorf("a screen with no light sensor offers Auto: %s", body)
	}
	if !strings.Contains(body, `<option value="" selected>Dark</option><option value="light">Light</option></select>`) {
		t.Errorf("the modes are not Dark and Light alone: %s", body)
	}
	if p := saveThemes(form(url.Values{"dark": {"Ocean"}, "light": {"Linen"}, "mode": {config.ThemeModeAuto}})); p == "" {
		t.Error("Auto was saved on a screen with no light sensor")
	}
}

// A device without themes (the Spot) has no section, and refuses its saves.
func TestNoThemesNoSection(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	SetScreen(&ScreenChoices{})
	t.Cleanup(func() { SetScreen(nil) })
	w := httptest.NewRecorder()
	themeSection(w, "tok")
	if w.Body.Len() != 0 {
		t.Error("a screen without themes got the section")
	}
	if saveThemes(form(url.Values{"dark": {"Ocean"}, "light": {"Paper"}, "mode": {""}})) == "" {
		t.Error("a screen without themes saved some")
	}
}

// The themes and the mode are saved as chosen and the screen told; what the device does not have is
// refused, Custom included until one is saved.
func TestThemesAndModeSave(t *testing.T) {
	told := withThemes(t, false)
	if p := saveThemes(form(url.Values{"dark": {"Ocean"}, "light": {"Linen"}, "mode": {config.ThemeModeAuto}})); p != "" {
		t.Fatal(p)
	}
	sc := config.Get().Screen
	if sc.Theme != "Ocean" || sc.LightTheme != "Linen" || sc.ThemeMode != config.ThemeModeAuto || *told != 1 {
		t.Errorf("saved dark %q, light %q, mode %q, told %d", sc.Theme, sc.LightTheme, sc.ThemeMode, *told)
	}
	for _, bad := range []url.Values{
		{"dark": {"Nope"}, "light": {"Paper"}, "mode": {""}},
		{"dark": {"Ocean"}, "light": {"Custom"}, "mode": {""}},
		{"dark": {"Ocean"}, "light": {"Paper"}, "mode": {"sideways"}},
	} {
		if p := saveThemes(form(bad)); p == "" {
			t.Errorf("%v was saved", bad)
		}
	}
}

// The custom colors are saved only when every one is #rrggbb, and make the theme they are for Custom.
func TestCustomColorsSaveAsHex(t *testing.T) {
	told := withThemes(t, false)
	_ = config.Set().Screen().Theme("Ocean")
	colors := url.Values{"ground": {"#101820"}, "accent": {" #E9A23B "}, "text": {"#e8dcc8"}, "dim": {"#8a7d6c"}, "rules": {"#3a2c22"}, "for": {"light"}}
	if p := saveThemeColors(form(colors)); p != "" {
		t.Fatal(p)
	}
	sc := config.Get().Screen
	if sc.LightTheme != "Custom" || sc.Theme != "Ocean" || sc.LightPalette.Accent != "#e9a23b" || *told != 1 {
		t.Errorf("saved light %q, dark %q, accent %q, told %d", sc.LightTheme, sc.Theme, sc.LightPalette.Accent, *told)
	}
	if sc.Palette != (config.Palette{}) {
		t.Errorf("colors for the light theme went to the dark one: %+v", sc.Palette)
	}
	for _, k := range []string{"ground", "accent", "text"} {
		bad := url.Values{}
		for kk, v := range colors {
			bad[kk] = v
		}
		bad.Set(k, "red;}body{x:y")
		if p := saveThemeColors(form(bad)); p == "" {
			t.Errorf("a %s of %q was saved", k, bad.Get(k))
		}
	}
	colors.Set("for", "both")
	if saveThemeColors(form(colors)) == "" {
		t.Error("colors for neither theme were saved")
	}
	// Custom is a theme to choose now for the light mode, whose it is, and not yet for the dark one.
	if p := saveThemes(form(url.Values{"dark": {"Ocean"}, "light": {"Custom"}, "mode": {""}})); p != "" {
		t.Error(p)
	}
	if p := saveThemes(form(url.Values{"dark": {"Custom"}, "light": {"Paper"}, "mode": {""}})); p == "" {
		t.Error("the dark theme was made Custom with only the light mode's colors saved")
	}
	colors.Set("for", "dark")
	colors.Set("accent", "#00ff00")
	if p := saveThemeColors(form(colors)); p != "" {
		t.Fatal(p)
	}
	sc = config.Get().Screen
	if sc.Theme != "Custom" || sc.Palette.Accent != "#00ff00" || sc.LightPalette.Accent != "#e9a23b" {
		t.Errorf("colors for the dark theme saved dark %q %q, light accent %q", sc.Theme, sc.Palette.Accent, sc.LightPalette.Accent)
	}
}

// Match the accent answers with the other four colors worked out from it, and saves nothing; only a
// browser let in may ask, and only with a color.
func TestMatchTheAccent(t *testing.T) {
	withThemes(t, false)
	f := build()
	f.Open()
	c := ask(t, f)
	f.button(buttons.Event{Name: buttons.Action, Kind: buttons.Tap})

	if w := get(f, "/setup/theme-match?accent=%23f05a3c", nil); w.Code == 200 {
		t.Error("a browser not let in was answered")
	}
	w := get(f, "/setup/theme-match?accent=%23f05a3c&light=1", c)
	var got map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("%d %s: %v", w.Code, w.Body.String(), err)
	}
	want := palette.Derive(0xf05a3c, true).Hex()
	if got["ground"] != want[0] || got["text"] != want[2] || got["accent"] != "#f05a3c" {
		t.Errorf("matched %v, want %v", got, want)
	}
	if config.Get().Screen.Palette != (config.Palette{}) {
		t.Error("matching saved the colors")
	}
	if w := get(f, "/setup/theme-match?accent=red", c); w.Code != 400 {
		t.Errorf("an accent of red got %d", w.Code)
	}
}
