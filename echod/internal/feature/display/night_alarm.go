//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
	"log/slog"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/alarm"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/locale"
)

// Night clock shows the next alarm (#619): an Echo Show 5 or Show 8 is the one on a bedside table, so its
// night clock says, small and under the time, when the next alarm will ring - "Alarm 7:30 AM", with the day
// as well when it is not today or tomorrow - without anybody leaving the night light to look. It is the
// clock's next alarm that rings (alarm.View's NextAlarm, the day clock's Next without the reminders):
// the device's alarms and the Home Assistant ones it follows, whichever comes first, over their weekdays
// and around an occurrence left out. It is drawn in a dimmer shade of the night red, and with no alarm on
// it is not there at all. On unless turned off. The Skylight hangs on a wall, a Panel is a tablet and
// the Spot's round face has no room under its time, so they do not offer it.

// nightAlarmHere is whether this device offers the next alarm on its night clock: every Echo Show 5 and
// Show 8 board, either generation of each (the 2nd gen Show 8 on either kernel). A variable so a test
// can say which device it is.
var nightAlarmHere = func() bool { return layout.Show5() || layout.Show8() }

// nightAlarmSwitch is the Home Assistant switch for the setting.
func nightAlarmSwitch(d *Display) *esphome.Switch {
	s := &esphome.Switch{
		Base: esphome.Base{
			ObjectID: "screen_night_alarm",
			Name:     "Night clock shows the next alarm",
			Icon:     "mdi:alarm",
			Category: esphome.CategoryConfig,
		},
	}
	s.OnCommand = d.setNightAlarm
	return s
}

// setNightAlarm saves the setting, tells Home Assistant and shows it at once.
func (d *Display) setNightAlarm(on bool) {
	if err := config.Set().Screen().NoNightAlarm(!on); err != nil {
		slog.Error("saving a setting failed", "setting", "screen_night_alarm", "err", err)
		on = !config.Get().Screen.NoNightAlarm
	} else {
		slog.Info("setting changed", "setting", "screen_night_alarm", "using", on)
	}
	if d.nightAlarmSw != nil {
		d.nightAlarmSw.Set(on)
	}
	d.wake()
}

// nightAlarmText is the next alarm as the night clock says it: "Alarm 7:30 AM" today or tomorrow,
// "Alarm Mon 7:30 AM" later in the week, and the date as well past it, as a one-off set for next month
// would be. A snoozed alarm is "Snoozed until", as on the day clock. Empty for no alarm.
func nightAlarmText(next *alarm.Upcoming, now time.Time, lang string) string {
	if next == nil || next.At.IsZero() {
		return ""
	}
	at := next.At.In(now.Location())
	words := locale.Alarm(lang, next.Snoozed)
	switch days := daysBetween(now, at); {
	case days <= 1:
	case days < 7:
		words += " " + locale.ShortWeekday(at, lang)
	default:
		words += " " + locale.ShortDate(at, lang)
	}
	return words + " " + clockText(at)
}

// nightAlarmLine is what the night clock of s says of the next alarm, or "" for nothing: the setting
// off, no alarm on, or a notification over the clock, which is then the one thing on it. A reminder is
// not counted (View's NextAlarm, not its Next): it is said, not rung, and "Alarm 9:00 AM" by the bed for
// one would be wrong. A snooze is, since it rings.
func nightAlarmLine(s scene) string {
	if !s.nightAlarm || s.showNotification {
		return ""
	}
	return nightAlarmText(s.alarms.NextAlarm, s.now, screenLang())
}

// nightAlarmInk is the line's red: three quarters of the night clock's, so it reads as the time's
// footnote and nothing on the night clock is brighter than the night red (nightTone).
var nightAlarmInk = nightTone(color.RGBA{0xc0, 0xc0, 0xc0, 0xff})

// The line stands under the time all night, and the Echo Show 5 2nd gen's panel keeps a shadow of what
// stands still for an hour (#560, clock_drift.go). The night clock itself does not move, so the line
// wanders on its own, a pixel a minute at most each way, across out to nightAlarmAcross either side and
// down to nightAlarmDown, both scaled to the screen. On the Show 5 that is back every 48 minutes across
// and every 16 down, so the path repeats every 48 minutes; on the Show 8, drawn a third larger, every 64
// and 20, repeating every 320. Either way it runs on through midnight without a jump. The path is the
// time of day on the clock, so a frame is the same for the same time, previews and all.
const (
	nightAlarmAcross = 12
	nightAlarmDown   = 4
	// nightAlarmFoot is the clear space, before scaling, under the line at the screen's foot.
	nightAlarmFoot = 16
)

// nightAlarmDrift is how far from its place the line is at now. The reach is scaled and the step is not,
// so a larger screen takes longer over the path rather than stepping two pixels at a time.
func (r *renderer) nightAlarmDrift(now time.Time) image.Point {
	m := now.Hour()*60 + now.Minute()
	return image.Pt(sway(m, r.s(nightAlarmAcross)), abs(sway(m, r.s(nightAlarmDown))))
}

// nightAlarmBand is how much of the screen's height the line takes under the clock: the words, the
// room they drift down into and the space under them. The clock keeps clear of it as it does of the
// Next up foot (nightInfoReserve), and the band sits above that foot.
func (r *renderer) nightAlarmBand() int {
	return r.small.Metrics().Height.Ceil() + r.s(nightAlarmDown) + r.s(nightAlarmFoot)
}

// nightAlarmOver draws the line in band, centered across the screen and moved by its drift.
func (r *renderer) nightAlarmOver(s scene, line string, band image.Rectangle) {
	if line == "" || band.Empty() {
		return
	}
	line = r.clipTo(r.small, line, r.w-2*r.margin-2*r.s(nightAlarmAcross))
	d := r.nightAlarmDrift(s.now)
	x := (r.w-r.width(r.small, line))/2 + d.X
	base := band.Min.Y + r.small.Metrics().Ascent.Ceil() + d.Y
	r.text(r.small, line, x, base, nightAlarmInk)
}
