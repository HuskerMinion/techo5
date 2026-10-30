//go:build !dot && !spot

package display

import (
	"image"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/alarm"
)

// realLight makes the page ask the alarm feature's own rules and ramp, over the config the test set,
// rather than a stand-in: the offer a light-only alarm gets is the engine's, and what is this page's
// is only whether the light on the screen is that alarm's own.
func realLight(t *testing.T) {
	t.Helper()
	wasOffer, wasProgress := skippable, sunriseProgress
	t.Cleanup(func() { skippable, sunriseProgress = wasOffer, wasProgress })
	a := &alarm.Alarms{}
	skippable = func(now time.Time) (alarm.SkipToken, bool) { return a.Skippable(now) }
	sunriseProgress = func(now time.Time) float64 { return a.SunriseProgress(now) }
}

// A light-only alarm lights the room as any alarm does, and that ramp is a page to leave that one
// morning out from as well. The minutes are the alarm's own light — its own, the device's default, or
// the fallback — and both the engine's offer and the ramp on the page are measured in those, so the
// button is about the alarm the sunrise is.
func TestTheLightPageOffersALightOnlyAlarmsOwnRamp(t *testing.T) {
	for _, tc := range []struct {
		name           string
		def, own, want int
	}{
		{"its own light", 15, 20, 20},
		{"the device's default", 15, config.SunriseOff, 15},
		{"the fallback with no default", 0, config.SunriseOff, config.LightOnlyMinutes},
	} {
		t.Run(tc.name, func(t *testing.T) {
			skipState(t, tc.def)
			at := time.Date(2026, time.October, 19, 7, 0, 0, 0, time.Local)
			al := config.Alarm{ID: "light", Hour: 7, Days: config.DaysEvery, Label: "Morning light",
				On: true, LightOnly: true, Sunrise: tc.own}
			if err := config.Set().Alarms().Put(al); err != nil {
				t.Fatal(err)
			}
			if got := config.Get().Alarms.SunriseFor(al); got != tc.want {
				t.Fatalf("the light-only alarm's light is %d minutes, want %d", got, tc.want)
			}
			realLight(t)
			noHold(t)

			// Five minutes before its time, the light on the page is that alarm's ramp, so the button
			// is offered for its morning.
			now := at.Add(-5 * time.Minute)
			got := sunriseSkip(now)
			if got == nil || got.token.ID != "light" || !got.token.At.Equal(at) || got.label != "Morning light" {
				t.Fatalf("the light's page offered %+v, want light at %v with its label", got, at)
			}

			// A minute before its window opens there is no ramp and nothing to leave out.
			if got := sunriseSkip(at.Add(-time.Duration(tc.want+1) * time.Minute)); got != nil {
				t.Errorf("offered %+v before the light-only alarm's window opened", *got)
			}
			// At its own time the light is over and the night ends: nothing to leave out.
			if got := sunriseSkip(at); got != nil {
				t.Errorf("offered %+v at the alarm's own time", *got)
			}
			// A light held at full is not a ramp being offered, even where one is still open.
			heldLight(t, now.Add(40*time.Minute), false, true)
			if got := sunriseSkip(now); got != nil {
				t.Errorf("offered %+v while the light was held at full", *got)
			}
		})
	}
}

// A tap on that button asks to leave out exactly the light-only morning the frame drew, at the foot
// of the page in the words the button already uses, and the button is gone once that morning is left
// out: the light-only alarm has no other ramp to offer until its next occurrence's window opens.
func TestATapOnALightOnlyAlarmsButtonLeavesOutItsMorning(t *testing.T) {
	at := time.Date(2026, time.October, 19, 7, 0, 0, 0, time.Local)
	now := at.Add(-5 * time.Minute)
	d := skipDisplay(t)
	if err := config.Set().Alarms().SunriseMinutes(15); err != nil {
		t.Fatal(err)
	}
	if err := config.Set().Alarms().Put(config.Alarm{ID: "light", Hour: 7, Days: config.DaysEvery,
		Label: "Morning light", On: true, LightOnly: true, Sunrise: 20}); err != nil {
		t.Fatal(err)
	}
	realLight(t)
	noHold(t)

	offer := sunriseSkip(now)
	if offer == nil {
		t.Fatal("nothing was offered while the light-only alarm's light came up")
	}
	d.r.draw(scene{now: now, phase: "idle", sunrise: sunriseProgress(now), skip: offer})
	b := d.r.skipAt
	if b.Empty() {
		t.Fatal("the button was not drawn on the light-only alarm's page")
	}
	mid := b.Min.Add(image.Pt(b.Dx()/2, b.Dy()/2))
	asked := recordingSkip(t, nil)
	if !d.sunriseSkipTap(mid, now) {
		t.Fatal("a tap on the button was not the button's")
	}
	if len(*asked) != 1 || (*asked)[0].ID != "light" || !(*asked)[0].At.Equal(at) {
		t.Errorf("asked to leave out %+v, want the light-only morning at %v", *asked, at)
	}
	if got, want := skipWhole(d.skipNoteNow(now)), "Alarm skipped: Morning light at "+clockText(at); got != want {
		t.Errorf("the foot says %q, want %q", got, want)
	}

	// Saved as the engine saves it, that morning is left out: the ramp is gone, and so is the button.
	if err := config.Set().Alarms().ChangeStrict(func(c *config.Alarms) error {
		c.Skipped = append(c.Skipped, config.Skip{ID: "light", At: at})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	d.r.draw(scene{now: now, phase: "idle", skip: sunriseSkip(now)})
	if !d.r.skipAt.Empty() {
		t.Errorf("the button at %v is still there for a morning left out", d.r.skipAt)
	}
}
