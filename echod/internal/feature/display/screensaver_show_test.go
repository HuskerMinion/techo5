//go:build !dot

package display

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// slideshowFor sets the slideshow's mode for a test, with the weather art in place of photos so there
// is something to show without fetching one, and puts the setting back afterwards.
func slideshowFor(t *testing.T, mode string) {
	t.Helper()
	// A store of the test's own, so these run alone (-run) as well as after a test that set one.
	config.Use(filepath.Join(t.TempDir(), "state.json"))
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

// Hide screensaver takes an up screensaver down (#744): the wait starts again from the press, so the
// next frame is the page under it, and the frame loop is woken to draw that.
func TestHideScreensaverTakesItDown(t *testing.T) {
	slideshowFor(t, config.SlideshowScreensaver)
	wait := home.Get().SlideshowIdleTimeout()
	d := &Display{poke: make(chan struct{}, 1), on: true, saverUp: true, slideshowIdleSince: time.Now().Add(-wait - time.Second)}

	d.hideScreensaver()

	if since := time.Since(d.slideshowIdleSince); since >= wait {
		t.Errorf("idle since %v ago, the screensaver would still be up after a wait of %v", since, wait)
	}
	if d.saverUp {
		t.Error("swipes still go to the photos")
	}
	select {
	case <-d.poke:
	default:
		t.Error("the frame loop was not woken")
	}
}

// Show then Hide is the screensaver up and down again, for an automation that runs it itself.
func TestShowThenHideScreensaver(t *testing.T) {
	slideshowFor(t, config.SlideshowScreensaver)
	wait := home.Get().SlideshowIdleTimeout()
	d := &Display{poke: make(chan struct{}, 1), on: true, slideshowIdleSince: time.Now().Add(-time.Second)}

	d.showScreensaver()
	<-d.poke
	d.hideScreensaver()

	if time.Since(d.slideshowIdleSince) >= wait {
		t.Error("the screensaver is still due after Hide")
	}
}

// A press with no screensaver up leaves the wait as it is: one partway through keeps running, and a
// screen in use, or no screensaver to show at all, is left alone.
func TestHideScreensaverDoesNothingWithNoneUp(t *testing.T) {
	partway := time.Now().Add(-time.Second)
	for _, c := range []struct {
		name  string
		mode  string
		since time.Time
	}{
		{"wait running", config.SlideshowScreensaver, partway},
		{"screen in use", config.SlideshowScreensaver, time.Time{}},
		{"background mode", config.SlideshowBackground, time.Now().Add(-2 * time.Hour)},
	} {
		t.Run(c.name, func(t *testing.T) {
			slideshowFor(t, c.mode)
			d := &Display{poke: make(chan struct{}, 1), on: true, slideshowIdleSince: c.since}

			d.hideScreensaver()

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
