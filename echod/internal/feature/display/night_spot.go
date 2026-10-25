//go:build spot

package display

import (
	"log/slog"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/announce"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	"github.com/HuskerMinion/techo5/echod/internal/feature/remind"
	"github.com/HuskerMinion/techo5/echod/internal/feature/setup"
	"github.com/HuskerMinion/techo5/echod/internal/feature/talkback"
	"github.com/HuskerMinion/techo5/echod/internal/feature/video"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
)

// The Spot's own parts of the night (night_select.go has what it shares with the Show): the hours it
// has when none are set, the night clock's brightness on its panel, when the night clock takes the face,
// and the action.
//
// The night dims the face (nightCeiling), or once nothing has happened for a while it leaves the red
// clock alone on it at the night light's level (Screen at night set to Night clock), or puts it out
// (Screen off). A touch lights the face as it is by night, dimmed, and a long press opens the ring menu
// too; a minute and a half after the last touch or turn the clock comes back, or the face goes out.

// nightHoursSet is the night hours in force: the hours set, or defaultNight on a Spot with none. "off"
// is no night (nightNever).
func nightHoursSet() string { return cmpOr(config.Get().Screen.Night, defaultNight) }

// nightNever is the night hours that are no night: empty is the default hours here.
const nightNever = "off"

// glowSteps is the night clock's scale, 1 to 10, as backlight out of screen.BacklightMax. All of it is
// under backlightMin, where the panel is too dim to read in a lit room: in a dark one that is a red
// clock across the room without a light on the ceiling. Step 1 is backlight 3, not the panel's lowest
// light, 1: at 1 the panel may go dark altogether, and 3 looks the same. The steps widen from there,
// where one step at the bottom is the difference between a glow and a light in a dark room. An owner
// found the old bottom, 6, still too bright for a bedroom (techo5-spot#7). A level saved on the old
// scale is moved to the nearest step of this one once, as the settings load (config's
// rescaleNightLight).
var glowSteps = [...]int{3, 4, 6, 8, 12, 16, 22, 30, 40, 50}

// glowDefault is the night clock's brightness on a Spot with none set: backlight 8.
const glowDefault = 4

// glowSetting is the night clock's brightness on its 1 to 10 scale: the one set, or glowDefault.
func (d *Display) glowSetting() int { return glowLevelSet() }

// glowLevelSet is glowSetting, for the settings sheet's rows.
func glowLevelSet() int {
	if v := config.Get().Screen.NightLightLevel; v > 0 {
		return v
	}
	return glowDefault
}

// hasGlowRow: the Spot sets the night clock's brightness in its own settings too, under Display.
const hasGlowRow = true

// glowPreview is how long a step of the brightness row shows itself on the backlight at night, the
// settings still open, before the sheet's own dimmed light comes back.
const glowPreview = 3 * time.Second

// stepGlow moves the night clock's brightness a step from the settings sheet, keeps Home Assistant's
// number with it, and at night shows the new level on the backlight at once (glowPreview), so it can
// be set in the dark while looking at it.
func (d *Display) stepGlow(delta int) {
	v := min(max(glowLevelSet()+delta, 1), len(glowSteps))
	if err := config.Set().Screen().NightLightLevel(v); err != nil {
		slog.Error("saving the night light brightness failed", "err", err)
		return
	}
	if d.glowLevel != nil {
		d.glowLevel.Set(float32(v))
	}
	if nightNow(time.Now()) {
		d.mu.Lock()
		d.glowUntil = time.Now().Add(glowPreview)
		if d.glowTimer == nil {
			d.glowTimer = time.AfterFunc(glowPreview, func() { d.relight(true) })
		} else {
			d.glowTimer.Reset(glowPreview)
		}
		d.mu.Unlock()
	}
	d.relight(true)
	d.wake()
}

// glowBacklight is the backlight the night clock shows at.
func (d *Display) glowBacklight() int {
	return glowSteps[min(max(d.glowSetting(), 1), len(glowSteps))-1]
}

// Actions: screen_night_hours sets the night to any start and end, "19:00" and "09:30", as on the
// Show; both empty is no night.
func (d *Display) Actions() []*esphome.Action {
	return []*esphome.Action{{
		Name: "screen_night_hours",
		Args: []esphome.Arg{{Name: "start", Type: esphome.ArgString}, {Name: "end", Type: esphome.ArgString}},
		Run:  func(c esphome.Call) (any, error) { return nil, d.setNightHours(c.String("start"), c.String("end")) },
	}}
}

