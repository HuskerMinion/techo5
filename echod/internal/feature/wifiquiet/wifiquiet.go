// Package wifiquiet keeps the device off the Wi-Fi for a window of the day.
//
// It is for a household that switches its access point off at night. Without it the device does what a
// station does when its network goes away: it joins another network it knows if one is in reach (an old
// access point, one that is out of reach by day), where it may stay all morning on a weaker network and
// without its own address, and with none in reach it goes on searching the air until the network is
// back. With Wi-Fi quiet hours on, it leaves the network when the hours start, keeps the radio quiet
// through them, and looks for its networks again when they end.
//
// It acts on the edges only. The device leaves the network once, as the hours start; somebody who joins a
// network by hand during them (the Wi-Fi page) keeps it, and the device is not taken off again before the
// next night. It is let back on as the hours end, and also at any look outside them after a start, so a
// restart in the middle of the night cannot leave it off the air. A clock that has not been set yet
// (no NTP since the start) keeps it on: the hours are only kept by a device that knows the time.
//
// While it is off, wifi.QuietFlag tells the boot scripts' network keeper so, which would otherwise
// start the supplicant again every minute and reboot the device after a quarter of an hour offline.
package wifiquiet

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wifi"
)

func init() {
	if layout.OnAndroid() {
		return // a tablet's Wi-Fi is Android's
	}
	component.Register(component.Network, New(), component.Order(96))
}

// every is how often it looks: the hours start and end within this of their time.
const every = 30 * time.Second

// Quiet is the component, with what it asks the world for as functions so a test can be the world.
type Quiet struct {
	on       *esphome.Switch
	from, to *esphome.Select

	now       func() time.Time
	available func() bool
	pause     func(context.Context) error
	resume    func(context.Context) error
	save      func(config.WifiQuiet) error

	kick chan struct{}

	mu     sync.Mutex
	cfg    config.WifiQuiet
	looked bool // the first look since the start has been made
	wasIn  bool // the last look was in the hours
	paused bool // this process took the device off the network and has not let it back on
}

func New() *Quiet {
	q := &Quiet{
		on: &esphome.Switch{Base: esphome.Base{
			ObjectID: "wifi_quiet_hours", Name: "Wi-Fi quiet hours", Icon: "mdi:wifi-off",
			Category: esphome.CategoryConfig,
		}},
		from: &esphome.Select{
			Base: esphome.Base{ObjectID: "wifi_quiet_hours_start", Name: "Wi-Fi quiet hours start",
				Icon: "mdi:wifi-cancel", Category: esphome.CategoryConfig},
			Options: quarters,
		},
		to: &esphome.Select{
			Base: esphome.Base{ObjectID: "wifi_quiet_hours_end", Name: "Wi-Fi quiet hours end",
				Icon: "mdi:wifi-check", Category: esphome.CategoryConfig},
			Options: quarters,
		},
		now:       time.Now,
		available: wifi.Available,
		pause:     wifi.Pause,
		resume:    wifi.Resume,
		save: func(c config.WifiQuiet) error {
			if err := config.Set().WifiQuiet().Hours(config.FormatWindow(window(c))); err != nil {
				return err
			}
			return config.Set().WifiQuiet().On(c.On)
		},
		kick: make(chan struct{}, 1),
	}
	q.on.OnCommand = func(on bool) {
		q.change(func(c *config.WifiQuiet) bool { c.On = on; return true })
	}
	q.from.OnCommand = func(v string) { q.setEnd(v, true) }
	q.to.OnCommand = func(v string) { q.setEnd(v, false) }
	return q
}

func (q *Quiet) Name() string { return "wifiquiet" }

func (q *Quiet) Entities() []esphome.Entity { return []esphome.Entity{q.on, q.from, q.to} }

func (q *Quiet) Restore(c config.Config) {
	q.mu.Lock()
	q.cfg = c.WifiQuiet
	q.mu.Unlock()
	q.show(c.WifiQuiet)
	slog.Info("restored", "what", q.on.ObjectID, "on", c.WifiQuiet.On, "hours", config.FormatWindow(window(c.WifiQuiet)))
}

