//go:build !dot && !spot

package display

import (
	"errors"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/alarm"
	"github.com/HuskerMinion/techo5/echod/internal/feature/notify"
	"github.com/HuskerMinion/techo5/echod/internal/feature/remind"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
)

// offerOf makes the alarm feature offer token for the length of a test, as the engine would.
func offerOf(t *testing.T, token alarm.SkipToken, ok bool) {
	t.Helper()
	was := skippable
	t.Cleanup(func() { skippable = was })
	skippable = func(time.Time) (alarm.SkipToken, bool) { return token, ok }
}

// answerOf is the answer a press on offer leaves at the foot of the page, as the page keeps it: the
// alarm, the moment and whether it was left out, not the line it is drawn as.
func answerOf(offer skipOffer, ok bool) skipAnswer {
	return skipAnswer{label: offer.label, at: offer.token.At, skipped: ok}
}

// skipWords is the whole line the answer to a press on offer makes, as the tests that read the words
// read it; the page keeps the answer and fits the line where it draws it (skipLine).
func skipWords(offer skipOffer, ok bool) string { return skipWhole(answerOf(offer, ok)) }

// recordingSkip stands in for the alarm feature's way of leaving an occurrence out, and keeps what
// was asked of it.
func recordingSkip(t *testing.T, err error) *[]alarm.SkipToken {
	t.Helper()
	was := skipOccurrence
	t.Cleanup(func() { skipOccurrence = was })
	var asked []alarm.SkipToken
	skipOccurrence = func(id string, at time.Time) error {
		asked = append(asked, alarm.SkipToken{ID: id, At: at})
		return err
	}
	return &asked
}

// lightAt makes the screen show p of the wake light, as the alarm feature reports it, for the length
// of a test.
func lightAt(t *testing.T, p float64) {
	t.Helper()
	was := sunriseProgress
	t.Cleanup(func() { sunriseProgress = was })
	sunriseProgress = func(time.Time) float64 { return p }
}

// noHold is the light only coming up, with nothing holding it at full after an alarm, for the length
// of a test.
func noHold(t *testing.T) {
	t.Helper()
	was := sunriseHold
	t.Cleanup(func() { sunriseHold = was })
	sunriseHold = func(time.Time) (time.Time, bool, bool) { return time.Time{}, false, false }
}

// skipState points the config store at a file of this test's own, with the ambient wake light on.
func skipState(t *testing.T, sunrise int) {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Alarms().SunriseMinutes(sunrise); err != nil {
		t.Fatal(err)
	}
}

