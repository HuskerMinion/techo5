package home

import (
	"slices"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

func screensaver(s *config.Slideshow) { s.Mode = config.SlideshowScreensaver }

// A swipe goes to the next photo or back through the ones seen, and the photo it turns to is held past
// its own time (#502).
func TestSwipingThroughTheScreensaver(t *testing.T) {
	f := slideshowFeature(t, &moreImmich{}, screensaver, wide1, wide2, wide3)
	if f.StepSlideshow(+1) {
		t.Fatal("a swipe with no photo up stepped")
	}
	f.advanceSlideshow()
	due(f)
	f.advanceSlideshow()
	if got := shownNow(f); !slices.Equal(got, []string{wide2}) {
		t.Fatalf("shown %v, want the second photo", got)
	}

	f.step(-1)
	if got := shownNow(f); !slices.Equal(got, []string{wide1}) {
		t.Fatalf("after a swipe back, shown %v, want the first photo again", got)
	}
	f.mu.Lock()
	first := f.slideshow.pos == 0
	f.mu.Unlock()
	if !first || f.StepSlideshow(-1) {
		t.Error("a swipe back from the first photo kept stepped")
	}

	// Held: its time over, the slideshow still leaves it up.
	due(f)
	f.advanceSlideshow()
	if got := shownNow(f); !slices.Equal(got, []string{wide1}) {
		t.Fatalf("a swiped-to photo was not held: shown %v", got)
	}

	// Once the hold is over, the slideshow carries on through what was seen before fetching new ones.
	f.mu.Lock()
	f.slideshow.holdUntil = time.Time{}
	f.mu.Unlock()
	due(f)
	f.advanceSlideshow()
	if got := shownNow(f); !slices.Equal(got, []string{wide2}) {
		t.Fatalf("after the hold, shown %v, want the photo seen next", got)
	}

	// A swipe forward from the newest photo fetches the next one in the list.
	f.step(+1)
	if got := shownNow(f); !slices.Equal(got, []string{wide3}) {
		t.Fatalf("a swipe forward showed %v, want the next photo", got)
	}
	f.mu.Lock()
	n, pos := len(f.slideshow.hist), f.slideshow.pos
	f.mu.Unlock()
	if n != 3 || pos != 2 {
		t.Errorf("history %d photos at %d, want 3 at 2", n, pos)
	}
}

// A photo seen before that can't be fetched again (deleted since) is let go of: after a swipe back the
// slideshow carries on with a new photo rather than trying the dead one every tick for good, and a swipe
// that put nothing new up holds nothing.
func TestAPhotoGoneSinceDoesNotStallTheSlideshow(t *testing.T) {
	m := &moreImmich{}
	f := slideshowFeature(t, m, screensaver, wide1, wide2, wide3)
	f.advanceSlideshow()
	due(f)
	f.advanceSlideshow()
	f.step(-1)
	if got := shownNow(f); !slices.Equal(got, []string{wide1}) {
		t.Fatalf("after a swipe back, shown %v", got)
	}
	m.mu.Lock()
	m.gone = map[string]bool{wide2: true}
	m.mu.Unlock()

	f.mu.Lock()
	f.slideshow.holdUntil = time.Time{}
	f.mu.Unlock()
	due(f)
	f.advanceSlideshow()
	if got := shownNow(f); slices.Equal(got, []string{wide1}) || slices.Equal(got, []string{wide2}) {
		t.Fatalf("with the next photo seen gone, shown %v, want a new one", got)
	}
	f.mu.Lock()
	for _, p := range f.slideshow.hist {
		if p.ids[0] == wide2 {
			t.Error("the photo that's gone is still ahead in the history")
		}
	}
	f.mu.Unlock()

	// A swipe back onto a photo that's gone puts nothing up, holds nothing, and takes it out, so the
	// next swipe back isn't stopped by it again.
	m.mu.Lock()
	m.gone[wide1] = true
	m.mu.Unlock()
	f.mu.Lock()
	n := len(f.slideshow.hist)
	f.mu.Unlock()
	f.step(-1)
	f.mu.Lock()
	held, after := !f.slideshow.holdUntil.IsZero(), len(f.slideshow.hist)
	f.mu.Unlock()
	if held {
		t.Error("a swipe that put nothing up held the photo there")
	}
	if after != n-1 {
		t.Errorf("history %d after a swipe back onto a photo that's gone, want %d", after, n-1)
	}
}

// A source that is only down for a while doesn't wear the history away: the slideshow going on by
// itself takes nothing out when the next photo seen can't be fetched.
func TestAnOutageKeepsTheHistory(t *testing.T) {
	m := &moreImmich{}
	f := slideshowFeature(t, m, screensaver, wide1, wide2, wide3)
	f.advanceSlideshow()
	due(f)
	f.advanceSlideshow()
	f.step(-1)
	m.mu.Lock()
	m.gone = map[string]bool{wide1: true, wide2: true, wide3: true}
	m.mu.Unlock()
	f.mu.Lock()
	f.slideshow.holdUntil = time.Time{}
	f.mu.Unlock()
	for range 3 {
		due(f)
		f.advanceSlideshow()
	}
	f.mu.Lock()
	n := len(f.slideshow.hist)
	f.mu.Unlock()
	if n != 2 {
		t.Errorf("history %d photos after an outage, want the 2 seen kept", n)
	}
}

// Swipes are for the screensaver: in Background mode the swipe is the clock's.
func TestNoSwipingTheBackground(t *testing.T) {
	f := slideshowFeature(t, &moreImmich{}, func(*config.Slideshow) {}, wide1, wide2)
	f.advanceSlideshow()
	if f.StepSlideshow(+1) {
		t.Error("a swipe stepped the background's photos")
	}
}

// The history keeps the last slideshowHistory photos, and a new photo after a swipe back drops the ones
// that had been ahead.
func TestTheHistoryIsBounded(t *testing.T) {
	f := &Feature{}
	for i := range slideshowHistory + 5 {
		f.remember(shownPhoto{ids: []string{string(rune('a' + i))}})
	}
	if len(f.slideshow.hist) != slideshowHistory || f.slideshow.pos != slideshowHistory-1 {
		t.Fatalf("history %d at %d", len(f.slideshow.hist), f.slideshow.pos)
	}
	f.slideshow.pos = 3
	f.remember(shownPhoto{ids: []string{"new"}})
	if len(f.slideshow.hist) != 5 || f.slideshow.hist[4].ids[0] != "new" || f.slideshow.pos != 4 {
		t.Errorf("after a swipe back and a new photo: %d at %d", len(f.slideshow.hist), f.slideshow.pos)
	}
}
