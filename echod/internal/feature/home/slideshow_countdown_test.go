package home

import (
	"math"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// A photo's time ends once it has had it and a swipe's hold is over, at the slideshow's next look
// from then on (#656): the countdown fills to when the photo really changes, not to when it was due.
func TestWhenAPhotosTimeEnds(t *testing.T) {
	ticks := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	at := ticks.Add(2 * time.Second) // put up just after a look, as a fetched photo is
	for _, c := range []struct {
		name  string
		every time.Duration
		hold  time.Time
		ticks time.Time
		want  time.Time
	}{
		{"no loop running", time.Minute, time.Time{}, time.Time{}, at.Add(time.Minute)},
		{"the look after it is due", time.Minute, time.Time{}, ticks, ticks.Add(90 * time.Second)},
		{"due on a look", 58 * time.Second, time.Time{}, ticks, ticks.Add(time.Minute)},
		{"shorter than a look apart", 5 * time.Second, time.Time{}, ticks, ticks.Add(30 * time.Second)},
		{"held by a swipe", time.Minute, at.Add(5 * time.Minute), ticks, ticks.Add(330 * time.Second)},
		{"a hold already over", time.Minute, at.Add(-time.Second), time.Time{}, at.Add(time.Minute)},
		{"put up before the loop", time.Minute, time.Time{}, at.Add(2 * time.Minute), at.Add(time.Minute)},
	} {
		if got := photoEnd(at, c.every, c.hold, c.ticks); !got.Equal(c.want) {
			t.Errorf("%s: ends %v after it went up, want %v", c.name, got.Sub(at), c.want.Sub(at))
		}
	}
}

// How much of a photo's time has gone: none as it comes up, all once it is over, never outside 0 to 1.
func TestHowMuchOfAPhotosTimeHasGone(t *testing.T) {
	from := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	to := from.Add(time.Minute)
	for _, c := range []struct {
		now  time.Time
		want float64
	}{
		{from, 0},
		{from.Add(15 * time.Second), 0.25},
		{from.Add(30 * time.Second), 0.5},
		{to, 1},
		{to.Add(time.Hour), 1},
		{from.Add(-time.Second), 0},
	} {
		if got := photoDone(c.now, from, to); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("at %v: %v gone, want %v", c.now.Sub(from), got, c.want)
		}
	}
	if got := photoDone(from, from, from.Add(-time.Second)); got != 1 {
		t.Errorf("a photo already due: %v gone, want all of it", got)
	}
}

// The countdown is off at first; on, it starts from nothing with each photo, a new one or one a swipe
// turned to, and there is none to draw off Screensaver mode or under the weather art (#656).
func TestThePhotoCountdown(t *testing.T) {
	f := slideshowFeature(t, &moreImmich{}, screensaver, wide1, wide2, wide3)
	if f.SlideshowCountdown() || f.slideshowCountdownSw.Get() {
		t.Fatal("the countdown is on at first")
	}
	f.advanceSlideshow()
	if _, ok := f.SlideshowProgress(time.Now()); ok {
		t.Fatal("a countdown to draw with it off")
	}

	f.SetSlideshowCountdown(true)
	if !config.Get().Home.Slideshow.Countdown || !f.SlideshowCountdown() || !f.slideshowCountdownSw.Get() {
		t.Fatal("turning it on did not save it, or Home Assistant was not told")
	}
	progress := func(at time.Time) float64 {
		t.Helper()
		p, ok := f.SlideshowProgress(at)
		if !ok {
			t.Fatal("no countdown to draw with it on and a photo up")
		}
		return p
	}
	if p := progress(time.Now()); p > 0.05 {
		t.Errorf("a photo just up is %v gone", p)
	}
	f.mu.Lock()
	at := f.slideshow.at
	f.mu.Unlock()
	if p := progress(at.Add(30 * time.Second)); math.Abs(p-0.5) > 1e-6 {
		t.Errorf("half its minute in, %v gone, want half", p)
	}

	// The next photo starts it again.
	due(f)
	if p := progress(time.Now()); p != 1 {
		t.Errorf("a photo due is %v gone, want all of it", p)
	}
	f.advanceSlideshow()
	if p := progress(time.Now()); p > 0.05 {
		t.Errorf("the next photo starts %v gone", p)
	}

	// So does a swipe, and the photo swiped to has the swipe's hold for its time.
	f.mu.Lock()
	f.slideshow.at = time.Now().Add(-50 * time.Second)
	f.mu.Unlock()
	if p := progress(time.Now()); p < 0.8 {
		t.Fatalf("50 seconds into its minute, %v gone", p)
	}
	f.step(+1)
	f.mu.Lock()
	at, hold := f.slideshow.at, f.slideshow.holdUntil
	f.mu.Unlock()
	if p := progress(time.Now()); p > 0.05 {
		t.Errorf("a photo swiped to starts %v gone", p)
	}
	if p := progress(at.Add(hold.Sub(at) / 2)); math.Abs(p-0.5) > 1e-6 {
		t.Errorf("halfway through the hold, %v gone, want half", p)
	}

	f.SetSlideshowArt(true)
	if _, ok := f.SlideshowProgress(time.Now()); ok {
		t.Error("a countdown over the weather art, which has no photos to count")
	}
	f.SetSlideshowArt(false)
	f.ChooseSlideshowMode(config.SlideshowBackground)
	if _, ok := f.SlideshowProgress(time.Now()); ok {
		t.Error("a countdown in Background mode, behind the clock")
	}

	f.SetSlideshowCountdown(false)
	if config.Get().Home.Slideshow.Countdown || f.slideshowCountdownSw.Get() {
		t.Error("turning it off did not save it, or Home Assistant was not told")
	}
}
