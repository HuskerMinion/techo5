package setup

import (
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

func screenForm(style, slideshow string, art bool) url.Values {
	v := url.Values{"clockstyle": {style}, "slideshow": {slideshow}}
	if art {
		v.Set("art", "yes")
	}
	return v
}

// The screen's section saves the clock style through the display's own choice (which tells Home
// Assistant), the slideshow's mode, and weather art; what the device does not have is refused.
func TestTheScreenSectionSaves(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	chosen := 0
	SetScreen(&ScreenChoices{Styles: []string{"Classic", "Big", "Words"}, Current: func() int { return chosen }, Choose: func(i int) { chosen = i }})
	t.Cleanup(func() { SetScreen(nil) })

	if p := saveScreen(form(screenForm("2", config.SlideshowBackground, true))); p != "" {
		t.Fatal(p)
	}
	if chosen != 2 || home.Get().SlideshowMode() != config.SlideshowBackground || !home.Get().SlideshowArt() {
		t.Errorf("saved style %d, slideshow %q, art %v", chosen, home.Get().SlideshowMode(), home.Get().SlideshowArt())
	}
	if p := saveScreen(form(screenForm("2", "", true))); p != "" || home.Get().SlideshowMode() != "" {
		t.Errorf("choosing Off with the art ticked left the slideshow %q (%s)", home.Get().SlideshowMode(), p)
	}
	for _, bad := range []url.Values{screenForm("9", "", false), screenForm("x", "", false), screenForm("0", "sideways", false)} {
		if p := saveScreen(form(bad)); p == "" {
			t.Errorf("%v was saved", bad)
		}
	}
}

// A device without a screen shows no such section, and refuses a save of it.
func TestNoScreenNoSection(t *testing.T) {
	SetScreen(nil)
	w := httptest.NewRecorder()
	screenSection(w, "tok")
	if strings.Contains(w.Body.String(), "Clock style") {
		t.Error("a device with no screen offered a clock style")
	}
	if saveScreen(form(screenForm("0", "", false))) == "" {
		t.Error("a device with no screen saved one")
	}
}

// The World style's places are saved as typed, a space taken for the underscore; an unknown zone or
// one too many is refused with the rest of the form, and an empty field is the style's own places.
func TestTheScreenSectionSavesWorldPlaces(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	chosen := 0
	SetScreen(&ScreenChoices{Styles: []string{"Classic", "World"}, Current: func() int { return chosen }, Choose: func(i int) { chosen = i },
		Places: 2})
	t.Cleanup(func() { SetScreen(nil) })
	with := func(style, world string) url.Values {
		v := screenForm(style, "", false)
		v.Set("world", world)
		return v
	}

	if p := saveScreen(form(with("1", " Europe/Paris,America/Los Angeles ,, UTC"))); p != "" {
		t.Fatal(p)
	}
	want := []string{"Europe/Paris", "America/Los_Angeles", "UTC"}
	if got := config.Get().Screen.WorldClocks; !slices.Equal(got, want) || chosen != 1 {
		t.Errorf("saved %q (style %d), want %q", got, chosen, want)
	}

	for _, bad := range []string{"Europe/Atlantis", "Local", "Asia/Tokyo, Mars/Olympus", "UTC, Europe/Paris, Asia/Tokyo, Europe/Berlin"} {
		p := saveScreen(form(with("0", bad)))
		if p == "" {
			t.Errorf("%q was saved", bad)
		}
		if got := config.Get().Screen.WorldClocks; !slices.Equal(got, want) || chosen != 1 {
			t.Errorf("after %q the places are %q and the style %d", bad, got, chosen)
		}
	}
	if p := saveScreen(form(with("1", "Mars/Olympus"))); !strings.Contains(p, "Mars/Olympus") {
		t.Errorf("the refusal %q does not name the zone", p)
	}

	// A form without the field (an older page) leaves the places alone.
	if p := saveScreen(form(screenForm("1", "", false))); p != "" {
		t.Fatal(p)
	}
	if got := config.Get().Screen.WorldClocks; !slices.Equal(got, want) {
		t.Errorf("a form without the field changed the places to %q", got)
	}

	if p := saveScreen(form(with("1", "  "))); p != "" {
		t.Fatal(p)
	}
	if got := config.Get().Screen.WorldClocks; len(got) != 0 {
		t.Errorf("an empty field left %q", got)
	}
}

// The section shows the places saved, says how many fit here, and escapes what it shows.
func TestTheScreenSectionShowsWorldPlaces(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	SetScreen(&ScreenChoices{Styles: []string{"Classic"}, Current: func() int { return 0 }, Choose: func(int) {}, Places: 2})
	t.Cleanup(func() { SetScreen(nil) })
	if err := config.Set().Screen().WorldClocks([]string{"Europe/Paris", `"><b>`}); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	screenSection(w, "tok")
	body := w.Body.String()
	for _, want := range []string{"World clock places", `value="Europe/Paris, &#34;&gt;&lt;b&gt;"`, "shows the first 2"} {
		if !strings.Contains(body, want) {
			t.Errorf("the section has no %q", want)
		}
	}
	if strings.Contains(body, `"><b>`) {
		t.Error("the places were not escaped")
	}
}

// The swipe setting on the setup page: ticked on a new device, and saved unticked.
func TestTheSwipeSettingOnTheSetupPage(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	set := false
	SetScreen(&ScreenChoices{Styles: []string{"Classic"}, Current: func() int { return 0 }, Choose: func(int) {},
		Swipe: func(on bool) { set = true; _ = config.Set().Screen().NoStyleSwipe(!on) }})
	t.Cleanup(func() { SetScreen(nil) })
	w := httptest.NewRecorder()
	screenSection(w, "tok")
	if !strings.Contains(w.Body.String(), `name="swipe" value="yes" style="width:auto" checked`) {
		t.Error("not ticked on a new device")
	}
	if p := saveScreen(form(screenForm("0", "", false))); p != "" {
		t.Fatal(p)
	}
	if !set || !config.Get().Screen.NoStyleSwipe {
		t.Error("unticking it did not turn it off")
	}
}

// The note tying swiping to Tap on the clock shows only where there is a Tap on the clock (the Show,
// not the Spot).
func TestTheSwipeNoteOnlyWithATapOnTheClock(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { SetScreen(nil) })
	const note = "Tap on the clock is Nothing"
	for _, c := range []struct {
		name string
		taps []string
	}{{"no Tap on the clock", nil}, {"a Tap on the clock", []string{"Assist", "Nothing"}}} {
		SetScreen(&ScreenChoices{Styles: []string{"Classic"}, Current: func() int { return 0 }, Choose: func(int) {},
			Swipe: func(bool) {}, Taps: c.taps, TapNow: func() int { return 0 }, ChooseTap: func(int) {}})
		w := httptest.NewRecorder()
		screenSection(w, "tok")
		if got, want := strings.Contains(w.Body.String(), note), c.taps != nil; got != want {
			t.Errorf("%s: the note shown %v, want %v", c.name, got, want)
		}
	}
}
