//go:build !dot && !spot

package display

import (
	"log/slog"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/presence"
)

// idleDimTick dims the screen once it has been idle for Dim after idle, or once Home Assistant's Dim
// screen button asked for it with nothing used since, and brings it back up at the first touch, turn or
// person near, or when the screen is turned on or its brightness set (apply). busy is anything going on
// that keeps it lit (a turn, a call, a ring, the settings open); it counts as use, so the wait starts
// again when it ends. The Screen dimmed sensor follows it.
func (d *Display) idleDimTick(now time.Time, on, busy, night bool) {
	after := idleDimAfter()
	d.mu.Lock()
	inUse := busy || presence.Present()
	if inUse || d.usedAt.IsZero() {
		d.usedAt = now
	}
	last := d.usedAt
	if d.touchedAt.After(last) {
		last = d.touchedAt
	}
	was := d.idleDimmed
	// The button holds until the screen is next used; any use since forgets it, so a later idle wait
	// is Dim after idle's own again.
	// A press while the screen is in use is dropped outright: it is stamped on Home Assistant's
	// goroutine, now at the start of the frame, so one landing between the two would otherwise look
	// unused and dim the screen for a frame.
	if inUse {
		d.dimAskedAt = time.Time{}
	}
	asked := !d.dimAskedAt.IsZero() && !last.After(d.dimAskedAt)
	if !asked {
		d.dimAskedAt = time.Time{}
	}
	// The night starts no dimming, and ends none either: a screen already down stays down into it
	// rather than jumping up to its brightness as the night begins.
	dim := on && (asked || after > 0 && now.Sub(last) >= after) && (!night || was)
	if asked && !dim {
		// Carried out at once or not at all: a press the night or a screen that is out kept from
		// dimming is not saved up to dim it hours later.
		d.dimAskedAt = time.Time{}
	}
	d.idleDimmed = dim
	d.mu.Unlock()
	if dim == was {
		return
	}
	if d.dimSensor != nil {
		d.dimSensor.Set(dim)
	}
	switch {
	case dim && asked:
		slog.Info("screen: dimmed, as Home Assistant asked")
	case dim:
		slog.Info("screen: idle, dimmed", "after", after)
	default:
		slog.Info("screen: in use, undimmed")
	}
	// Down slowly, so it is seen to happen and not taken for a fault; back up at once.
	d.relight(!dim)
}

// idleDimLevel is the backlight a dimmed screen gets: its Dimmest share of the brightness, never more
// than it would have anyway.
func idleDimLevel(target float64, ceiling int) float64 {
	return min(target, dayBacklight(ceiling, true, 0, dimmest()))
}

// dimScreen is the Dim screen button: the screen goes down to its Dimmest now, as Dim after idle would
// take it, and comes back up at the next touch, turn or person near. Home Assistant cannot see a finger
// on the panel, so this leaves bringing it back to the device. Not at night, which has its own rules,
// and not on a screen that is out. Anything going on when it is pressed counts as use, so then it does
// nothing.
func (d *Display) dimScreen() {
	now := time.Now()
	d.mu.Lock()
	on := d.on
	d.mu.Unlock()
	if !on || nightNow(now) {
		slog.Info("screen: dim asked, not done", "on", on, "night", nightNow(now))
		return
	}
	d.mu.Lock()
	d.dimAskedAt = now
	d.mu.Unlock()
	d.wake()
}

// dimButton and dimmedSensor are Home Assistant's: the button dims the screen until it is next used,
// and the sensor says whether the screen is down at its Dimmest, by the button or Dim after idle.
func dimButton(d *Display) *esphome.Button {
	return &esphome.Button{
		Base:    esphome.Base{ObjectID: "screen_dim", Name: "Dim screen", Icon: "mdi:brightness-4"},
		OnPress: d.dimScreen,
	}
}

func dimmedSensor() *esphome.BinarySensor {
	return &esphome.BinarySensor{
		Base: esphome.Base{ObjectID: "screen_dimmed", Name: "Screen dimmed", Icon: "mdi:brightness-4"},
	}
}

// setIdleDim saves the choice at i and shows it in Home Assistant.
func (d *Display) setIdleDim(i int) {
	if i < 0 || i >= len(idleDims) {
		return
	}
	if err := config.Set().Screen().DimAfterMinutes(idleDims[i].minutes); err != nil {
		slog.Error("saving dim after idle failed", "err", err)
		return
	}
	if d.idleDimSel != nil {
		d.idleDimSel.Set(idleDims[i].label)
	}
	slog.Info("setting changed", "setting", "screen_dim_after_idle", "using", idleDims[i].label)
	d.wake()
}

// idleDimSelect is the Home Assistant setting.
func idleDimSelect(d *Display) *esphome.Select {
	s := &esphome.Select{
		Base: esphome.Base{
			ObjectID: "screen_dim_after_idle",
			Name:     "Dim after idle",
			Icon:     "mdi:brightness-auto",
			Category: esphome.CategoryConfig,
		},
		Options: idleDimOptions(),
	}
	s.OnCommand = func(v string) {
		for i, t := range idleDims {
			if t.label == v {
				d.setIdleDim(i)
				return
			}
		}
	}
	return s
}
