package media

import (
	"context"
	"log/slog"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Night volume: as quiet hours start, a device left loud from the afternoon's music is turned down to
// a level somebody chose, so the first answer at bedtime is not a shout; as they end, it goes back to
// where it was.
//
// It turns the volume down once a night, as the hours start, and never again that night: somebody who
// turns it up at two in the morning wanted it up, and a restart does not undo that, since the night it
// last looked at is saved. It puts the level back only if it is still the one night volume set, so a
// level picked overnight is not undone by the morning. Changing the night volume inside the hours moves
// a device still at the old one to the new one, and none, or one above where it was, puts it straight
// back. Where it came down from and to are saved, so a restart in the night still puts it back, and a
// device that starts inside hours it has not looked at yet is turned down then.
//
// Muted, the level still moves, but the speaker stays silent: unmuting is somebody's choice.
//
// Alarms and timers ring at their own volume (config.Alarms.Ring), which starts from the daytime level
// rather than this one.

// nightCheck is how often the hours are looked at: a minute late at worst is fine for a volume.
const nightCheck = 30 * time.Second

// nightSettle is how long after a start the hours are first looked at. The clock can be hours out at
// boot until the network sets it, and a device turned back up at midnight by a wrong clock would be
// turned down again a moment later, loudly in between.
var nightSettle = 2 * time.Minute

func newNight() *esphome.Number {
	return &esphome.Number{
		Base: esphome.Base{
			ObjectID: "night_volume",
			Name:     "Night volume",
			Icon:     "mdi:volume-low",
			Category: esphome.CategoryConfig,
		},
		Min: 0, Max: VolumeSteps, Step: 1,
		Mode: esphome.NumberSlider,
	}
}

// turnDown is the level as quiet hours start, and the daytime level to remember: the limit when the
// volume is over it, else unchanged. Nothing changes without a limit, or when it was already turned
// down.
func turnDown(limit, step, day int) (to, keep int) {
	if limit <= 0 || day > 0 || step <= limit {
		return step, day
	}
	return limit, step
}

// turnUp is the level as quiet hours end: back to where it was turned down from if it is still at the
// level night volume set, else where somebody put it since.
func turnUp(set, step, day int) int {
	if day > 0 && step == set {
		return day
	}
	return step
}

// relimit is the level when the night volume changes to limit while the device is turned down: the new
// limit for a device still at the old one, straight back for none or for one above where it was, and
// unchanged for a level somebody chose. It says what to remember after, as turnDown does.
func relimit(limit, step, day, set int) (to, keepDay, keepSet int) {
	if day == 0 || step != set {
		return step, day, set
	}
	if limit <= 0 || limit >= day {
		return day, 0, 0
	}
	return limit, day, limit
}

// nightOf names the night now belongs to in quiet hours window: the date the hours began, so the small
// hours of a window across midnight belong to the evening before, and a day belongs to the night to
// come. Empty for a window that does not parse.
func nightOf(window string, now time.Time) string {
	from, to, ok := config.ParseWindow(window)
	if !ok {
		return ""
	}
	day := now
	if m := now.Hour()*60 + now.Minute(); from > to && m < to {
		day = now.AddDate(0, 0, -1)
	}
	return day.Format("2006-01-02")
}

// Run follows quiet hours for the night volume until ctx ends.
func (p *Player) Run(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return nil
	case <-time.After(nightSettle):
	}
	quiet := quietNow()
	// Whatever happened while the daemon was not running: a night that started, or one that ended.
	if quiet {
		p.nightStart(false)
	} else {
		p.nightEnd()
	}
	p.pushLanes()
	t := time.NewTicker(nightCheck)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
		now := quietNow()
		switch {
		case now && !quiet:
			p.nightStart(true)
		case !now && quiet:
			p.nightEnd()
		}
		quiet = now
		p.pushLanes()
	}
}

func quietNow() bool { return config.Get().Speaker.Quiet(time.Now()) }

// nightStart turns the volume down to the night volume, if it is over it. Unless fresh (the hours have
// just begun, or the night volume was just chosen), a night already looked at is left alone.
func (p *Player) nightStart(fresh bool) {
	p.nightMu.Lock()
	defer p.nightMu.Unlock()
	p.nightStartLocked(fresh)
}