// window is the hours to use: the ones set, or the default.
func window(c config.WifiQuiet) (int, int) { return c.Window() }

// show puts the setting on the three entities.
func (q *Quiet) show(c config.WifiQuiet) {
	from, to := window(c)
	q.on.Set(c.On)
	q.from.Set(quarterText(from))
	q.to.Set(quarterText(to))
}

// setEnd is a new start (start true) or end of the hours, as chosen in Home Assistant.
func (q *Quiet) setEnd(v string, start bool) {
	m, ok := quarterMinutes(v)
	if !ok {
		return
	}
	q.change(func(c *config.WifiQuiet) bool {
		from, to := window(*c)
		if start {
			from = m
		} else {
			to = m
		}
		if from == to {
			slog.Warn("wifi quiet hours: the start and the end are the same time; not changed", "at", v)
			return false
		}
		c.Hours = config.FormatWindow(from, to)
		return true
	})
}

// change applies edit to the setting, saves it, shows it, and looks at once, so turning the hours off in
// the middle of them lets the device back on now rather than at their end.
func (q *Quiet) change(edit func(*config.WifiQuiet) bool) {
	q.mu.Lock()
	c := q.cfg
	ok := edit(&c)
	if ok {
		q.cfg = c
	}
	q.mu.Unlock()
	if ok {
		if err := q.save(c); err != nil {
			slog.Error("saving the Wi-Fi quiet hours failed", "err", err)
		}
	}
	q.show(c)
	select {
	case q.kick <- struct{}{}:
	default:
	}
}

// Run looks every little while, on a device that has a supplicant to ask.
func (q *Quiet) Run(ctx context.Context) error {
	if !q.available() {
		<-ctx.Done()
		return nil
	}
	t := time.NewTicker(every)
	defer t.Stop()
	q.look(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		case <-q.kick:
		}
		q.look(ctx)
	}
}

// clockSet is whether the wall clock has been set, as elsewhere: until NTP has run, the kernel starts
// in 2010 or so, and the hours would be kept at the wrong time.
func clockSet(now time.Time) bool { return now.Year() >= 2025 }

// look keeps the device off the network as the hours start and lets it back on outside them.
func (q *Quiet) look(ctx context.Context) {
	now := q.now()
	q.mu.Lock()
	c := q.cfg
	first := !q.looked
	wasIn, paused := q.wasIn, q.paused
	q.mu.Unlock()

	in := c.On && clockSet(now) && c.In(now.Hour()*60+now.Minute())
	switch {
	case in && !wasIn:
		_, to := window(c)
		if err := q.pause(ctx); err != nil {
			slog.Warn("wifi quiet hours: could not leave the network; trying again shortly", "err", err)
			return // wasIn stays false, so the next look tries again
		}
		slog.Info("wifi quiet hours: off the network until the hours end", "until", quarterText(to))
		paused = true
	case !in && (paused || first):
		// At the first look after a start too: a restart during the hours must not leave the device
		// off the air, and on a device that is on the network this changes nothing.
		if err := q.resume(ctx); err != nil {
			slog.Warn("wifi quiet hours: could not look for the network again; trying again shortly", "err", err)
			return // looked stays false after a start, so the next look tries again
		}
		if paused {
			slog.Info("wifi quiet hours: looking for the network again")
		}
		paused = false
	}
	q.mu.Lock()
	q.wasIn, q.paused, q.looked = in, paused, true
	q.mu.Unlock()
}

// quarterText is minutes since midnight as 24-hour time, "01:00".
func quarterText(m int) string { return fmt.Sprintf("%02d:%02d", m/60, m%60) }

// quarterMinutes reads a quarterText back.
func quarterMinutes(v string) (int, bool) {
	h, m, ok := strings.Cut(v, ":")
	if !ok {
		return 0, false
	}
	hour, err1 := strconv.Atoi(h)
	minute, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, false
	}
	return hour*60 + minute, true
}

// quarters are the times the start and the end offer: every quarter hour of the day.
var quarters = func() []string {
	out := make([]string, 0, 96)
	for m := 0; m < 24*60; m += 15 {
		out = append(out, quarterText(m))
	}
	return out
}()
