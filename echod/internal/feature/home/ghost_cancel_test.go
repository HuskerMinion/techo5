package home

import (
	"testing"
	"time"
)

// A still replaced by the next one lets go of its fetch at once: the next picture's request goes out
// while the first server is still holding its answer back, and the first request is canceled rather
// than waited out.
func TestAReplacedStillsFetchIsCanceled(t *testing.T) {
	ha := newSlowHA(t)
	f := newFeature(t)
	if err := f.ShowPicture("/local/door.png", Caption{ID: "1", Message: "one"}, time.Minute, ""); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the first fetch to start", func() bool { return ha.started.Load() == 1 })
	if err := f.ShowPicture("/local/door.png", Caption{ID: "2", Message: "two"}, time.Minute, ""); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the second fetch to start with the first unanswered", func() bool { return ha.started.Load() == 2 })
	eventually(t, "the first request to be canceled", func() bool { return ha.live.Load() == 1 })
	if ha.hits.Load() != 0 {
		t.Error("a server answered before it was released")
	}
}

// A still taken down is canceled too, whichever way it goes: tapped away, or hidden by its id.
func TestAHiddenStillsFetchIsCanceled(t *testing.T) {
	for name, hide := range map[string]func(f *Feature){
		"HideCamera":  func(f *Feature) { f.HideCamera() },
		"HidePicture": func(f *Feature) { f.HidePicture("1") },
		"HideView":    func(f *Feature) { v, _ := f.Camera(); f.HideView(v.View) },
	} {
		t.Run(name, func(t *testing.T) {
			ha := newSlowHA(t)
			f := newFeature(t)
			if err := f.ShowPicture("/local/door.png", Caption{ID: "1", Message: "one"}, time.Minute, ""); err != nil {
				t.Fatal(err)
			}
			eventually(t, "the fetch to start", func() bool { return ha.started.Load() == 1 })
			hide(f)
			eventually(t, "the request to be canceled", func() bool { return ha.live.Load() == 0 })
		})
	}
}
