//go:build !dot && !spot

package display

import (
	"bytes"
	"errors"
	"image"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/alarm"
	"github.com/HuskerMinion/techo5/echod/internal/feature/notify"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// skipAt is a daily 7:00 in the morning and the frame at 6:45, inside the half hour of light before it.
func skipAt(t *testing.T) (now, ring time.Time) {
	t.Helper()
	loc := time.Local
	return time.Date(2026, time.October, 19, 6, 45, 0, 0, loc), time.Date(2026, time.October, 19, 7, 0, 0, 0, loc)
}

// skipState points the config store at a file of this test's own, with a device that has been set up.
func skipState(t *testing.T) {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	notify.Get().Dismiss() // a card another test left up would take the first tap, as it should
	if err := config.Set().Screen().Welcomed(true); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		notify.Get().Dismiss()
		notify.SetNight(func(time.Time) bool { return false })
	})
}

// skipStubs puts the light and the engine where a test needs them: the progress the page draws, no
// light held, the occurrence offered, and what a press on it answers.
func skipStubs(t *testing.T, progress float64, token *alarm.SkipToken, err error) *int {
	t.Helper()
	wasProgress, wasHold, wasSkippable, wasSkip := sunriseProgress, sunriseHold, skippable, skipOccurrence
	t.Cleanup(func() {
		sunriseProgress, sunriseHold, skippable, skipOccurrence = wasProgress, wasHold, wasSkippable, wasSkip
	})
	sunriseProgress = func(time.Time) float64 { return progress }
	sunriseHold = func(time.Time) (time.Time, bool, bool) { return time.Time{}, false, false }
	skippable = func(time.Time) (alarm.SkipToken, bool) {
		if token == nil {
			return alarm.SkipToken{}, false
		}
		return *token, true
	}
	presses := 0
	skipOccurrence = func(string, time.Time) error {
		presses++
		return err
	}
	return &presses
}

// skipFrame draws the light's page at 6:45 and answers where the button was drawn.
func skipFrame(t *testing.T, d *Display, now time.Time, s scene) image.Rectangle {
	t.Helper()
	s.now, s.phase, s.sunrise = now, "idle", 0.5
	d.r.draw(s)
	return d.r.skipAt
}

// skipLight is the device's default wake light, half an hour, which is the window an alarm that
// chooses none of its own follows.
func skipLight(t *testing.T) {
	t.Helper()
	if err := config.Set().Alarms().SunriseMinutes(30); err != nil {
		t.Fatal(err)
	}
}

func skipPut(t *testing.T, al config.Alarm) {
	t.Helper()
	if err := config.Set().Alarms().Put(al); err != nil {
		t.Fatal(err)
	}
}

// A press on the button the frame drew leaves that occurrence out, says what came of it in the
// device's own note — the alarm's name, and the result with the moment, whole — and has that note on
// the screen at night at once, without touching anything else that may be up.
func TestAPressOnTheLightSButtonLeavesTheOccurrenceOut(t *testing.T) {
	skipState(t)
	was := clock24.Load()
	t.Cleanup(func() { clock24.Store(was) })
	clock24.Store(false)
	now, ring := skipAt(t)
	d := noticeDisplay(t, showWide, showHigh)
	skipLight(t)
	skipPut(t, config.Alarm{ID: "wake", Hour: 7, Minute: 0, Days: config.DaysEvery, Label: "Wake up", On: true})
	token := alarm.SkipToken{ID: "wake", At: ring}
	presses := skipStubs(t, 0.5, &token, nil)

	b := skipFrame(t, d, now, scene{})
	if b.Empty() {
		t.Fatal("the light's page drew no button")
	}
	if offer, ok := d.r.skipTapped(image.Pt(b.Min.X+b.Dx()/2, b.Min.Y+b.Dy()/2)); !ok || offer.token != token || offer.label != "Wake up" {
		t.Fatalf("the button does not carry the occurrence it was drawn for: %+v %v", offer, ok)
	}
	tapAt(d, image.Pt(b.Min.X+b.Dx()/2, b.Min.Y+b.Dy()/2))
	if *presses != 1 {
		t.Fatalf("the press on the button asked the engine %d times, want 1", *presses)
	}
	up, showing := notify.Get().Showing()
	if !showing || !up.Own {
		t.Fatalf("the press was not answered with the device's own note: %+v %v", up, showing)
	}
	if up.Title != "Wake up" {
		t.Errorf("the note's heading is %q, want the alarm's name", up.Title)
	}
	if !strings.Contains(up.Message, "Skipped") || !strings.Contains(up.Message, "7:00 AM") {
		t.Errorf("the note says %q, want the result and the moment", up.Message)
	}
	// At night the note waits for a touch; the press that made it is that touch, so it is released.
	d.mu.Lock()
	seen := d.notifySeen
	d.mu.Unlock()
	if seen != up.ID {
		t.Errorf("the note the press made was not released for the night: seen %q, note %q", seen, up.ID)
	}
	night := scene{showNotification: true, notification: up, redClock: true}
	d.gateNotice(&night, true)
	if !night.showNotification || night.redClock {
		t.Error("the answer to the press was hidden by the night gate")
	}
	night.notification.ID = "another-notification"
	d.gateNotice(&night, true)
	if night.showNotification {
		t.Error("releasing the answer also released another notification")
	}
	if d.view.Phase != "idle" {
		t.Errorf("the press started a turn (%q) instead of stopping at the button", d.view.Phase)
	}
}