// nightSoon is how soon the red clock comes up when it was just asked for: the night starting, Night
// clock chosen, or the settings or the menu closing. Long enough to see the choice made, short enough
// that nobody wonders whether it worked.
const nightSoon = 10 * time.Second

// nightClockSoon asks for the red clock soon (nightSoon), as Night clock is chosen.
func (d *Display) nightClockSoon() {
	d.mu.Lock()
	d.soonAt = time.Now()
	d.mu.Unlock()
}

// nightDarkOn is whether the night puts the face out (Screen at night set to Screen off).
func nightDarkOn() bool {
	sc := config.Get().Screen
	return sc.NightDark && !sc.NightLight
}

// night puts the red clock up, or the face out, inside the night once nothing has happened for a while,
// when that is what the night is set to do, and takes it down when the night ends. Only what has to be
// seen and answered takes the clock down by itself: a call, a talk through a camera, the light before an
// alarm, a browser asking to be let in; they are shown at the night's dimmed brightness. A turn, a touch
// in the last minute and a half, the menu or the settings open, or a video leave the face as it is: the
// clock up, or the face lit. Something playing does not. A face the night put out is lit again by what
// lights a dark face anyway (a ring, a call, a reminder, a video, and at night an announcement and the
// light before an alarm: frame), and is not put out again while that is up. It reports whether it
// changed the face.
func (d *Display) night(now time.Time, on bool, view voice.State) bool {
	in := nightNow(now)
	d.showNightMode(in)
	d.mu.Lock()
	glowing, dark, touched, viewAt, using := d.nightGlow, d.nightDark, d.touchedAt, d.viewAt, d.menuOpen || d.sheetOpen
	// The night beginning, and the settings or the menu closing, ask for the clock soon (nightSoon).
	if (in && !d.nightIn) || (!using && d.nightUsing) {
		d.soonAt = now
	}
	d.nightIn, d.nightUsing = in, using
	soonAt := d.soonAt
	d.mu.Unlock()
	switch {
	case dark && !on && !(in && nightDarkOn()):
		// The night over, or Screen off no longer chosen: the face the night put out comes back.
		d.mu.Lock()
		d.nightDark = false
		d.mu.Unlock()
		slog.Info("screen: night over, back on")
		d.apply(true, d.ceilingOrDefault(), false)
		return true
	case dark && !in:
		d.mu.Lock()
		d.nightDark = false
		d.mu.Unlock()
	}
	clock, out := in && on && nightClockOn(), in && on && nightDarkOn()
	if clock || out {
		lift := phone.Get().Busy() || talkback.Get().Busy() || sunriseProgress(now) > 0 || setup.Get().Waiting()
		recent := now.Sub(touched) < nightIdle || now.Sub(viewAt) < nightIdle
		if clock && !soonAt.IsZero() && !touched.After(soonAt) && !viewAt.After(soonAt) {
			// Asked for since the last touch or turn: only a short wait from then, never a longer one.
			recent = recent && now.Sub(soonAt) < nightSoon
		}
		active := view.Phase != "idle" || recent || using || video.Get().State().Active()
		if out {
			_, reminding := remind.Get().Showing()
			if lift || active || reminding || announcing() || announce.Get().Recording() || ringingNow(now).any() {
				return false
			}
			slog.Info("screen: night, going dark")
			d.mu.Lock()
			d.nightDark, d.nightGlow = true, false
			d.mu.Unlock()
			d.apply(false, d.ceilingOrDefault(), false)
			return true
		}
		if !lift && active {
			return false
		}
		clock = !lift
	}
	if clock == glowing {
		return false
	}
	d.mu.Lock()
	d.nightGlow = clock
	d.mu.Unlock()
	slog.Info("screen: night clock", "up", clock)
	d.relight(true)
	return true
}

// darkByNight is whether the face is out because the night put it out.
func (d *Display) darkByNight() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.nightDark && !d.on
}

// redClockUp is whether the face drawn last is the red night clock, so a touch on it is for the clock.
func (d *Display) redClockUp() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.on && d.nightGlow && d.redShown
}

// liftNight takes the red clock down for a touch: the face as it is by night, dimmed, until the night
// takes it again (night).
func (d *Display) liftNight() {
	d.mu.Lock()
	was := d.nightGlow
	d.nightGlow = false
	d.mu.Unlock()
	if was {
		slog.Info("screen: night clock lifted by a touch")
		d.relight(true)
	}
	d.wake()
}

// announcing is an announcement that arrived being up.
func announcing() bool {
	_, up := announce.Get().Showing()
	return up
}
