package home

import (
	"slices"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// home_slideshow with the source already showing reads it again (#180): the photo up stays up, the
// next look is due at once and gathers the list afresh, so a photo added since shows without the
// source being cleared first, and without the screen going dark in between.
func TestTheSameSourceAgainReadsItAgain(t *testing.T) {
	m := &moreImmich{}
	// The list as it was gathered before: two photos. Immich now has another (its search answers tallA).
	f := slideshowFeature(t, m, func(*config.Slideshow) {}, wide1, wide2)
	f.advanceSlideshow()
	if got := shownNow(f); !slices.Equal(got, []string{wide1}) {
		t.Fatalf("shown %v, want the first of the list", got)
	}
	m.mu.Lock()
	asked := len(m.searches)
	m.mu.Unlock()

	f.SetSlideshowSource(ImmichAll)
	if config.Get().Home.Slideshow.Source != ImmichAll {
		t.Fatal("the source changed")
	}
	if f.currentSlideshowPhoto() == nil || !slices.Equal(shownNow(f), []string{wide1}) {
		t.Fatal("the photo up went before the list was read again")
	}

	f.advanceSlideshow()
	m.mu.Lock()
	again := len(m.searches) > asked
	m.mu.Unlock()
	if !again {
		t.Fatal("Immich was not asked for the photos again")
	}
	if got := shownNow(f); !slices.Equal(got, []string{tallA}) {
		t.Errorf("then shown %v, want the photo from the list read again", got)
	}
}

// An empty source with none set is still nothing to read again: it changes nothing.
func TestNoSourceAgainIsNothing(t *testing.T) {
	m := &moreImmich{}
	f := slideshowFeature(t, m, func(s *config.Slideshow) { s.Source = "" })
	f.SetSlideshowSource("")
	if f.currentSlideshowPhoto() != nil || config.Get().Home.Slideshow.Source != "" {
		t.Error("an empty source set again did something")
	}
}

// A slideshow in order read again keeps its place (#180): with the photo up still in the list, the next
// is the one after it there, not the first. And a second re-read straight after the first (a script that
// copies photos in one at a time and asks after each) reads the list again without cutting short the
// time of the photo the first one put up.
func TestAReadAgainKeepsThePlaceAndHurriesOnce(t *testing.T) {
	m := &moreImmich{found: []string{wide1, wide2, wide3, tallA}}
	f := slideshowFeature(t, m, func(*config.Slideshow) {}, wide1, wide2, wide3)
	f.advanceSlideshow()
	due(f)
	f.advanceSlideshow()
	if got := shownNow(f); !slices.Equal(got, []string{wide2}) {
		t.Fatalf("shown %v, want the second of the list", got)
	}

	f.SetSlideshowSource(ImmichAll)
	f.advanceSlideshow()
	if got := shownNow(f); !slices.Equal(got, []string{wide3}) {
		t.Fatalf("after the re-read shown %v; want the one after %s, its place kept", got, wide2)
	}

	m.mu.Lock()
	asked := len(m.searches)
	m.mu.Unlock()
	f.SetSlideshowSource(ImmichAll)
	f.advanceSlideshow()
	if got := shownNow(f); !slices.Equal(got, []string{wide3}) {
		t.Errorf("a second re-read cut the photo's time short too: shown %v", got)
	}
	due(f)
	f.advanceSlideshow()
	m.mu.Lock()
	again := len(m.searches) > asked
	m.mu.Unlock()
	if got := shownNow(f); !again || !slices.Equal(got, []string{tallA}) {
		t.Errorf("at the photo's own time: read again %v, shown %v; want the list read again and %s", again, got, tallA)
	}

	// Once a photo has taken over at its own time, a re-read hurries the next one again.
	f.SetSlideshowSource(ImmichAll)
	f.advanceSlideshow()
	if got := shownNow(f); !slices.Equal(got, []string{wide1}) {
		t.Errorf("a later re-read: shown %v, want the list from the start after its last photo", got)
	}
}

// A shuffled slideshow read again, or one whose photo up has gone from the folder, starts the new list
// from its beginning, as before.
func TestAReadAgainWithThePhotoGoneStartsOver(t *testing.T) {
	m := &moreImmich{found: []string{wide3, tallA}}
	f := slideshowFeature(t, m, func(*config.Slideshow) {}, wide1, wide2)
	f.advanceSlideshow()
	f.SetSlideshowSource(ImmichAll)
	f.advanceSlideshow()
	if got := shownNow(f); !slices.Equal(got, []string{wide3}) {
		t.Errorf("shown %v, want the first of the new list", got)
	}
}