// The light's page offers the alarm whose own window is lighting the room, and nothing else: not one
// whose window is not open yet, nor one already past, nor any while the light is held at full after
// an alarm. The engine's own rules — normal, local, repeating, wake light — are its business
// (feature/alarm/skip_test.go); what is this page's is which occurrence the sunrise on it belongs to.
func TestTheLightPageOffersOnlyTheAlarmItIsComingUpFor(t *testing.T) {
	now := time.Date(2026, time.October, 19, 6, 45, 0, 0, time.Local)
	at := time.Date(2026, time.October, 19, 7, 0, 0, 0, time.Local)

	for _, tc := range []struct {
		name   string
		alarm  config.Alarm
		token  alarm.SkipToken
		light  float64 // what the screen shows of the wake light
		offers bool
		heldAt time.Time
	}{
		{"the alarm ahead", config.Alarm{ID: "wake", Hour: 7, Label: "Wake up", On: true},
			alarm.SkipToken{ID: "wake", At: at}, 0.5, true, time.Time{}},
		{"a window not open yet", config.Alarm{ID: "wake", Hour: 7, Label: "Wake up", On: true},
			alarm.SkipToken{ID: "wake", At: at.Add(30 * time.Minute)}, 0.5, false, time.Time{}},
		{"the moment gone by", config.Alarm{ID: "wake", Hour: 7, Label: "Wake up", On: true},
			alarm.SkipToken{ID: "wake", At: now.Add(-time.Minute)}, 0.5, false, time.Time{}},
		{"an alarm not set", config.Alarm{ID: "other", Hour: 7, Label: "Other", On: true},
			alarm.SkipToken{ID: "wake", At: at}, 0.5, false, time.Time{}},
		{"no wake light of its own", config.Alarm{ID: "wake", Hour: 7, Label: "Wake up", On: true, Sunrise: config.SunriseOff},
			alarm.SkipToken{ID: "wake", At: at}, 0, false, time.Time{}},
		{"another alarm's light further along", config.Alarm{ID: "wake", Hour: 7, Label: "Wake up", On: true},
			alarm.SkipToken{ID: "wake", At: at}, 0.8, false, time.Time{}},
		{"a light that is not this ramp", config.Alarm{ID: "wake", Hour: 7, Label: "Wake up", On: true},
			alarm.SkipToken{ID: "wake", At: at}, 0.2, false, time.Time{}},
		{"held at full after an alarm", config.Alarm{ID: "wake", Hour: 7, Label: "Wake up", On: true},
			alarm.SkipToken{ID: "wake", At: at}, 1, false, now.Add(50 * time.Minute)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			skipState(t, 30)
			al := tc.alarm
			al.Days = config.DaysEvery
			if err := config.Set().Alarms().Put(al); err != nil {
				t.Fatal(err)
			}
			offerOf(t, tc.token, true)
			if tc.heldAt.IsZero() {
				noHold(t)
				lightAt(t, tc.light)
			} else {
				heldLight(t, tc.heldAt, false, true)
			}

			got := sunriseSkip(now)
			if !tc.offers {
				if got != nil {
					t.Fatalf("offered %+v, want nothing", *got)
				}
				return
			}
			if got == nil {
				t.Fatal("nothing was offered while the light came up before the alarm")
			}
			if got.token != tc.token || got.label != "Wake up" {
				t.Errorf("offered %+v, want %+v with its label", *got, tc.token)
			}
		})
	}

	// The engine having nothing to offer is no offer; an alarm with no label is still offered, and the
	// drawing falls back to the word for an alarm. The light is the offered alarm's own ramp here, at
	// 06:50 on the way to 07:00.
	skipState(t, 30)
	if err := config.Set().Alarms().Put(config.Alarm{ID: "wake", Hour: 7, Days: config.DaysEvery, On: true}); err != nil {
		t.Fatal(err)
	}
	noHold(t)
	lightAt(t, 0.5)
	offerOf(t, alarm.SkipToken{}, false)
	if got := sunriseSkip(now); got != nil {
		t.Errorf("offered %+v with nothing to offer", *got)
	}
	offerOf(t, alarm.SkipToken{ID: "wake", At: at}, true)
	if got := sunriseSkip(now); got == nil || got.label != "" {
		t.Errorf("offered %+v, want the unnamed alarm's moment", got)
	}
}

// The button is where the frame that drew it put it, and only on that page: a page drawn instead of
// the light leaves nothing there to tap, and the light's own page brings it back.
func TestTheButtonsTapFollowsTheFrame(t *testing.T) {
	now := time.Date(2026, time.October, 19, 6, 45, 0, 0, time.Local)
	at := now.Add(15 * time.Minute)
	offer := skipOffer{token: alarm.SkipToken{ID: "wake", At: at}, label: "Wake up"}
	light := func() scene {
		return scene{now: now, phase: "idle", sunrise: 0.4, skip: &offer}
	}

	dir := os.Getenv("SHOW_PREVIEW")
	for _, panel := range showPanels() {
		r := newRenderer(image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high)))
		r.draw(light())
		b := r.skipAt
		if b.Empty() {
			t.Fatalf("%s: the button was not drawn on the light's page", panel.name)
		}
		if b.Min.Y < panel.high/2 || b.Max.Y > panel.high-r.s(48) || b.Min.X < 0 || b.Max.X > panel.wide {
			t.Errorf("%s: the button at %v is not between the clock and the foot of a %dx%d page",
				panel.name, b, panel.wide, panel.high)
		}
		mid := b.Min.Add(image.Pt(b.Dx()/2, b.Dy()/2))
		got, ok := r.skipTapped(mid)
		if !ok || got.token != offer.token || got.label != offer.label {
			t.Errorf("%s: a tap on the button at %v found %+v %v", panel.name, mid, got, ok)
		}
		if _, ok := r.skipTapped(image.Pt(panel.wide/2, panel.high/2)); ok {
			t.Errorf("%s: a tap on the clock counted as the button", panel.name)
		}
		if _, ok := r.skipTapped(image.Pt(b.Min.X-r.s(20), b.Min.Y-r.s(20))); ok {
			t.Errorf("%s: a tap well off the button counted", panel.name)
		}

		// A page that takes the screen instead of the light: the ring, the night clock, the settings,
		// the calendar. The scene still carries the light and its offer, as a frame caught between the
		// light being up and the page arriving can, and none of them draws the button.
		for name, over := range map[string]scene{
			"settings": {now: now, phase: "idle", showSheet: true, sheet: settings{cat: catDisplay}},
			"calendar": {now: now, phase: "idle", showCalendar: true, cal: calendarView{
				month: now, now: now, loaded: true}},
			"ringing": {now: now, phase: "idle",
				ring: ringState{alarm: &alarm.Ring{Label: "Wake up", At: now}}},
			"night": {now: now, phase: "idle", redClock: true, redStyle: "digits"},
		} {
			over.sunrise = 0.4
			r.draw(over)
			if !r.skipAt.Empty() {
				t.Errorf("%s/%s: the button at %v is still tappable under another page", panel.name, name, r.skipAt)
			}
			if _, ok := r.skipTapped(mid); ok {
				t.Errorf("%s/%s: a tap where the button was counted", panel.name, name)
			}
		}

		// The ordinary clock page, which the frame loop draws without the light or an offer.
		r.draw(light())
		r.draw(scene{now: now, phase: "idle"})
		if !r.skipAt.Empty() {
			t.Errorf("%s: the button at %v outlived the light's page", panel.name, r.skipAt)
		}

		// The light's page again: the button is back where it was drawn.
		r.draw(light())
		if got, ok := r.skipTapped(mid); !ok || got.token != offer.token {
			t.Errorf("%s: the button did not come back on the light's page (%+v %v)", panel.name, got, ok)
		}

		if dir == "" {
			continue
		}
		img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
		newRenderer(img).draw(light())
		f, err := os.Create(filepath.Join(dir, "sunrise-skip-"+panel.name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// preview draws a page and writes it as a PNG in dir, for looking at with SHOW_PREVIEW set.
func preview(t *testing.T, dir, name string, draw func(*renderer)) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, showWide, showHigh))
	draw(newRenderer(img))
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// skipDisplay is a Show's Display with what a tap and the page it lands on need: an idle page, the
// first-run card out of the way, and a frame loop to wake.
func skipDisplay(t *testing.T) *Display {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Screen().Welcomed(true); err != nil {
		t.Fatal(err)
	}
	// The light as the page shows it: coming up, nothing held, so a tap is the page's and not a held
	// light's to let go. A card another test left up would take the tap for itself first.
	heldLight(t, time.Time{}, false, false)
	t.Cleanup(func() {
		notify.Get().Dismiss()
		remind.Get().Stop()
	})
	return &Display{r: newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh))), on: true,
		poke: make(chan struct{}, 1), view: voice.State{Phase: "idle"}}
}