// A press the engine refuses is still the button's: it says it was not left out rather than falling
// through to the clock below it.
func TestARefusedPressIsAnsweredAndGoesNoFurther(t *testing.T) {
	skipState(t)
	now, ring := skipAt(t)
	d := noticeDisplay(t, showWide, showHigh)
	skipLight(t)
	skipPut(t, config.Alarm{ID: "wake", Hour: 7, Minute: 0, Days: config.DaysEvery, Label: "Wake up", On: true})
	token := alarm.SkipToken{ID: "wake", At: ring}
	presses := skipStubs(t, 0.5, &token, errors.New("the alarm changed"))

	b := skipFrame(t, d, now, scene{})
	if b.Empty() {
		t.Fatal("the light's page drew no button")
	}
	tapAt(d, image.Pt(b.Min.X+b.Dx()/2, b.Min.Y+b.Dy()/2))
	if *presses != 1 {
		t.Fatalf("the press asked the engine %d times, want 1", *presses)
	}
	up, _ := notify.Get().Showing()
	if !strings.Contains(up.Message, "Not skipped") || !strings.Contains(up.Message, "7:00 AM") {
		t.Errorf("the refusal is not on the card: %+v", up)
	}
	if d.view.Phase != "idle" {
		t.Errorf("the refused press fell through to a turn (%q)", d.view.Phase)
	}
}

// Nothing drawn over the page is anything to press: the frame publishes no button, where the page
// under it drew one, and a card dismissed after the frame was drawn leaves no button behind it.
func TestAFrameWithSomethingOverThePageOffersNoButton(t *testing.T) {
	skipState(t)
	now, ring := skipAt(t)
	d := noticeDisplay(t, showWide, showHigh)
	skipLight(t)
	skipPut(t, config.Alarm{ID: "wake", Hour: 7, Minute: 0, Days: config.DaysEvery, Label: "Wake up", On: true})
	token := alarm.SkipToken{ID: "wake", At: ring}
	presses := skipStubs(t, 0.5, &token, nil)
	visible := skipFrame(t, d, now, scene{})
	if visible.Empty() {
		t.Fatal("the clear page drew no button")
	}
	mid := visible.Min.Add(image.Pt(visible.Dx()/2, visible.Dy()/2))

	for _, tc := range []struct {
		name string
		s    scene
	}{
		{"notification", scene{showNotification: true}},
		{"reminder", scene{showReminder: true}},
		{"popup", scene{popup: &hass.Event{}}},
		{"announcement", scene{showAnnouncement: true}},
		{"recording", scene{announceRecording: true}},
		{"drawer", scene{showDrawer: true}},
		{"volume", scene{showVolume: true}},
		{"update", scene{updateActive: true}},
		{"video", scene{showVideo: true, videoLive: true}},
	} {
		if b := skipFrame(t, d, now, tc.s); !b.Empty() {
			t.Errorf("%s: a button was published under it: %v", tc.name, b)
		}
	}
	// A card dismissed between the frame and the tap: the frame published no button, so the tap is not
	// one, even at the place the page under the card drew it.
	if b := skipFrame(t, d, now, scene{showNotification: true}); !b.Empty() {
		t.Fatalf("a button was published under a card: %v", b)
	}
	tapAt(d, mid)
	if *presses != 0 {
		t.Errorf("a tap after the card went found a button it never saw")
	}
}

// The page kept under Rainbow is not kept while it is the light before an alarm, a card over it, or
// the device's own answer to a press: those come and go by themselves, and a kept copy of one would
// put an old button or an old line back on the screen after it had gone.
func TestWhatIsNotKeptUnderRainbow(t *testing.T) {
	skipState(t)
	for _, tc := range []struct {
		name  string
		s     scene
		quiet bool
	}{
		{"clock", scene{}, true},
		{"sunrise", scene{sunrise: 0.4}, false},
		{"card", scene{showNotification: true}, false},
		{"drawer", scene{showDrawer: true}, false},
		{"volume", scene{showVolume: true}, false},
		{"camera", scene{showCamera: true}, false},
	} {
		if _, quiet := rainbowKeyOf(tc.s); quiet != tc.quiet {
			t.Errorf("%s: the page is keepable=%v, want %v", tc.name, quiet, tc.quiet)
		}
	}
	// The device's own note is a card in the scene, and a card is not kept (the row above): the bypass
	// reads the scene alone, so no notification identity is read while drawing.
	notify.Get().Note("Wake up", "Skipped: the alarm at 7:00 AM")
	if _, quiet := rainbowKeyOf(scene{showNotification: true}); quiet {
		t.Error("the device's own note was keepable under Rainbow")
	}
}

