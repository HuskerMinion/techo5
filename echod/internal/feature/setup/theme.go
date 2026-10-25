package setup

import (
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/palette"
)

// The screen's themes, on Screen & Photos where the screen has them (the Show): the dark mode's theme
// and the light mode's, which of the two the screen wears, and the custom colors, each a color picker
// with its #rrggbb beside it. The colors start from the theme the screen wears now, and Match the accent
// fills the other four in from the accent (palette.Derive), to be changed after as anybody likes.

func init() { routes["/setup/theme-match"] = (*Feature).themeMatch }

// ThemeChoices is what the display hands in for the section: whether the light theme is the one worn
// now, how to tell the screen and Home Assistant of a change saved here, and whether Auto is offered
// (only where there is a light sensor; not on a panel).
type ThemeChoices struct {
	LightNow func() bool
	Changed  func()
	Auto     bool
}

// themeModeChoices are the modes as the page names them, with what the config keeps. Home Assistant's
// Theme mode names them Dark, Light and Auto (display/theme_mode.go).
var themeModeChoices = []struct{ label, value string }{
	{"Dark", ""},
	{"Light", config.ThemeModeLight},
	{"Auto, by the room's light", config.ThemeModeAuto},
}

// modeChoices are the modes the section offers: Auto only where tc says.
func (tc *ThemeChoices) modeChoices() []struct{ label, value string } {
	if tc.Auto {
		return themeModeChoices
	}
	return themeModeChoices[:2]
}

// colorRoles are the five colors, as the form names them and as the page says them.
var colorRoles = [5]struct{ key, label string }{
	{"ground", "Ground"}, {"accent", "Accent"}, {"text", "Text"}, {"dim", "Dim text"}, {"rules", "Rules"},
}

// themeChoices are the screen's theme choices, or nil where the screen has no themes.
func themeChoices() *ThemeChoices {
	if s := screen.Load(); s != nil {
		return s.Themes
	}
	return nil
}

// savedCustom is the light mode's custom palette saved, or the dark mode's, when every one of its
// colors is a color.
func savedCustom(light bool) (palette.Preset, bool) {
	p := config.Get().Screen.PaletteFor(light)
	return palette.FromHex("Custom", p.Ground, p.Accent, p.Text, p.Dim, p.Rules)
}

// themeNames are the themes the light mode may wear, or the dark mode: the presets, and Custom once
// that mode's own is saved.
func themeNames(light bool) []string {
	out := make([]string, 0, len(palette.Presets)+1)
	for _, p := range palette.Presets {
		out = append(out, p.Name)
	}
	if _, ok := savedCustom(light); ok {
		out = append(out, "Custom")
	}
	return out
}

// themeOptions writes a select's options for the themes, the one named selected.
func themeOptions(w http.ResponseWriter, names []string, now string) {
	for _, n := range names {
		fmt.Fprintf(w, `<option%s>%s</option>`, selected(n == now), html.EscapeString(n))
	}
}

func themeSection(w http.ResponseWriter, token string) {
	tc := themeChoices()
	if tc == nil {
		return
	}
	sc := config.Get().Screen
	fmt.Fprint(w, `<fieldset><legend>Themes</legend><form method="post" action="/setup/save">`)
	hidden(w, token, "themes", "photos")
	fmt.Fprint(w, `<label for="darktheme">Dark theme</label><select id="darktheme" name="dark">`)
	themeOptions(w, themeNames(false), sc.ThemeFor(false))
	fmt.Fprint(w, `</select><label for="lighttheme">Light theme</label><select id="lighttheme" name="light">`)
	themeOptions(w, themeNames(true), sc.ThemeFor(true))
	fmt.Fprint(w, `</select><label for="thememode">Theme mode</label><select id="thememode" name="mode">`)
	for _, m := range tc.modeChoices() {
		fmt.Fprintf(w, `<option value="%s"%s>%s</option>`, m.value, selected(m.value == sc.ThemeMode), m.label)
	}
	fmt.Fprint(w, `</select>
	 <p class="note">Dark wears the dark theme, as the screen always has.`)
	if tc.Auto {
		fmt.Fprint(w, ` Auto wears the light theme in a lit
	  room and the dark theme in a dim one, once the room has stayed that way about a minute, by the light
	  sensor auto-brightness uses; until the sensor has a reading it stays dark.`)
	}
	fmt.Fprint(w, ` The night clock keeps its own
	  look. Also <strong>Theme mode</strong>, <strong>Theme</strong> (the dark theme) and <strong>Light
	  theme</strong> in Home Assistant.</p>
	 <p><button type="submit">Save</button></p></form>`)

	start := pageColors()
	forLight := tc.LightNow != nil && tc.LightNow()
	fmt.Fprint(w, `<h3>Custom colors</h3><form method="post" action="/setup/save">`)
	hidden(w, token, "theme-colors", "photos")
	for i, c := range start.Hex() {
		k := colorRoles[i].key
		fmt.Fprintf(w, `<label for="hex-%s">%s</label>
		 <div style="display:flex;gap:.5rem;align-items:center"><input type="color" id="pick-%s" value="%s" aria-label="%s"
		  style="width:3.2rem;height:2.6rem;padding:.15rem;flex:none"><input id="hex-%s" name="%s" value="%s" maxlength="7"
		  pattern="#[0-9a-fA-F]{6}" spellcheck="false" autocomplete="off"></div>`,
			k, colorRoles[i].label, k, c, colorRoles[i].label, k, k, c)
	}
	fmt.Fprintf(w, `<label for="colorsfor">Use them for</label><select id="colorsfor" name="for">
	 <option value="dark"%s>The dark theme</option><option value="light"%s>The light theme</option></select>
	 <div class="btns"><button type="button" class="quiet" id="match">Match the accent</button>
	  <button type="submit">Use these colors</button></div>
	 <p id="matchsays" class="note">Pick a color, or type it as #rrggbb. They start from the theme the screen wears
	  now. Match the accent fills in the other four from the accent's hue, dark or light for the theme they are
	  for; change any of them after. Using them makes that theme Custom; each theme keeps a Custom of its
	  own.</p></form>`,
		selected(!forLight), selected(forLight))
	fmt.Fprint(w, `<script>
	 (() => {
	   const keys = ['ground', 'accent', 'text', 'dim', 'rules'], hex = /^#[0-9a-fA-F]{6}$/;
	   const el = (id) => document.getElementById(id);
	   for (const k of keys) {
	     el('pick-' + k).addEventListener('input', () => { el('hex-' + k).value = el('pick-' + k).value; });
	     el('hex-' + k).addEventListener('input', () => {
	       const v = el('hex-' + k).value.trim();
	       if (hex.test(v)) el('pick-' + k).value = v.toLowerCase();
	     });
	   }
	   el('match').addEventListener('click', async () => {
	     const say = el('matchsays'), accent = el('hex-accent').value.trim();
	     if (!hex.test(accent)) { say.textContent = 'The accent has to be a #rrggbb color first.'; return; }
	     const light = el('colorsfor').value === 'light' ? '1' : '';
	     try {
	       const r = await fetch('/setup/theme-match?accent=' + encodeURIComponent(accent) + '&light=' + light);
	       if (!r.ok) { say.textContent = await r.text(); return; }
	       const c = await r.json();
	       for (const k of keys) {
	         if (k !== 'accent' && hex.test(c[k] || '')) { el('hex-' + k).value = c[k]; el('pick-' + k).value = c[k]; }
	       }
	       say.textContent = 'Filled in from the accent. Use these colors to keep them.';
	     } catch (e) { say.textContent = 'The colors could not be worked out.'; }
	   });
	 })();
	 </script></fieldset>`)
}