// drawLightPage draws the light's page with the button offered there, and is where the finger lands.
func drawLightPage(t *testing.T, d *Display, now, at time.Time) image.Point {
	t.Helper()
	offer := skipOffer{token: alarm.SkipToken{ID: "wake", At: at}, label: "Wake up"}
	d.r.draw(scene{now: now, phase: "idle", sunrise: 0.4, skip: &offer})
	b := d.r.skipAt
	if b.Empty() {
		t.Fatal("the button was not drawn")
	}
	return b.Min.Add(image.Pt(b.Dx()/2, b.Dy()/2))
}

// A tap on the button leaves out the occurrence the frame drew — not whatever the alarms say by the
// time the finger lands — and says so at the foot of the page, for a while.
func TestATapOnTheButtonLeavesOutTheOccurrenceItDrew(t *testing.T) {
	now := time.Date(2026, time.October, 19, 6, 45, 0, 0, time.Local)
	at := now.Add(15 * time.Minute)
	d := skipDisplay(t)
	asked := recordingSkip(t, nil)
	mid := drawLightPage(t, d, now, at)

	// The alarms change between the frame and the finger: one moved, another offered. The drawn
	// occurrence is still the one the press is about.
	if err := config.Set().Alarms().SunriseMinutes(30); err != nil {
		t.Fatal(err)
	}
	if err := config.Set().Alarms().Put(config.Alarm{ID: "wake", Hour: 8, Days: config.DaysEvery, Label: "Wake up", On: true}); err != nil {
		t.Fatal(err)
	}
	offerOf(t, alarm.SkipToken{ID: "wake", At: at.Add(time.Hour)}, true)

	if !d.sunriseSkipTap(mid, now) {
		t.Fatal("a tap on the button was not the button's")
	}
	if len(*asked) != 1 || (*asked)[0].ID != "wake" || !(*asked)[0].At.Equal(at) {
		t.Errorf("asked to leave out %+v, want the drawn occurrence at %v", *asked, at)
	}
	if got, want := skipWhole(d.skipNoteNow(now)), "Alarm skipped: Wake up at "+clockText(at); got != want {
		t.Errorf("the foot says %q, want %q", got, want)
	}
	if got := d.skipNoteNow(now.Add(skipNoteFor + time.Second)); got.exists() {
		t.Errorf("the foot still says %q after its while", skipWhole(got))
	}
	if d.sunriseSkipTap(image.Pt(2, 2), now) {
		t.Error("a tap off the button was taken for it")
	}
	if len(*asked) != 1 {
		t.Errorf("a tap off the button left something out: %+v", *asked)
	}

	// The button gone from the screen is no button either.
	d.r.draw(scene{now: now, phase: "idle"})
	if d.sunriseSkipTap(mid, now) {
		t.Error("a tap where the button had been was taken for it")
	}

	// The clock page the tap leaves behind, saying what it did, for looking at with SHOW_PREVIEW set.
	if dir := os.Getenv("SHOW_PREVIEW"); dir != "" {
		preview(t, dir, "sunrise-skip-done", func(r *renderer) {
			r.draw(scene{now: now, phase: "idle", skipNote: d.skipNoteNow(now)})
		})
	}
}