// The button is drawn at every text size the settings offer, on every panel, inside the room between
// the date and the foot; at the largest hand-edited sizes, where that room is not there, it is left out
// rather than drawn over the clock or the foot.
func TestTheButtonFitsThePanelAtEveryTextSize(t *testing.T) {
	skipState(t)
	now, ring := skipAt(t)
	skipLight(t)
	skipPut(t, config.Alarm{ID: "wake", Hour: 7, Minute: 0, Days: config.DaysEvery, Label: "Wake up", On: true})
	token := alarm.SkipToken{ID: "wake", At: ring}
	skipStubs(t, 0.5, &token, nil)
	for _, panel := range showPanels() {
		for _, size := range append(append([]int(nil), textSteps...), 50, 200) {
			if err := config.Set().Screen().TextSize(size); err != nil {
				t.Fatal(err)
			}
			d := &Display{r: newRenderer(image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high)))}
			b := skipFrame(t, d, now, scene{})
			if size <= 125 {
				if b.Empty() {
					t.Errorf("%s/%d%%: the button was not drawn", panel.name, size)
					continue
				}
				date := panel.high/2 - d.r.s(56) + d.r.s(64) + d.r.small.Metrics().Descent.Ceil()
				foot := panel.high - d.r.s(24) - d.r.tiny.Metrics().Ascent.Ceil()
				if b.Min.Y < date || b.Max.Y > foot {
					t.Errorf("%s/%d%%: the button %v runs into the date (to %d) or the foot (from %d)",
						panel.name, size, b, date, foot)
				}
				if b.Min.X < 0 || b.Max.X > panel.wide {
					t.Errorf("%s/%d%%: the button runs off the panel: %v", panel.name, size, b)
				}
			} else if !b.Empty() {
				date := panel.high/2 - d.r.s(56) + d.r.s(64) + d.r.small.Metrics().Descent.Ceil()
				if b.Min.Y < date {
					t.Errorf("%s/%d%%: the button %v is drawn over the clock and date", panel.name, size, b)
				}
			}
		}
	}
}

// The engine really is what offers the occurrence and what writes it down: a normal repeating alarm
// is offered from inside its own light, the press marks that one instant in the store, and the alarm
// itself is untouched. A light-only alarm, which is only a lamp, is not offered at all.
//
// The alarm is fifteen minutes ahead of the clock the test runs on, so the engine's own clock is inside
// the light it would come up in; the light and the engine are the real ones here, not the stubs.
func TestTheRealEngineOffersAndMarksANormalAlarmOnly(t *testing.T) {
	skipState(t)
	d := noticeDisplay(t, showWide, showHigh)
	path := filepath.Join(t.TempDir(), "state.json")
	config.Use(path)
	if err := config.Set().Screen().Welcomed(true); err != nil {
		t.Fatal(err)
	}
	if err := config.Set().Screen().ClockTap("nothing"); err != nil {
		t.Fatal(err)
	}
	skipLight(t)
	now := time.Now()
	ring := now.Add(15 * time.Minute).Truncate(time.Minute)
	skipPut(t, config.Alarm{ID: "wake", Hour: ring.Hour(), Minute: ring.Minute(), Days: config.DaysEvery, Label: "Wake up", On: true})
	if alarm.Get().SunriseProgress(now) <= 0 {
		t.Fatalf("the alarm is not inside its light: %v", alarm.Get().SunriseProgress(now))
	}

	b := skipFrame(t, d, now, scene{})
	if b.Empty() {
		t.Fatal("the light's page drew no button for a normal alarm inside its light")
	}
	mid := b.Min.Add(image.Pt(b.Dx()/2, b.Dy()/2))
	// Dismiss a real card between frames. The old button's exact position must save nothing,
	// including after a reload, until a new clear frame actually draws it again.
	notify.Get().Note("Fictional card", "A card dismissal is not an alarm skip.")
	n, up := notify.Get().Card()
	if !up {
		t.Fatal("no notification card")
	}
	skipFrame(t, d, now, scene{showNotification: true, notification: n})
	notify.Get().Dismiss()
	tapAt(d, mid)
	config.Use(path)
	if marks := config.Get().Alarms.Skipped; len(marks) != 0 {
		t.Fatalf("a tap on the covered frame persisted a hidden skip: %+v", marks)
	}
	if b := skipFrame(t, d, now, scene{}); b.Empty() {
		t.Fatal("the clear redraw did not restore the button")
	}
	tapAt(d, mid)
	config.Use(path)
	var marked []config.Skip
	for _, sk := range config.Get().Alarms.Skipped {
		if sk.ID == "wake" {
			marked = append(marked, sk)
		}
	}
	if len(marked) != 1 || !marked[0].At.Equal(ring) {
		t.Fatalf("the store has %+v, want one mark of wake at %v", config.Get().Alarms.Skipped, ring)
	}
	al := config.Get().Alarms
	i := -1
	for n, a := range al.List {
		if a.ID == "wake" {
			i = n
		}
	}
	if i < 0 || !al.List[i].On || al.List[i].Days != config.DaysEvery || al.List[i].Hour != ring.Hour() {
		t.Fatalf("leaving the occurrence out changed the alarm: %+v", al.List[i])
	}

	// The same engine, for a light-only alarm: nothing offered, so nothing drawn and nothing pressable.
	if err := config.Set().Alarms().Delete("wake"); err != nil {
		t.Fatal(err)
	}
	skipPut(t, config.Alarm{ID: "lamp", Hour: ring.Hour(), Minute: ring.Minute(), Days: config.DaysEvery, Label: "Lamp", On: true, LightOnly: true})
	if b := skipFrame(t, d, now, scene{}); !b.Empty() {
		t.Errorf("a light-only alarm was offered a way out of its ring: %v", b)
	}
}

