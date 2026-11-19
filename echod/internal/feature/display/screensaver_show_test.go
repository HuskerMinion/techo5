//go:build !dot

package display

import (
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// slideshowFor sets the slideshow's mode for a test, with the weather art in place of photos so there
// is something to show without fetching one, and puts the setting back afterwards.
func slideshowFor(t *testing.T, mode string) {
	t.Helper()
	was := config.Get().Home.Slideshow
	t.Cleanup(func() { _ = config.Set().Home().Slideshow(was) })
	s := was
	s.Mode, s.Art = mode, true
	if err := config.Set().Home().Slideshow(s); err != nil {
		t.Fatal(err)
	}
}

// A press puts the screensaver up on the next frame: the idle wait reads as run out, and the frame
// loop is woken to draw it.
func TestShowScreensaverStartsTheWaitThatFarBack(t *testing.T) {
	slideshowFor(t, config.SlideshowScreensaver)
	now := time.Now()
	d := &Display{poke: make(chan struct{}, 1), on: true, slideshowIdleSince: now.Add(-time.Second)}

	d.showScreensaver()

	if wait := home.Get().SlideshowIdleTimeout(); time.Since(d.slideshowIdleSince) < wait {
		t.Errorf("idle since %v ago, want at least the wait, %v", time.Since(d.slideshowIdleSince), wait)
	}
	select {
	case <-d.poke:
	default:
		t.Error("the frame loop was not woken")
	}
}

// A press leaves the screen alone when there is no screensaver to show, when the screen is out, and
// when something is going on (the last frame was not the idle page, so the wait is not running).
func TestShowScreensaverDoesNothingWhenItCannot(t *testing.T) {
	since := time.Now().Add(-time.Second)
	for _, c := range []struct {
		name  string
		mode  string
		on    bool
		since time.Time
	}{
		{"background mode", config.SlideshowBackground, true, since},
		{"slideshow off", "", true, since},
		{"screen out", config.SlideshowScreensaver, false, since},
		{"screen in use", config.SlideshowScreensaver, true, time.Time{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			slideshowFor(t, c.mode)
			d := &Display{poke: make(chan struct{}, 1), on: c.on, slideshowIdleSince: c.since}

			d.showScreensaver()

			if !d.slideshowIdleSince.Equal(c.since) {
				t.Errorf("idle since moved from %v to %v", c.since, d.slideshowIdleSince)
			}
			select {
			case <-d.poke:
				t.Error("the frame loop was woken for nothing")
			default:
			}
		})
	}
}