// A press the alarm feature refuses, because the alarm moved or went between the frame and the
// finger, is still the button's press: the page says it was not left out, and the tap does not fall
// through to the clock under it — where it would start a turn and open the microphone.
func TestARefusedPressIsAnsweredAndGoesNoFurther(t *testing.T) {
	now := time.Date(2026, time.October, 19, 6, 45, 0, 0, time.Local)
	at := now.Add(15 * time.Minute)
	d := skipDisplay(t)
	asked := recordingSkip(t, errors.New("alarms: next rings at another time"))
	mid := drawLightPage(t, d, now, at)

	// A tap on the clock with the dashboard asked for is what a tap that got through would do.
	if err := config.Set().Screen().ClockTap("dashboard"); err != nil {
		t.Fatal(err)
	}
	if err := config.Set().Dashboard().Mode(config.DashboardDrawn); err != nil {
		t.Fatal(err)
	}

	tapAt(d, mid)
	if len(*asked) != 1 || !(*asked)[0].At.Equal(at) {
		t.Fatalf("the refused occurrence was not the one asked about: %+v", *asked)
	}
	if d.dash {
		t.Error("a refused press fell through to the clock and opened the dashboard")
	}
	if got, want := skipWhole(d.skipNoteNow(now)), "Not skipped: Wake up at "+clockText(at); got != want {
		t.Errorf("the foot says %q, want %q", got, want)
	}

	// The light's page still up, with the button and what the page says: the refused press answered
	// where the finger is, for looking at with SHOW_PREVIEW set.
	if dir := os.Getenv("SHOW_PREVIEW"); dir != "" {
		offer := skipOffer{token: alarm.SkipToken{ID: "wake", At: at}, label: "Wake up"}
		preview(t, dir, "sunrise-skip-refused", func(r *renderer) {
			r.draw(scene{now: now, phase: "idle", sunrise: 0.4, skip: &offer, skipNote: d.skipNoteNow(now)})
		})
	}

	// The same tap where there is no button is the clock's, which is the fall-through the button
	// takes out of its way.
	d.r.draw(scene{now: now, phase: "idle"})
	tapAt(d, mid)
	if !d.dash {
		t.Error("a tap where no button was drawn did not reach the clock")
	}
}

// A page of its own over the clock takes the tap before the button does, as every overlay does: the
// settings and the drawer are asked first, and a finger where the button was does not leave an alarm
// out behind them.
func TestAPageOverTheClockTakesTheTapFirst(t *testing.T) {
	now := time.Date(2026, time.October, 19, 6, 45, 0, 0, time.Local)
	at := now.Add(15 * time.Minute)
	for _, tc := range []struct {
		name string
		open func(d *Display)
	}{
		{"settings", func(d *Display) { d.sheet = true }},
		{"drawer", func(d *Display) { d.drawer = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := skipDisplay(t)
			asked := recordingSkip(t, nil)
			mid := drawLightPage(t, d, now, at)
			d.mu.Lock()
			tc.open(d)
			d.mu.Unlock()

			tapAt(d, mid)
			if len(*asked) != 0 {
				t.Errorf("a tap on the %s left an alarm out: %+v", tc.name, *asked)
			}

			// Put away, the same tap is the button's: the page over it was what took it.
			d.mu.Lock()
			d.sheet, d.drawer = false, false
			d.mu.Unlock()
			tapAt(d, mid)
			if len(*asked) != 1 || !(*asked)[0].At.Equal(at) {
				t.Errorf("with nothing over the page the tap did not leave the alarm out: %+v", *asked)
			}
		})
	}
}

