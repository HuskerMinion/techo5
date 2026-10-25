package setup

import (
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// The screen's own settings that a device far from its owner still needs set from a phone: how the
// clock looks, the font of the screen's words, what a tap on it does, the slideshow, and weather art in place of its photos. On the
// Screen & Photos tab, over the dashboard and the photos; the rest of what the screen shows is still
// set on the screen, where it can be seen.

// ScreenChoices is the clock's look as the screen offers it, handed in by the display, which imports
// this package rather than the other way round. Choose saves one and tells Home Assistant.
type ScreenChoices struct {
	Styles  []string
	Current func() int
	Choose  func(int)

	// Fonts are the fonts the screen's words can be drawn in, with the one in force and how to choose
	// one (which tells Home Assistant too).
	Fonts      []string
	FontNow    func() int
	ChooseFont func(int)

	// OwnFonts lists the fonts folder, AddFont keeps a font file sent from the page (at most MaxFont
	// bytes) and RemoveFont deletes one (fonts.go); nil where there is no such folder.
	OwnFonts   func() []OwnFont
	AddFont    func(name string, r io.Reader) error
	RemoveFont func(file string) error
	MaxFont    int64

	// Taps are what a tap on the clock can do, where the screen offers the choice (the Show), with the
	// one chosen and how to choose one.
	Taps      []string
	TapNow    func() int
	ChooseTap func(int)

	// Places is how many of the World style's places the screen has room for.
	Places int

	// Swipe sets Swipe between clock styles, telling Home Assistant.
	Swipe func(bool)
	// VolumeSwipe sets Swipe for volume, telling Home Assistant (#243).
	VolumeSwipe func(bool)

	// IgnoreRoomLights sets Screen ignores room light commands, telling Home Assistant (#179); nil
	// leaves the box off the page.
	IgnoreRoomLights func(bool)
	// Calendar is how the calendar page looks, where the screen has one of its own (the Show): on the
	// Weather & Calendar tab, under the calendars.
	Calendar []CalendarChoice

	// Themes is the screen's themes and light and dark mode, where it has them (the Show); nil leaves
	// the Themes section off the page (theme.go).
	Themes *ThemeChoices
}

// CalendarChoice is one of the calendar's look switches (display/calendar_look.go): its name and what it
// does, its options (On and Off for a switch), the one in force and how to choose one, which saves it
// and tells Home Assistant.
type CalendarChoice struct {
	Key, Label, Note string
	Options          []string
	Now              func() int
	Choose           func(int)
}

// maxWorldPlaces is how many World places the page takes: the most any screen shows.
const maxWorldPlaces = 3

// fewerPlaces says so when this screen shows fewer World places than the page takes.
func fewerPlaces(n int) string {
	if n <= 0 || n >= maxWorldPlaces {
		return ""
	}
	return fmt.Sprintf(" This screen shows the first %d.", n)
}

var screen atomic.Pointer[ScreenChoices]

// SetScreen is how the display offers its choices; a device with no screen never calls it.
func SetScreen(s *ScreenChoices) { screen.Store(s) }

// slideshowModes are the slideshow's modes as the page names them, with what the config keeps.
var slideshowModes = []struct{ label, value string }{
	{"Off", ""},
	{"Behind the clock", config.SlideshowBackground},
	{"Screensaver, after a while", config.SlideshowScreensaver},
}

func screenSection(w http.ResponseWriter, token string) {
	s := screen.Load()
	if s == nil {
		return
	}
	fmt.Fprint(w, `<fieldset><legend>The screen</legend><form method="post" action="/setup/save">`)
	hidden(w, token, "screen", "photos")
	fmt.Fprint(w, `<label for="clockstyle">Clock style</label><select id="clockstyle" name="clockstyle">`)
	cur := s.Current()
	for i, l := range s.Styles {
		fmt.Fprintf(w, `<option value="%d"%s>%s</option>`, i, selected(i == cur), html.EscapeString(l))
	}
	fmt.Fprint(w, `</select>`)
	if len(s.Fonts) > 0 {
		fmt.Fprint(w, `<label for="font">Font</label><select id="font" name="font">`)
		now := s.FontNow()
		for i, l := range s.Fonts {
			fmt.Fprintf(w, `<option value="%d"%s>%s</option>`, i, selected(i == now), html.EscapeString(l))
		}
		fmt.Fprint(w, `</select>
	 <p class="note">For every word on the screen: the clock, the weather, the settings and the rest. Go is
	  the screen's own. A letter a font does not have is drawn in Go.</p>`)
	}
	places := strings.Join(config.Get().Screen.WorldClocks, ", ")
	fmt.Fprintf(w, `<label for="world">World clock places</label>
	 <input id="world" name="world" value="%s" placeholder="America/New_York, Europe/London, Asia/Tokyo">
	 <p class="note">For the World clock style: up to %d time zone names, with commas between them. Leave it
	  empty for New York, London and Tokyo.%s</p>`, html.EscapeString(places), maxWorldPlaces, fewerPlaces(s.Places))
	swipe := ""
	if !config.Get().Screen.NoStyleSwipe {
		swipe = " checked"
	}
	fmt.Fprintf(w, `<p><label><input type="checkbox" name="swipe" value="yes" style="width:auto"%s> Swipe between clock
	 styles: a swipe left or right across the clock turns to the next style or the one before</label></p>`, swipe)
	if len(s.Taps) > 0 {
		// Only where there is a Tap on the clock to set (the Show).
		fmt.Fprint(w, `<p class="note">Off whenever Tap on the clock is Nothing, too.</p>`)
	}
	volume := ""
	if !config.Get().Screen.NoVolumeSwipe {
		volume = " checked"
	}
	fmt.Fprintf(w, `<p><label><input type="checkbox" name="volumeswipe" value="yes" style="width:auto"%s> Swipe for
	 volume: a swipe up or down on the clock turns the volume up or down</label></p>`, volume)
	if len(s.Taps) > 0 {
		fmt.Fprint(w, `<label for="clocktap">Tap on the clock</label><select id="clocktap" name="clocktap">`)
		now := s.TapNow()
		for i, l := range s.Taps {
			fmt.Fprintf(w, `<option value="%d"%s>%s</option>`, i, selected(i == now), html.EscapeString(l))
		}
		fmt.Fprint(w, `</select>`)
	}
	if s.IgnoreRoomLights != nil {
		room := ""
		if config.Get().Screen.IgnoreRoomLights {
			room = " checked"
		}
		fmt.Fprintf(w, `<p><label><input type="checkbox" name="roomlights" value="yes" style="width:auto"%s> Screen ignores
	 room light commands (off at first): the screen's light is then a configuration entity in Home Assistant,
	 apart from the room's lights</label></p>
	 <p class="note">Home Assistant leaves a configuration entity out of area, device and label targets and
	  out of the dashboards it makes itself; an automation that names the light by its entity ID still reaches
	  it, and Screen power, a switch, is the one to name by voice. A screen Home Assistant already gave to
	  Assist stays with it until it is unticked under Settings, Voice assistants, Expose, which on its own keeps
	  "lights off" said to Assist away from the screen too.</p>`, room)
	}
	fmt.Fprint(w, `<label for="slideshow">Slideshow</label><select id="slideshow" name="slideshow">`)
	mode := home.Get().SlideshowMode()
	for _, m := range slideshowModes {
		fmt.Fprintf(w, `<option value="%s"%s>%s</option>`, m.value, selected(m.value == mode), m.label)
	}
	art := ""
	if home.Get().SlideshowArt() {
		art = " checked"
	}
	fmt.Fprintf(w, `</select>
	 <p><label><input type="checkbox" name="art" value="yes" style="width:auto"%s> Weather art in place of photos</label></p>
	 <p class="note">Weather art is a landscape the device draws for the weather and the time of day, with rain,
	  snow and clouds moving over it. It needs no photos and no Home Assistant.</p>
	 <p><button type="submit">Save</button></p></form></fieldset>`, art)
}

func saveScreen(r *http.Request) string {
	s := screen.Load()
	if s == nil {
		return "this device has no screen"
	}
	i, err := strconv.Atoi(r.PostFormValue("clockstyle"))
	if err != nil || i < 0 || i >= len(s.Styles) {
		return "that is not a clock style this device has"
	}
	mode := r.PostFormValue("slideshow")
	known := false
	for _, m := range slideshowModes {
		known = known || m.value == mode
	}
	if !known {
		return "that is not a slideshow this device has"
	}
	fnt := -1
	if len(s.Fonts) > 0 {
		f, err := strconv.Atoi(r.PostFormValue("font"))
		if err != nil || f < 0 || f >= len(s.Fonts) {
			return "that is not a font this device has"
		}
		fnt = f
	}
	tap := -1
	if len(s.Taps) > 0 {
		t, err := strconv.Atoi(r.PostFormValue("clocktap"))
		if err != nil || t < 0 || t >= len(s.Taps) {
			return "that is not something a tap on the clock can do"
		}
		tap = t
	}
	world, has := r.PostForm["world"]
	var places []string
	if has {
		var problem string
		if places, problem = worldZones(strings.Join(world, ",")); problem != "" {
			return problem
		}
	}
	if i != s.Current() {
		s.Choose(i)
	}
	if fnt >= 0 && fnt != s.FontNow() {
		s.ChooseFont(fnt)
	}
	if tap >= 0 && tap != s.TapNow() {
		s.ChooseTap(tap)
	}
	if on := r.PostFormValue("swipe") == "yes"; on == config.Get().Screen.NoStyleSwipe {
		if s.Swipe != nil {
			s.Swipe(on)
		} else if err := config.Set().Screen().NoStyleSwipe(!on); err != nil {
			return "the swipe setting could not be saved"
		}
	}
	if on := r.PostFormValue("volumeswipe") == "yes"; on == config.Get().Screen.NoVolumeSwipe {
		if s.VolumeSwipe != nil {
			s.VolumeSwipe(on)
		} else if err := config.Set().Screen().NoVolumeSwipe(!on); err != nil {
			return "the volume swipe setting could not be saved"
		}
	}
	if s.IgnoreRoomLights != nil {
		if on := r.PostFormValue("roomlights") == "yes"; on != config.Get().Screen.IgnoreRoomLights {
			s.IgnoreRoomLights(on)
		}
	}
	if has && !slices.Equal(places, config.Get().Screen.WorldClocks) {
		if err := config.Set().Screen().WorldClocks(places); err != nil {
			return "the world clock places could not be saved"
		}
	}
	h := home.Get()
	h.ChooseSlideshowMode(mode)
	// The mode first, then the art: ticking weather art with the slideshow off turns it on behind the
	// clock (SetSlideshowArt), and choosing Off with the art already ticked turns the slideshow off.
	if on := r.PostFormValue("art") == "yes"; on != h.SlideshowArt() {
		h.SetSlideshowArt(on)
	}
	slog.Info("setup page: screen set", "clock style", strings.ToLower(s.Styles[i]), "slideshow", mode, "art", h.SlideshowArt())
	return ""
}

// worldZones is the World style's places as typed, comma-separated time zone names, checked: each one
// has to be a zone this device knows, and there can be at most maxWorldPlaces. A space in a name is
// taken for the underscore ("America/New York"). None is the style's own places.
func worldZones(typed string) ([]string, string) {
	var out []string
	for _, z := range strings.Split(typed, ",") {
		z = strings.Join(strings.Fields(z), "_")
		if z == "" {
			continue
		}
		if _, err := time.LoadLocation(z); err != nil || z == "Local" {
			return nil, fmt.Sprintf("%q is not a time zone this device knows; use a name like America/Chicago or Europe/Paris", z)
		}
		out = append(out, z)
	}
	if len(out) > maxWorldPlaces {
		return nil, fmt.Sprintf("the World clock has room for %d places at most", maxWorldPlaces)
	}
	return out, ""
}
