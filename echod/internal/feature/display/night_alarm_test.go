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

// nightAlarmPanels are the screens that offer the line: the Show 5 and the Show 8.
func nightAlarmPanels() []struct {
	name       string
	wide, high int
} {
	return showPanels()[:2]
}

// nightAlarmFrame is a night clock frame at 3:47 on a Monday with the next alarm at 7:30,
// carrying the chips or the Next up card along the foot as foot says ("", "chips" or "card").
func nightAlarmFrame(style, foot string) scene {
	at := time.Date(2026, 11, 2, 3, 47, 0, 0, time.Local)
	s := scene{now: at, phase: "idle", redClock: true, redStyle: style, nightDim: true}
	if foot != "" {
		s = nightInfoFrame(at, style, true, foot == "chips")
	}
	s.nightAlarm = true
	s.alarms.NextAlarm = &alarm.Upcoming{Key: "a1", At: time.Date(2026, 11, 2, 7, 30, 0, 0, time.Local)}
	s.alarms.Next = s.alarms.NextAlarm
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
// night clock style and screen, with or without the foot.
func TestNightAlarmOffIsTheClockAsItWas(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	previewFont(t)
	for _, panel := range nightAlarmPanels() {
		for _, style := range []string{nightStylePlain, nightStyleLED, nightStyleFlip} {
			for _, foot := range []string{"", "chips", "card"} {
				off := nightAlarmFrame(style, foot)
				off.nightAlarm = false
				alone := drawNight(panel.wide, panel.high, off)
				for name, s := range map[string]scene{"off": off, "no alarm": nightAlarmFrame(style, foot)} {
					if name == "no alarm" {
						s.alarms.Next, s.alarms.NextAlarm = nil, nil
					}
					if got := drawNight(panel.wide, panel.high, s); !samePixels(got, alone) {
						t.Errorf("%s %q foot %q, %s: the night clock changed", panel.name, style, foot, name)
					}
				}
				if got := drawNight(panel.wide, panel.high, nightAlarmFrame(style, foot)); samePixels(got, alone) {
					t.Errorf("%s %q foot %q: the line is not drawn", panel.name, style, foot)
				}
			}
		}
	}
}

// The line has a band of its own between the time and the foot, on the Show 5 and the Show 8: drawn
// with the line's ink put out, the band is black, so neither the clock nor the Next up card or the
// chips reach into it; drawn as it is, the line is there and nothing else in the frame differs, at
// every place its drift takes it through the night, clear of the screen's sides. Nothing is brighter
// than the night red.
func TestNightAlarmKeepsClearOfTheClockAndTheFoot(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	previewFont(t)
	ink := nightAlarmInk
	t.Cleanup(func() { nightAlarmInk = ink })
	for _, panel := range nightAlarmPanels() {
		w, h := panel.wide, panel.high
		r := newRenderer(image.NewRGBA(image.Rect(0, 0, w, h)))
		for _, style := range []string{nightStylePlain, nightStyleLED, nightStyleFlip} {
			for _, foot := range []string{"", "chips", "card"} {
				name := panel.name + " " + style + " foot " + foot
				s := nightAlarmFrame(style, foot)
				bottom := h - r.nightInfoReserve(s)
				band := image.Rect(0, bottom-r.nightAlarmBand(), w, bottom)

				nightAlarmInk = ink
				lit := drawNight(w, h, s)
				nightAlarmInk = color.RGBA{0, 0, 0, 255}
				dark := drawNight(w, h, s)
				if n := litBelow(dark.SubImage(band).(*image.RGBA), band.Min.Y); n != 0 {
					t.Errorf("%s: %d pixels of the clock or the foot in the line's band %v", name, n, band)
				}
				if n := litBelow(lit.SubImage(band).(*image.RGBA), band.Min.Y); n < 300 {
					t.Errorf("%s: only %d pixels of the line in its band", name, n)
				}
				if p, c, bad := brighterThanNight(lit, style == nightStyleFlip); bad {
					t.Errorf("%s: a pixel the night cannot make at %v: %v", name, p, c)
				}
				// The minutes of the night that take the line furthest each way: out to the left and down,
				// back at its place, and out to the right.
				for _, m := range []int{0, 2 * r.s(nightAlarmAcross), 3 * r.s(nightAlarmAcross), r.s(nightAlarmAcross)} {
					s.now = time.Date(2026, 11, 2, m/60, m%60, 0, 0, time.Local)
					nightAlarmInk = ink
					moved := drawNight(w, h, s)
					nightAlarmInk = color.RGBA{0, 0, 0, 255}
					still := drawNight(w, h, s)
					if b := diffBounds(moved, still); b.Empty() || !b.In(band) || b.Min.X < r.margin || b.Max.X > w-r.margin {
						t.Errorf("%s at %02d:%02d: the line at %v, out of its band %v or near the sides", name, m/60, m%60, b, band)
					}
				}
			}
		}
	}
}

// diffBounds is the smallest rectangle holding every pixel where a and b differ.
func diffBounds(a, b *image.RGBA) image.Rectangle {
	var out image.Rectangle
	for y := a.Rect.Min.Y; y < a.Rect.Max.Y; y++ {
		for x := a.Rect.Min.X; x < a.Rect.Max.X; x++ {
			if a.RGBAAt(x, y) != b.RGBAAt(x, y) {
				out = out.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	return out
}

// A reminder before the alarm is not what the line says: it is said, not rung. The line is the alarm
// after it, and with only a reminder ahead there is no line at all.
func TestNightAlarmLeavesOutReminders(t *testing.T) {
	was := clock24.Load()
	t.Cleanup(func() { clock24.Store(was) })
	clock24.Store(false)
	s := nightAlarmFrame(nightStylePlain, "")
	s.alarms.Next = &alarm.Upcoming{Key: "trash", Label: "Take out the trash", At: time.Date(2026, 11, 2, 6, 0, 0, 0, time.Local)}
	if got, want := nightAlarmLine(s), "Alarm 7:30 AM"; got != want {
		t.Errorf("with a reminder first: %q, want %q", got, want)
	}
	s.alarms.NextAlarm = nil
	if got := nightAlarmLine(s); got != "" {
		t.Errorf("with only a reminder ahead: %q, want no line", got)
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
	for _, panel := range nightAlarmPanels() {
		if !samePixels(drawNight(panel.wide, panel.high, s), drawNight(panel.wide, panel.high, without)) {
			t.Errorf("%s: the line is drawn under a notification", panel.name)
		}
	}
}

// The line does not stand still all night (#560): on the Show 5 and the Show 8 alike it moves at most a
// pixel a minute each way and stays in its scaled room, and it comes back on itself, every 48 minutes on
// the Show 5 and every 320 on the Show 8, through midnight too.
func TestNightAlarmDrifts(t *testing.T) {
	for _, c := range []struct {
		name       string
		wide, high int
		every      int
	}{{"show5", showWide, showHigh, 48}, {"show8", show8Wide, show8High, 320}} {
		r := newRenderer(image.NewRGBA(image.Rect(0, 0, c.wide, c.high)))
		day := time.Date(2026, 11, 2, 0, 0, 0, 0, time.Local)
		seen := map[image.Point]bool{}
		prev := r.nightAlarmDrift(day.Add(-time.Minute))
		for m := 0; m < 24*60; m++ {
			at := day.Add(time.Duration(m) * time.Minute)
			d := r.nightAlarmDrift(at)
			seen[d] = true
			if abs(d.X-prev.X) > 1 || abs(d.Y-prev.Y) > 1 {
				t.Fatalf("%s at %s: the line jumped from %v to %v", c.name, at.Format("15:04"), prev, d)
			}
			if abs(d.X) > r.s(nightAlarmAcross) || d.Y < 0 || d.Y > r.s(nightAlarmDown) {
				t.Fatalf("%s at %s: the line is out of its room: %v", c.name, at.Format("15:04"), d)
			}
			if again := r.nightAlarmDrift(at.Add(time.Duration(c.every) * time.Minute)); m+c.every < 24*60 && again != d {
				t.Fatalf("%s at %s: the path does not come back after %d minutes: %v then %v", c.name, at.Format("15:04"), c.every, d, again)
			}
			prev = d
		}
		if len(seen) < 20 {
			t.Errorf("%s: the line takes only %d places through the night", c.name, len(seen))
		}
	}
}

// Only an Echo Show 5 or Show 8 offers the setting (nightAlarmHere): the row under Display and the
// switch in Home Assistant. It is on at first, and the row turns it off and on again.
func TestNightAlarmIsTheBedsideShows(t *testing.T) {
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
		t.Error("the row is offered on a device that is not an Echo Show 5 or 8")
	}
	if switched() {
		t.Error("the switch is offered on a device that is not an Echo Show 5 or 8")
	}

	nightAlarmHere = func() bool { return true }
	r, ok := row()
	if !ok {
		t.Fatal("no row where it is offered")
	}
	if !r.on {
		t.Error("the setting is not on at first")
	}
	if !switched() {
		t.Error("no switch where it is offered")
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

// With SHOW_PREVIEW set, the night clock with the next alarm is written there on the Show 5 (960x480)
// and the Show 8 (1280x800): each night style alone, and with the Next up card and the chips along the
// foot.
func TestNightAlarmPreview(t *testing.T) {
	dir := os.Getenv("SHOW_PREVIEW")
	if dir == "" {
		t.Skip("SHOW_PREVIEW is not set")
	}
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	previewFont(t)
	for _, panel := range nightAlarmPanels() {
		for style, label := range map[string]string{nightStylePlain: "red", nightStyleLED: "led", nightStyleFlip: "flip"} {
			for _, foot := range []string{"", "card", "chips"} {
				name := "night-alarm-" + label
				if foot != "" {
					name += "-" + foot
				}
				img := drawNight(panel.wide, panel.high, nightAlarmFrame(style, foot))
				f, err := os.Create(filepath.Join(dir, name+"-"+panel.name+".png"))
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
}