// The button works the alarm feature as it is, not only the stand-ins the tests above use: the
// occurrence the frame drew is the one the config keeps, the alarm stays on with its days, and the
// page stops offering it. The engine's own rules are tested in feature/alarm; this is the wiring.
//
// It runs against the wall clock because the feature checks a request against the moment it is
// processed, not the one it was drawn at.
func TestTheButtonLeavesTheOccurrenceOutThroughTheAlarmFeature(t *testing.T) {
	now := time.Now()
	at := now.Add(15 * time.Minute)
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Alarms().SunriseMinutes(30); err != nil {
		t.Fatal(err)
	}
	if err := config.Set().Alarms().Put(config.Alarm{ID: "wake", Hour: at.Hour(), Minute: at.Minute(),
		Days: config.DaysEvery, Label: "Wake up", On: true}); err != nil {
		t.Fatal(err)
	}

	// The engine's own offer, and the light it reports, the way the frame loop reads them together.
	offer := sunriseSkip(now)
	if offer == nil {
		t.Skip("no alarm is coming up by this clock just now; the offer's rules are tested above")
	}

	d := &Display{r: newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh))), on: true,
		poke: make(chan struct{}, 1), view: voice.State{Phase: "idle"}}
	d.r.draw(scene{now: now, phase: "idle", sunrise: 0.4, skip: offer})
	b := d.r.skipAt
	if b.Empty() {
		t.Fatal("the button was not drawn")
	}
	if !d.sunriseSkipTap(b.Min.Add(image.Pt(b.Dx()/2, b.Dy()/2)), now) {
		t.Fatal("a tap on the button was not the button's")
	}

	c := config.Get().Alarms
	if len(c.Skipped) != 1 || c.Skipped[0].ID != "wake" || !c.Skipped[0].At.Equal(offer.token.At) {
		t.Fatalf("saved marks = %+v, want one for wake at %v", c.Skipped, offer.token.At)
	}
	if len(c.List) != 1 || !c.List[0].On || c.List[0].Days != config.DaysEvery {
		t.Errorf("leaving the occurrence out changed the alarm: %+v", c.List)
	}
	if got := sunriseSkip(now); got != nil {
		t.Errorf("the page still offers %+v after the occurrence was left out", *got)
	}
	if got, want := skipWhole(d.skipNoteNow(now)), "Alarm skipped: Wake up at "+clockText(offer.token.At); got != want {
		t.Errorf("the foot says %q, want %q", got, want)
	}
}

// The foot says nothing about the light: another alarm's window may still be open, and the sunrise may
// stay or go with the occurrence left out. What was promised is the occurrence, and that is what the
// line says.
func TestTheFootSaysWhatWasLeftOutAndNotWhatTheLightDid(t *testing.T) {
	now := time.Date(2026, time.October, 19, 6, 45, 0, 0, time.Local)
	at := now.Add(15 * time.Minute)
	offer := skipOffer{token: alarm.SkipToken{ID: "wake", At: at}, label: "Wake up"}
	for _, tc := range []struct {
		ok   bool
		want string
	}{
		{true, "Alarm skipped: Wake up at " + clockText(at)},
		{false, "Not skipped: Wake up at " + clockText(at)},
	} {
		got := skipWords(offer, tc.ok)
		if got != tc.want {
			t.Errorf("skipWords(%v) = %q, want %q", tc.ok, got, tc.want)
		}
		if strings.Contains(got, "light") {
			t.Errorf("the foot says %q, which is about the light", got)
		}
	}

	// An alarm with no label is still named by the word for one.
	if got, want := skipWords(skipOffer{token: offer.token}, true), "Alarm skipped: Alarm at "+clockText(at); got != want {
		t.Errorf("an unnamed alarm says %q, want %q", got, want)
	}
}