// saveThemes saves the dark and light themes and the mode.
func saveThemes(r *http.Request) string {
	tc := themeChoices()
	if tc == nil {
		return "this device has no themes"
	}
	dark, light, mode := r.PostFormValue("dark"), r.PostFormValue("light"), r.PostFormValue("mode")
	if !slices.Contains(themeNames(false), dark) || !slices.Contains(themeNames(true), light) {
		return "that is not a theme this device has"
	}
	if !slices.ContainsFunc(tc.modeChoices(), func(m struct{ label, value string }) bool { return m.value == mode }) {
		return "that is not a theme mode this device has"
	}
	if err := config.Set().Screen().Themes(dark, light, mode); err != nil {
		return "the themes could not be saved"
	}
	if tc.Changed != nil {
		tc.Changed()
	}
	slog.Info("setup page: themes set", "dark", dark, "light", light, "mode", mode)
	return ""
}

// saveThemeColors saves the five custom colors and makes them the dark theme or the light one. Each has
// to be exactly #rrggbb (palette.FromHex): they go into the page's style sheet as well as the screen.
func saveThemeColors(r *http.Request) string {
	tc := themeChoices()
	if tc == nil {
		return "this device has no themes"
	}
	var typed [5]string
	for i, c := range colorRoles {
		typed[i] = strings.TrimSpace(r.PostFormValue(c.key))
	}
	p, ok := palette.FromHex("Custom", typed[:]...)
	if !ok {
		return "each color has to be a #rrggbb color, like #e9a23b"
	}
	which := r.PostFormValue("for")
	if which != "dark" && which != "light" {
		return "the colors have to be for the dark theme or the light one"
	}
	hx := p.Hex()
	saved := config.Palette{Ground: hx[0], Accent: hx[1], Text: hx[2], Dim: hx[3], Rules: hx[4]}
	if err := config.Set().Screen().CustomFor(which == "light", saved); err != nil {
		return "the colors could not be saved"
	}
	if tc.Changed != nil {
		tc.Changed()
	}
	slog.Info("setup page: custom colors set", "for", which)
	return ""
}

// themeMatch is Match the accent: the other four colors worked out from the accent the page has, for a
// dark theme or a light one, as JSON. It saves nothing; the page fills its fields in with them.
func (f *Feature) themeMatch(w http.ResponseWriter, r *http.Request) {
	if _, in := f.session(r); !in {
		http.Error(w, "not let in", http.StatusForbidden)
		return
	}
	if themeChoices() == nil {
		http.Error(w, "this device has no themes", http.StatusNotFound)
		return
	}
	// FromHex reads five colors; the accent is checked as the first, the same way a saved one is.
	accent, ok := palette.FromHex("", r.URL.Query().Get("accent"), "#000000", "#000000", "#000000", "#000000")
	if !ok {
		http.Error(w, "the accent has to be a #rrggbb color", http.StatusBadRequest)
		return
	}
	light, _ := strconv.ParseBool(r.URL.Query().Get("light"))
	hx := palette.Derive(accent.Ground, light).Hex()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprintf(w, `{"ground":%q,"accent":%q,"text":%q,"dim":%q,"rules":%q}`, hx[0], hx[1], hx[2], hx[3], hx[4])
}