func (p *Player) nightStartLocked(fresh bool) {
	c := config.Get().Speaker
	night := nightOf(c.QuietHours, time.Now())
	if !fresh && night != "" && night == c.NightOf {
		return
	}
	// Saved last, and only once the night is dealt with: a level that could not be saved leaves the
	// night still to look at, rather than marked as looked at with the device still loud.
	done := false
	defer func() {
		if done && night != config.Get().Speaker.NightOf {
			if err := config.Set().Speaker().NightOf(night); err != nil {
				slog.Error("saving the night looked at failed", "err", err)
			}
		}
	}()
	from := p.Volume()
	to, keep := turnDown(c.NightVolume, from, c.DayVolume)
	if to == from {
		done = true
		return
	}
	if err := config.Set().Speaker().Night(keep, to); err != nil {
		slog.Error("saving the daytime volume failed", "err", err)
		return
	}
	p.setQuietly(to)
	done = true
	slog.Info("night volume: turned down", "from", keep, "to", to)
}

// nightEnd puts the volume back where night volume found it.
func (p *Player) nightEnd() {
	p.nightMu.Lock()
	defer p.nightMu.Unlock()
	c := config.Get().Speaker
	if c.DayVolume == 0 {
		return
	}
	// Turned down by a build that did not save the level it set: that was the night volume.
	set := c.NightSet
	if set == 0 {
		set = c.NightVolume
	}
	to := turnUp(set, p.Volume(), c.DayVolume)
	if err := config.Set().Speaker().Night(0, 0); err != nil {
		slog.Error("saving the daytime volume failed", "err", err)
	}
	if to == p.Volume() {
		slog.Info("night volume: left as it was set overnight", "step", to)
		return
	}
	p.setQuietly(to)
	slog.Info("night volume: turned back up", "to", to)
}

// setQuietly applies and saves a level without the arc: nobody turned it, and at night a ring lighting
// up in a dark room is the opposite of the point. A muted device takes the level and stays silent
// (apply).
func (p *Player) setQuietly(step int) {
	applied := p.apply(step, false)
	if err := config.Set().Speaker().Volume(applied); err != nil {
		slog.Error("saving volume failed", "err", err)
	}
}

// SetNightVolume chooses the night volume, 0 for none, as Home Assistant and the setup page do. Chosen
// inside quiet hours it applies at once (relimit, or turnDown for a device not yet turned down). The
// setting is saved under the same lock it is applied under, so two changes at once end where the last
// saved one says.
func (p *Player) SetNightVolume(n int) {
	n = max(0, min(n, VolumeSteps))
	p.nightMu.Lock()
	defer p.nightMu.Unlock()
	if err := config.Set().Speaker().NightVolume(n); err != nil {
		slog.Error("saving a setting failed", "setting", p.night.ObjectID, "err", err)
		return
	}
	p.night.Set(float32(n))
	slog.Info("setting changed", "setting", p.night.ObjectID, "using", n)
	p.pushLanes()
	if !quietNow() {
		return
	}
	c := config.Get().Speaker
	if c.DayVolume == 0 {
		p.nightStartLocked(true)
		return
	}
	from := p.Volume()
	to, day, set := relimit(n, from, c.DayVolume, c.NightSet)
	if to == from && day == c.DayVolume && set == c.NightSet {
		return
	}
	if err := config.Set().Speaker().Night(day, set); err != nil {
		slog.Error("saving the daytime volume failed", "err", err)
		return
	}
	p.setQuietly(to)
	slog.Info("night volume: moved with the setting", "from", from, "to", to)
}

// CapRaise is the most a remote's volume slider may take the device to when it asks for to: in quiet
// hours with a night volume, a raise stops at the night volume, or where the device already is if
// somebody put it higher. Night volume only turns the device down once, so without this a slider left
// high on a phone would undo it at the first nudge. A lower level is always taken.
func (p *Player) CapRaise(to int) int {
	return capRaise(to, p.Volume(), config.Get().Speaker.NightVolume, quietNow())
}

func capRaise(to, from, limit int, quiet bool) int {
	if !quiet || limit <= 0 || to <= from {
		return to
	}
	return min(to, max(from, limit))
}

// NightVolume is the night volume, 0 for none.
func (p *Player) NightVolume() int { return config.Get().Speaker.NightVolume }