// The page's own words are the screen's catalog's: Spanish has words of its own and every other
// language says the English ones until a native speaker writes it, the complete catalogs carrying the
// English rather than a missing entry (lib/locale). One whole message per line, with the alarm and the
// hour as its arguments, so a language can put them where it says them. "This alarm", not
// "today's", and a line that names the alarm and the hour without claiming a day: the occurrence the
// alarm feature offers is the next one, which is tomorrow's when the light comes up before midnight, and
// the hour on the button says which moment is meant.
func TestTheButtonsWordsAreTheScreensLanguage(t *testing.T) {
	now := time.Date(2026, time.October, 19, 6, 45, 0, 0, time.Local)
	at := now.Add(15 * time.Minute)
	offer := skipOffer{token: alarm.SkipToken{ID: "wake", At: at}, label: "Wake up"}
	clock := clockText(at)
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { _ = config.Set().Screen().Language("") })
	for _, tc := range []struct{ lang, action, done, refused string }{
		{"", "Skip", "Alarm skipped: Wake up at " + clock, "Not skipped: Wake up at " + clock},
		{"en", "Skip", "Alarm skipped: Wake up at " + clock, "Not skipped: Wake up at " + clock},
		{"es", "Omitir", "Alarma omitida: Wake up a las " + clock, "No se pudo omitir: Wake up a las " + clock},
		// The rest say the English words until a native speaker writes theirs: the complete catalogs
		// carry them, and a language with no catalog of its own falls back to them (lib/locale).
		{"de", "Überspringen", "Alarm skipped: Wake up at " + clock, "Not skipped: Wake up at " + clock},
		{"fr", "Ignorer", "Alarm skipped: Wake up at " + clock, "Not skipped: Wake up at " + clock},
		{"it", "Salta", "Alarm skipped: Wake up at " + clock, "Not skipped: Wake up at " + clock},
		{"nl", "Skip", "Alarm skipped: Wake up at " + clock, "Not skipped: Wake up at " + clock},
		{"pt", "Skip", "Alarm skipped: Wake up at " + clock, "Not skipped: Wake up at " + clock},
		{"ru", "Skip", "Alarm skipped: Wake up at " + clock, "Not skipped: Wake up at " + clock},
		{"tr", "Atla", "Alarm skipped: Wake up at " + clock, "Not skipped: Wake up at " + clock},
		{"ja", "スキップ", "Alarm skipped: Wake up at " + clock, "Not skipped: Wake up at " + clock},
		{"xx", "Skip", "Alarm skipped: Wake up at " + clock, "Not skipped: Wake up at " + clock},
	} {
		if err := config.Set().Screen().Language(tc.lang); err != nil {
			t.Fatal(err)
		}
		if got := skipAction(); got != tc.action {
			t.Errorf("%q: the button says %q, want %q", tc.lang, got, tc.action)
		}
		if got := skipWords(offer, true); got != tc.done {
			t.Errorf("%q: a skipped occurrence says %q, want %q", tc.lang, got, tc.done)
		}
		if got := skipWords(offer, false); got != tc.refused {
			t.Errorf("%q: a refused press says %q, want %q", tc.lang, got, tc.refused)
		}
		// No day is claimed in any of them: what the page leaves out is the next occurrence.
		for _, got := range []string{skipAction(), skipWords(offer, true), skipWords(offer, false)} {
			if day := claimedDay(got); day != "" {
				t.Errorf("%q: %q claims a day (%q)", tc.lang, got, day)
			}
		}
	}
}

// claimedDay is the word for a day one of the page's lines would claim, or "" for a line that leaves
// the day unsaid.
func claimedDay(line string) string {
	low := strings.ToLower(line)
	for _, word := range []string{"today", "tomorrow", "hoy", "mañana", "heute", "morgen", "aujourd'hui",
		"demain", "oggi", "domani", "vandaag", "morgen", "hoje", "amanhã", "сегодня", "завтра", "今日", "明日"} {
		if strings.Contains(low, word) {
			return word
		}
	}
	return ""
}

