//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/alarm"
	"github.com/HuskerMinion/techo5/echod/internal/feature/notify"
)

// nightAlarmFrame is a night clock frame on a Show 5 at 3:47 on a Monday with the next alarm at 7:30,
// carrying the chips or the Next up card along the foot as foot says ("", "chips" or "card").
func nightAlarmFrame(style, foot string) scene {
	at := time.Date(2026, 11, 2, 3, 47, 0, 0, time.Local)
	s := scene{now: at, phase: "idle", redClock: true, redStyle: style, nightDim: true}
	if foot != "" {
		s = nightInfoFrame(at, style, true, foot == "chips")
	}
	s.nightAlarm = true
	s.alarms.Next = &alarm.Upcoming{Key: "a1", At: time.Date(2026, 11, 2, 7, 30, 0, 0, time.Local)}
	return s
}

// The line says the time as the clock is set, 12 or 24 hours, with the day only when the alarm is not
// today or tomorrow, and the date as well past a week.
func TestNightAlarmText(t *testing.T) {
	was := clock24.Load()
	t.Cleanup(func() { clock24.Store(was) })
	now := time.Date(2026, 11, 2, 23, 10, 0, 0, time.Local) // a Monday
	at := func(day, h, m int) *alarm.Upcoming {
		return &alarm.Upcoming{At: time.Date(2026, 11, day, h, m, 0, 0, time.Local)}
	}
	for _, c := range []struct {
		next   *alarm.Upcoming
		h24    bool
		lang   string
		want   string
		reason string
	}{
		{nil, false, "", "", "no alarm"},
		{at(2, 23, 45), false, "", "Alarm 11:45 PM", "later today"},
		{at(3, 7, 30), false, "", "Alarm 7:30 AM", "tomorrow"},
		{at(3, 7, 30), true, "", "Alarm 07:30", "tomorrow on a 24-hour clock"},
		{at(4, 6, 5), false, "", "Alarm Wed 6:05 AM", "the day after tomorrow"},
		{at(8, 9, 0), true, "", "Alarm Sun 09:00", "the end of the week"},
		{at(9, 7, 30), false, "", "Alarm Mon, Nov 9 7:30 AM", "a week on"},
		{&alarm.Upcoming{At: time.Date(2026, 11, 2, 23, 19, 0, 0, time.Local), Snoozed: true}, false, "",
			"Snoozed until 11:19 PM", "a snooze"},
		{at(3, 7, 30), true, "de", "Wecker 07:30", "in German"},
	} {
		clock24.Store(c.h24)
		if got := nightAlarmText(c.next, now, c.lang); got != c.want {
			t.Errorf("%s: %q, want %q", c.reason, got, c.want)
		}
	}
}

// With the setting off, or on with no alarm, the night clock is exactly the clock it was, on every
// night clock style, with or without the foot.
func TestNightAlarmOffIsTheClockAsItWas(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	previewFont(t)
	for _, style := range []string{nightStylePlain, nightStyleLED, nightStyleFlip} {
		for _, foot := range []string{"", "chips", "card"} {
			off := nightAlarmFrame(style, foot)
			off.nightAlarm = false
			alone := drawNight(showWide, showHigh, off)
			for name, s := range map[string]scene{"off": off, "no alarm": nightAlarmFrame(style, foot)} {
				if name == "no alarm" {
					s.alarms.Next = nil
				}
				if got := drawNight(showWide, showHigh, s); !samePixels(got, alone) {
					t.Errorf("%q foot %q, %s: the night clock changed", style, foot, name)
				}
			}
			if got := drawNight(showWide, showHigh, nightAlarmFrame(style, foot)); samePixels(got, alone) {
				t.Errorf("%q foot %q: the line is not drawn", style, foot)
			}
		}
	}
}

// The line has a band of its own between the time and the foot: drawn with the line's ink put out, the
// band is black, so neither the clock nor the Next up card or the chips reach into it; drawn as it is,
// the line is there and nothing else in the frame differs. Nothing is brighter than the night red.
func TestNightAlarmKeepsClearOfTheClockAndTheFoot(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	previewFont(t)
	ink := nightAlarmInk
	t.Cleanup(func() { nightAlarmInk = ink })
	for _, style := range []string{nightStylePlain, nightStyleLED, nightStyleFlip} {
		for _, foot := range []string{"", "chips", "card"} {
			name := style + " foot " + foot
			s := nightAlarmFrame(style, foot)
			nightAlarmInk = ink
			lit := drawNight(showWide, showHigh, s)
			nightAlarmInk = color.RGBA{0, 0, 0, 255}
			dark := drawNight(showWide, showHigh, s)

			r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
			bottom := showHigh - r.nightInfoReserve(s)
			band := image.Rect(0, bottom-r.nightAlarmBand(), showWide, bottom)
			if n := litBelow(dark.SubImage(band).(*image.RGBA), band.Min.Y); n != 0 {
				t.Errorf("%s: %d pixels of the clock or the foot in the line's band %v", name, n, band)
			}
			if n := litBelow(lit.SubImage(band).(*image.RGBA), band.Min.Y); n < 300 {
				t.Errorf("%s: only %d pixels of the line in its band", name, n)
			}
			for y := 0; y < showHigh; y++ {
				if y >= band.Min.Y && y < band.Max.Y {
					continue
				}
				for x := 0; x < showWide; x++ {
					if lit.RGBAAt(x, y) != dark.RGBAAt(x, y) {
						t.Fatalf("%s: the line drew outside its band, at %d,%d", name, x, y)
					}
				}
			}
			if p, c, bad := brighterThanNight(lit, style == nightStyleFlip); bad {
				t.Errorf("%s: a pixel the night cannot make at %v: %v", name, p, c)
			}
		}
	}
}

