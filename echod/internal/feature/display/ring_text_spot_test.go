//go:build spot

package display

import (
	"image"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
	"github.com/HuskerMinion/techo5/echod/internal/lib/locale"
)

// ringScenes are the states that change what the ring's hints say.
func ringScenes() []roundScene {
	at := time.Date(2026, 9, 16, 14, 7, 0, 0, time.Local)
	base := roundScene{now: at, volume: 40, announceReady: true, phoneReady: true, houseReady: true}
	var out []roundScene
	add := func(f func(s *roundScene)) {
		s := base
		f(&s)
		out = append(out, s)
	}
	add(func(s *roundScene) {})
	add(func(s *roundScene) { s.muted = true })
	add(func(s *roundScene) { s.playing = true })
	add(func(s *roundScene) { s.paused = true })
	add(func(s *roundScene) { s.showDash = true })
	add(func(s *roundScene) { s.timerRinging = true })
	add(func(s *roundScene) { s.timers = []timer.Countdown{{Left: 3725 * time.Second}} })
	add(func(s *roundScene) {
		s.timers = []timer.Countdown{{Left: 3725 * time.Second}, {Left: time.Minute}}
	})
	add(func(s *roundScene) { s.announceReady = false; s.phoneReady, s.houseReady = false, false })
	add(func(s *roundScene) { s.announceRecording = true })
	add(func(s *roundScene) { s.announcePeers = 12; s.contactCount = 12 })
	return out
}

// Every hint the ring can show stays inside the clear space between the icons, on at most two lines, at
// the default text size and at the largest, and none of the ones the dial shows needs an ellipsis in
// English.
func TestRingHintsFitInsideTheIcons(t *testing.T) {
	for _, size := range []int{100, 125} {
		img := image.NewRGBA(image.Rect(0, 0, side, side))
		r := newRoundRenderer(img)
		r.textSize = size
		r.makeFaces(fontKey{name: r.font, gen: r.fontGen, size: size})
		checkRingHints(t, r, size, false)
	}
}

// checkRingHints checks every hint of every scene on r. Where clipOK is false a line may not be cut.
func checkRingHints(t *testing.T, r *roundRenderer, size int, clipOK bool) {
	t.Helper()
	for si, s := range ringScenes() {
		for _, it := range mainItems {
			hint := itemHint(s, it.id)
			lines := r.ringLines(r.small, hint, 286, 2)
			if len(lines) == 0 && hint != "" {
				t.Errorf("size %d scene %d %s: %q has no line", size, si, it.id, hint)
			}
			lineH := r.small.Metrics().Height.Round() + 4
			for n, l := range lines {
				if w, room := r.width(r.small, l), ringRoom(r.small, 286+n*lineH); w > room {
					t.Errorf("size %d scene %d %s: %q is %d wide, the ring has %d", size, si, it.id, l, w, room)
				}
				if !clipOK && size == 100 && strings.HasSuffix(l, "…") {
					t.Errorf("size %d scene %d %s: %q was cut", size, si, it.id, l)
				}
			}
		}
	}
}

// The names and the header keep inside the ring too, cut with an ellipsis when a camera's name is long.
func TestRingNameIsCutToTheIcons(t *testing.T) {
	r := newRoundRenderer(image.NewRGBA(image.Rect(0, 0, side, side)))
	long := "A camera with a very long name that would run into the icons on either side"
	got := r.paint.fit(r.title, long, ringRoom(r.title, 252))
	if !strings.HasSuffix(got, "…") || r.width(r.title, got) > ringRoom(r.title, 252) {
		t.Errorf("name %q is not cut to the ring", got)
	}
}

// screenLangs are the languages with a full catalog, and "" for English.
var screenLangs = []string{"", "de", "fr", "gl", "it", "ja", "tr"}

// In every language with a catalog, each hint still fits inside the icons on two lines, and the names
// of the items on one, without being cut short.
func TestRingTextFitsInEveryLanguage(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { _ = config.Set().Screen().Language("") })
	r := newRoundRenderer(image.NewRGBA(image.Rect(0, 0, side, side)))
	for _, lang := range screenLangs {
		if err := config.Set().Screen().Language(lang); err != nil {
			t.Fatal(err)
		}
		checkRingHints(t, r, 100, false)
		for _, s := range ringScenes() {
			for _, it := range mainItems {
				name := itemName(s, it.id)
				if got := r.paint.fit(r.body, name, ringRoom(r.body, 252)); got != name {
					t.Errorf("%q: the name %q is cut to %q", lang, name, got)
				}
			}
		}
	}
}

// The empty contact list's messages are said in full in every language, on the four lines it has.
func TestContactMessagesFitInEveryLanguage(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { _ = config.Set().Screen().Language("") })
	r := newRoundRenderer(image.NewRGBA(image.Rect(0, 0, side, side)))
	for _, lang := range screenLangs {
		if err := config.Set().Screen().Language(lang); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"ui_no_contacts_yet", "ui_set_house_word_to_call_other_devices", "drawer_no_peers", "weather_no_reading_from_home_assistant", "weather_forecast_needs_a_home_assistant_token"} {
			lines, more := r.paragraphLines(r.body, strings.Fields(locale.Text(lang, key)), 230, 4)
			if key[0] == 'w' {
				lines, more = r.paragraphLines(r.small, strings.Fields(locale.Text(lang, key)), 310, 2)
			}
			if more {
				t.Errorf("%q: %s is cut after %d lines: %q", lang, key, len(lines), lines)
			}
		}
	}
}