// lightness is a color's relative luminance as the eye sees it, the sRGB one two colors' contrast ratio
// is read from: not readable.go's luma, which is a brightness for choosing ink.
func lightness(c color.RGBA) float64 {
	f := func(v uint8) float64 {
		x := float64(v) / 255
		if x <= 0.04045 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	return 0.2126*f(c.R) + 0.7152*f(c.G) + 0.0722*f(c.B)
}

// contrastRatio is the ratio between two of those: 1 for one color, 21 for black on white, and 4.5 is
// where text somebody has to read begins.
func contrastRatio(a, b float64) float64 {
	return (math.Max(a, b) + 0.05) / (math.Min(a, b) + 0.05)
}

// The answer to a press has to be read at the end of the light as well as at its beginning. The foot of
// the wake light page is drawn dark on pale, as the button above it is, rather than in the foot's amber,
// which the pale gold of the last minutes swallows: the line is the only answer a press gets, and the
// page it is drawn on is the only lamp in the room.
func TestTheFootsAnswerReadsAtTheEndOfTheLight(t *testing.T) {
	now := time.Date(2026, time.October, 19, 6, 59, 0, 0, time.Local)
	answer := answerOf(skipOffer{token: alarm.SkipToken{ID: "wake", At: now.Add(time.Minute)}, label: "Wake up"}, false)
	for _, panel := range showPanels() {
		img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
		r := newRenderer(img)
		// The last minute of the light, the palest the sky under the button ever gets.
		r.draw(scene{now: now, phase: "idle", sunrise: 1, skipNote: answer})
		// The foot: from under the button to the bottom edge, where the line is drawn.
		band := image.Rect(0, panel.high-r.s(64), panel.wide, panel.high)
		lo, hi := 1.0, 0.0
		for y := band.Min.Y; y < band.Max.Y; y++ {
			for x := band.Min.X; x < band.Max.X; x++ {
				l := lightness(img.RGBAAt(x, y))
				lo, hi = math.Min(lo, l), math.Max(hi, l)
			}
		}
		if got := contrastRatio(lo, hi); got < 4.5 {
			t.Errorf("%s: the foot's answer is drawn at %.1f:1 against the sky behind it", panel.name, got)
		}
	}

	// The last minute of the light, for looking at with SHOW_PREVIEW set.
	if dir := os.Getenv("SHOW_PREVIEW"); dir != "" {
		preview(t, dir, "sunrise-skip-answer", func(r *renderer) {
			r.draw(scene{now: now, phase: "idle", sunrise: 1, skipNote: answer})
		})
	}
}

// An alarm's name has no length limit, so the line in the foot is clipped to the room left beside the
// word at the right end rather than run off the page. It is the same line when the light ends and the
// clock comes back — where it usually lasts out its eight seconds — and it is still inside the margins
// there.
func TestTheFootsAnswerIsKeptOnThePageAndOutlastsTheLight(t *testing.T) {
	now := time.Date(2026, time.October, 19, 6, 59, 0, 0, time.Local)
	at := now.Add(time.Minute)
	long := answerOf(skipOffer{token: alarm.SkipToken{ID: "wake", At: at},
		label: strings.Repeat("A very long alarm name ", 12)}, true)
	for _, panel := range showPanels() {
		// The light's page: the dark the line is drawn on keeps inside the page's margins.
		img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
		r := newRenderer(img)
		r.draw(scene{now: now, phase: "idle", sunrise: 1, skipNote: long})
		band := image.Rect(0, panel.high-r.s(64), panel.wide, panel.high)
		dark := -1
		for y := band.Min.Y; y < band.Max.Y; y++ {
			for x := band.Min.X; x < band.Max.X; x++ {
				if lightness(img.RGBAAt(x, y)) < 0.05 {
					dark = max(dark, x)
				}
			}
		}
		if dark < 0 {
			t.Fatalf("%s: the foot's answer is not drawn on the light's page", panel.name)
		}
		if dark > panel.wide-r.margin {
			t.Errorf("%s: the foot's answer reaches %d, past the margin at %d", panel.name, dark, panel.wide-r.margin)
		}

		// The clock the light ends on: the same line, in the foot's amber, inside the same margins.
		img = image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
		r = newRenderer(img)
		r.draw(scene{now: now, phase: "idle", skipNote: long})
		band = image.Rect(0, panel.high-r.s(46), panel.wide, panel.high)
		seen := false
		for y := band.Min.Y; y < band.Max.Y; y++ {
			for x := band.Min.X; x < band.Max.X; x++ {
				if lightness(img.RGBAAt(x, y)) <= 0.15 {
					continue
				}
				if x > panel.wide-r.margin {
					t.Errorf("%s: the foot's answer on the clock's page reaches %d, past the margin at %d",
						panel.name, x, panel.wide-r.margin)
				}
				if x < panel.wide/2 {
					seen = true
				}
			}
		}
		if !seen {
			t.Errorf("%s: the foot's answer is gone from the clock's page the light ends on", panel.name)
		}
	}
}

// The bar along the foot covers the bottom of the page and the button's lowest line with it: what the
// bar covers cannot be tapped, as the camera page's controls have it, so a frame that draws the bar
// draws no button and publishes nowhere for a tap to land. The frame that draws the page again brings
// the button back where it was.
func TestTheVolumeBarLeavesNothingToTapUnderIt(t *testing.T) {
	now := time.Date(2026, time.October, 19, 6, 45, 0, 0, time.Local)
	at := now.Add(15 * time.Minute)
	offer := skipOffer{token: alarm.SkipToken{ID: "wake", At: at}, label: "Wake up"}
	light := scene{now: now, phase: "idle", sunrise: 0.4, skip: &offer}
	for _, panel := range showPanels() {
		r := newRenderer(image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high)))
		r.draw(light)
		b := r.skipAt
		if b.Empty() {
			t.Fatalf("%s: the button was not drawn on the light's page", panel.name)
		}
		if top := panel.high - r.s(volumeBarH); top >= b.Max.Y {
			t.Fatalf("%s: the bar at %d does not reach the button at %v", panel.name, top, b)
		}
		mid := b.Min.Add(image.Pt(b.Dx()/2, b.Dy()/2))

		withBar := light
		withBar.showVolume, withBar.volume = true, 6
		r.draw(withBar)
		if !r.skipAt.Empty() {
			t.Errorf("%s: the button at %v is still there to tap under the volume bar", panel.name, r.skipAt)
		}
		if _, ok := r.skipTapped(mid); ok {
			t.Errorf("%s: a tap on the button the bar covers was taken for it", panel.name)
		}

		r.draw(light)
		if got, ok := r.skipTapped(mid); !ok || got.token != offer.token {
			t.Errorf("%s: the button did not come back when the bar went (%+v %v)", panel.name, got, ok)
		}
	}
}