// Under a notification the line waits, as the foot does: the card is the one thing on the night clock.
func TestNightAlarmWaitsUnderANotification(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	previewFont(t)
	s := nightAlarmFrame(nightStylePlain, "card")
	s.showNotification = true
	s.notification = notify.Notification{Kind: notify.Text, Title: "Server", Message: "The home server is not answering"}
	without := s
	without.nightAlarm = false
	if !samePixels(drawNight(showWide, showHigh, s), drawNight(showWide, showHigh, without)) {
		t.Error("the line is drawn under a notification")
	}
}

// The line does not stand still all night (#560): it moves at most a pixel a minute each way, stays in
// its band, and comes back on itself every 48 minutes, through midnight too.
func TestNightAlarmDrifts(t *testing.T) {
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	day := time.Date(2026, 11, 2, 0, 0, 0, 0, time.Local)
	seen := map[image.Point]bool{}
	prev := r.nightAlarmDrift(day.Add(-time.Minute))
	for m := 0; m < 24*60; m++ {
		at := day.Add(time.Duration(m) * time.Minute)
		d := r.nightAlarmDrift(at)
		seen[d] = true
		if abs(d.X-prev.X) > 1 || abs(d.Y-prev.Y) > 1 {
			t.Fatalf("at %s the line jumped from %v to %v", at.Format("15:04"), prev, d)
		}
		if abs(d.X) > nightAlarmAcross || d.Y < 0 || d.Y > nightAlarmDown {
			t.Fatalf("at %s the line is out of its room: %v", at.Format("15:04"), d)
		}
		if again := r.nightAlarmDrift(at.Add(48 * time.Minute)); m+48 < 24*60 && again != d {
			t.Fatalf("at %s the path does not come back after 48 minutes: %v then %v", at.Format("15:04"), d, again)
		}
		prev = d
	}
	if len(seen) < 20 {
		t.Errorf("the line takes only %d places through the night", len(seen))
	}
}

// Only an Echo Show 5 offers the setting: the row under Display and the switch in Home Assistant. It is
// on at first, and the row turns it off and on again.
func TestNightAlarmIsTheShow5s(t *testing.T) {
	d := noticeDisplay(t, showWide, showHigh)
	if err := config.Set().Screen().AtNight(true, true); err != nil {
		t.Fatal(err)
	}
	if err := config.Set().Screen().Night("22-6"); err != nil {
		t.Fatal(err)
	}
	was := nightAlarmHere
	t.Cleanup(func() { nightAlarmHere = was })
	d.nightAlarmSw = nightAlarmSwitch(d)
	row := func() (settingRow, bool) {
		rows, _ := categoryRows(sheetView{st: settings{cat: catDisplay, night: "22-6"}})
		for _, r := range rows {
			if r.id == "nightalarm" {
				return r, true
			}
		}
		return settingRow{}, false
	}
	switched := func() bool {
		return slices.Contains(d.Entities(), esphome.Entity(d.nightAlarmSw))
	}

	nightAlarmHere = func() bool { return false }
	if _, ok := row(); ok {
		t.Error("the row is offered on a device that is not an Echo Show 5")
	}
	if switched() {
		t.Error("the switch is offered on a device that is not an Echo Show 5")
	}

	nightAlarmHere = func() bool { return true }
	r, ok := row()
	if !ok {
		t.Fatal("no row on an Echo Show 5")
	}
	if !r.on {
		t.Error("the setting is not on at first")
	}
	if !switched() {
		t.Error("no switch on an Echo Show 5")
	}
	d.rowTap("nightalarm", partMain, 0)
	if !config.Get().Screen.NoNightAlarm {
		t.Error("the row did not turn it off")
	}
	if r, _ := row(); r.on {
		t.Error("the row still says on")
	}
	d.rowTap("nightalarm", partMain, 0)
	if config.Get().Screen.NoNightAlarm {
		t.Error("the row did not turn it back on")
	}
}

// With SHOW_PREVIEW set, the Show 5's night clock with the next alarm is written there: each night style
// alone, and with the Next up card and the chips along the foot.
func TestNightAlarmPreview(t *testing.T) {
	dir := os.Getenv("SHOW_PREVIEW")
	if dir == "" {
		t.Skip("SHOW_PREVIEW is not set")
	}
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	previewFont(t)
	for style, label := range map[string]string{nightStylePlain: "red", nightStyleLED: "led", nightStyleFlip: "flip"} {
		for _, foot := range []string{"", "card", "chips"} {
			name := "night-alarm-" + label
			if foot != "" {
				name += "-" + foot
			}
			img := drawNight(showWide, showHigh, nightAlarmFrame(style, foot))
			f, err := os.Create(filepath.Join(dir, name+"-show5.png"))
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(f, img); err != nil {
				t.Fatal(err)
			}
			f.Close()
		}
	}
}
