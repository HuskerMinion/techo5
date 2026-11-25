//go:build !dot

package display

import (
	"log/slog"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// showScreensaver is the Show screensaver button (#627): the screensaver comes up now, the way it does
// by itself once the screen has been idle for the screensaver's wait, and a tap takes it down as usual.
//
// It does that by starting the idle wait as far back as the wait is long, so the frame that follows
// puts the screensaver up through the same path. Whatever keeps the screensaver away after the wait
// keeps it away after a press too: anything going on (a turn, a call, a ring, the settings open) zeroes
// the wait on the next frame, so a press then does nothing.
//
// Nothing happens either with no screensaver to show: the slideshow is not in Screensaver mode, it has
// no photo yet (no source set, or nothing fetched), or the screen is out.
func (d *Display) showScreensaver() {
	now := time.Now()
	d.mu.Lock()
	on := d.on
	d.mu.Unlock()
	if !on || !screensaverToShow() {
		slog.Info("screen: screensaver asked, nothing to show", "on", on, "mode", home.Get().SlideshowMode())
		return
	}
	d.mu.Lock()
	if !d.slideshowIdleSince.IsZero() {
		d.slideshowIdleSince = saverStart(now, home.Get().SlideshowIdleTimeout())
	}
	idle := !d.slideshowIdleSince.IsZero()
	d.mu.Unlock()
	if !idle {
		slog.Info("screen: screensaver asked, screen in use")
		return
	}
	slog.Info("screen: screensaver up, as Home Assistant asked")
	d.wake()
}

// hideScreensaver is the Hide screensaver button (#744): the screensaver goes down to the page under
// it, the clock, as a tap takes it down, so an automation that put it up can end it too. The idle wait
// starts again from the press, so it comes back by itself only after the whole wait, as after a tap;
// an automation that wants it never to come by itself sets Slideshow back to Background or Off, which
// also takes it down. A press with no screensaver up does nothing, so a wait already running is left
// to run.
func (d *Display) hideScreensaver() {
	now := time.Now()
	d.mu.Lock()
	since := d.slideshowIdleSince
	up := !since.IsZero() && now.Sub(since) >= home.Get().SlideshowIdleTimeout() && screensaverToShow()
	if up {
		d.slideshowIdleSince = now
		d.saverUp = false // a swipe straight after is the clock's again, not the photos'
	}
	d.mu.Unlock()
	if !up {
		slog.Info("screen: screensaver hide asked, none up")
		return
	}
	slog.Info("screen: screensaver down, as Home Assistant asked")
	d.wake()
}

// saverStart is when the idle wait has to have begun for the screensaver to be due at now.
func saverStart(now time.Time, wait time.Duration) time.Time { return now.Add(-wait) }

// screensaverToShow is whether the screensaver has anything to put up: Screensaver mode, with a photo
// to show or the weather art in place of photos.
func screensaverToShow() bool {
	h := home.Get()
	if h.SlideshowMode() != config.SlideshowScreensaver {
		return false
	}
	return h.SlideshowArt() || h.SlideshowScreensaverPhoto() != nil
}

// screensaverButton is Home Assistant's Show screensaver button.
func screensaverButton(d *Display) *esphome.Button {
	return &esphome.Button{
		Base:    esphome.Base{ObjectID: "slideshow_screensaver_show", Name: "Show screensaver", Icon: "mdi:image-multiple"},
		OnPress: d.showScreensaver,
	}
}

// screensaverHideButton is Home Assistant's Hide screensaver button.
func screensaverHideButton(d *Display) *esphome.Button {
	return &esphome.Button{
		Base:    esphome.Base{ObjectID: "slideshow_screensaver_hide", Name: "Hide screensaver", Icon: "mdi:image-off"},
		OnPress: d.hideScreensaver,
	}
}