// Real Rainbow frames must hide the button through entry/fade, then restore it; a result card
// appearing and disappearing within the same second must not leave its pixels in the kept page.
func TestRainbowFramesKeepOnlyTheVisibleSkipAndResult(t *testing.T) {
	if !hasRainbow {
		t.Skip("no Rainbow on this device")
	}
	skipState(t)
	skipLight(t)
	if err := config.Set().Screen().TurnStyle("rainbow"); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.October, 31, 15, 0, 0, 0, time.Local)
	token := alarm.SkipToken{ID: "wake", At: now.Add(15 * time.Minute)}
	skipPut(t, config.Alarm{ID: "wake", Hour: 15, Minute: 15, Days: config.DaysEvery, On: true})
	skipStubs(t, 0.5, &token, nil)
	sunriseProgress = func(at time.Time) float64 {
		return 1 - float64(token.At.Sub(at))/float64(30*time.Minute)
	}
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	light := scene{now: now, phase: "idle", sunrise: 0.5}
	r.draw(light)
	if r.skipAt.Empty() {
		t.Fatal("idle Rainbow hid the button")
	}
	turn := light
	turn.now, turn.cue, turn.reply, turn.since = now.Add(100*time.Millisecond), "replying", "Fictional answer", now
	r.draw(turn)
	if !r.skipAt.Empty() || !r.skipDrawn.Empty() {
		t.Fatal("a button was drawn under Rainbow")
	}
	for i := 1; i <= 40; i++ {
		light.now = turn.now.Add(time.Duration(i) * 200 * time.Millisecond)
		r.draw(light)
		if r.rainbow.Busy() {
			if !r.skipAt.Empty() {
				t.Fatal("a fading Rainbow left an actionable button")
			}
			continue
		}
		// Finish can complete the fade after the page was drawn with it still covering the
		// button. The next clear frame, not that final covered frame, must offer it again.
		light.now = light.now.Add(200 * time.Millisecond)
		r.draw(light)
		if r.rainbow.Busy() || r.skipAt.Empty() {
			t.Fatal("the first clear frame after the fade did not restore the button")
		}
		break
	}
	if r.rainbow.Busy() {
		t.Fatal("Rainbow never finished fading")
	}
	page := scene{now: now.Add(time.Minute), phase: "idle", cue: "listening", since: now.Add(time.Minute)}
	r.draw(page)
	if !r.rainbowPageOK {
		t.Fatal("the plain page was not kept")
	}
	card := page
	card.now = page.now.Add(50 * time.Millisecond)
	card.showNotification = true
	card.notification = notify.Notification{ID: "result", Own: true, Title: "Wake up", Message: "Skipped: the alarm at 15:15"}
	r.draw(card)
	if r.rainbowPageOK || !r.skipAt.Empty() {
		t.Fatal("a result-card frame kept stale pixels or a skip target")
	}
	page.now = page.now.Add(75 * time.Millisecond)
	r.draw(page)
	fresh := newRenderer(image.NewRGBA(r.dst.Bounds()))
	fresh.draw(page)
	if !r.rainbowPageOK || !fresh.rainbowPageOK || !bytes.Equal(r.rainbowPage.Pix, fresh.rainbowPage.Pix) {
		t.Fatal("the dismissed card remained in the same-second kept page")
	}
}