// The occurrence the engine offers is the open ramp furthest along, and the page is that alarm's: at
// 6:57 the light before a 7:10 alarm with thirty minutes has seventeen of them behind it, and the light
// before a 7:00 alarm with five has two, so the engine offers the 7:10 alarm and the page draws it while
// the screen shows its ramp. The shorter light's ramp is not the one offered, and the page offers
// nothing there rather than a button about an alarm the sunrise is not.
func TestTheLightPageOffersTheRampTheEngineChose(t *testing.T) {
	now := time.Date(2026, time.October, 19, 6, 57, 0, 0, time.Local)
	early := time.Date(2026, time.October, 19, 7, 0, 0, 0, time.Local)
	late := time.Date(2026, time.October, 19, 7, 10, 0, 0, time.Local)
	skipState(t, 30)
	for _, al := range []config.Alarm{
		{ID: "short", Hour: 7, Minute: 0, Sunrise: 5, Days: config.DaysEvery, Label: "Short light", On: true},
		{ID: "long", Hour: 7, Minute: 10, Sunrise: 30, Days: config.DaysEvery, Label: "Long light", On: true},
	} {
		if err := config.Set().Alarms().Put(al); err != nil {
			t.Fatal(err)
		}
	}
	noHold(t)

	realLight(t)
	if got := sunriseProgress(now); math.Abs(got-17.0/30.0) > 1e-9 {
		t.Fatalf("the engine's displayed ramp is %.9f, want the longer ramp %.9f", got, 17.0/30.0)
	}
	offer := sunriseSkip(now)
	if offer == nil || offer.token.ID != "long" || offer.label != "Long light" || !offer.token.At.Equal(late) {
		t.Fatalf("offered %+v, want the long light's occurrence at %v", offer, late)
	}

	lightAt(t, 2.0/5.0)
	if got := sunriseSkip(now); got != nil {
		t.Errorf("the short light's ramp offered %+v, want nothing", *got)
	}

	// The engine's own answer is the same occurrence the page drew, and the other alarm is still open.
	token, ok := alarm.Get().Skippable(now)
	if !ok || token.ID != "long" || !token.At.Equal(late) {
		t.Errorf("the engine offers %+v %v, want long at %v", token, ok, late)
	}
	if at, ok := alarm.Get().Skippable(now); ok && at.At.Equal(early) {
		t.Errorf("the engine offered the earlier alarm at %v", at.At)
	}
}

// The answer, like the rest of the page's words, is read when the frame is drawn: a language changed
// after the press is the language the line is drawn in, and switching back draws it as it was. The
// answer still goes after its eight seconds whatever the language.
func TestTheAnswerIsDrawnInTheLanguageOfItsFrame(t *testing.T) {
	now := time.Date(2026, time.October, 19, 6, 45, 0, 0, time.Local)
	at := now.Add(15 * time.Minute)
	d := skipDisplay(t)
	t.Cleanup(func() { _ = config.Set().Screen().Language("") })
	recordingSkip(t, nil)
	mid := drawLightPage(t, d, now, at)
	if !d.sunriseSkipTap(mid, now) {
		t.Fatal("a tap on the button was not the button's")
	}
	for _, tc := range []struct{ lang, result string }{
		{"", "Alarm skipped:"},
		{"es", "Alarma omitida:"},
		{"", "Alarm skipped:"},
	} {
		if err := config.Set().Screen().Language(tc.lang); err != nil {
			t.Fatal(err)
		}
		answer := d.skipNoteNow(now)
		if !answer.exists() {
			t.Fatalf("%q: the answer has already gone", tc.lang)
		}
		if got := skipWhole(answer); !strings.HasPrefix(got, tc.result) {
			t.Errorf("%q: the answer says %q, want %q first", tc.lang, got, tc.result)
		}
	}
	if err := config.Set().Screen().Language("es"); err != nil {
		t.Fatal(err)
	}
	if got := d.skipNoteNow(now.Add(skipNoteFor + time.Second)); got.exists() {
		t.Errorf("the answer outlasted its while in Spanish: %q", skipWhole(got))
	}
}
