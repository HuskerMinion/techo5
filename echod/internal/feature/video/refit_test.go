//go:build !dot

package video

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// filterOf is the -vf a decode was started with.
func filterOf(a []string) string {
	if i := slices.Index(a, "-vf"); i >= 0 && i+1 < len(a) {
		return a[i+1]
	}
	return ""
}

// The button on the video page goes round Fit, Fill and Stretch for the video playing (#733): each
// change starts the decoder again where the video is, under the same id and without asking the server
// again, the picture made the new way; a seek keeps the way, a pause stays a pause, and the next video
// starts at Fit again.
func TestRefitGoesRoundAndStartsAgainWhereItIs(t *testing.T) {
	f := fake(t, 100000, true)
	log := recordRuns(t)
	id, err := f.Play(Request{URL: "http://192.168.1.20/a.mp4", Title: "A film"})
	if err != nil {
		t.Fatal(err)
	}
	if st := until(t, f, Playing); st.Aspect != AspectFit {
		t.Fatalf("a new video takes the screen as %v", st.Aspect)
	}
	if ok, known := f.Refittable(id); !ok || !known {
		t.Fatalf("a video with a length: refittable %v, known %v", ok, known)
	}
	if err := f.Seek(id, 90*time.Second); err != nil {
		t.Fatal(err)
	}
	until(t, f, Playing)

	scr := Screen{W: testH, H: testW, Rotated: true, PixFmt: "bgra"}
	info, _ := ParseProbe(probeMP4)
	for n, want := range []Aspect{AspectFill, AspectStretch, AspectFit} {
		got, err := f.Refit(id)
		if err != nil || got != want {
			t.Fatalf("refit %d: %v %v, want %v", n, got, err, want)
		}
		st := until(t, f, Playing)
		if st.ID != id || st.Aspect != want || st.Pos < 90*time.Second || st.Title != "A film" {
			t.Errorf("after refit %d: %+v", n, st)
		}
		probes, decodes := runs(t, log)
		if probes != 1 || len(decodes) != 3+n {
			t.Fatalf("refit %d: %d probes and %d decodes", n, probes, len(decodes))
		}
		last := decodes[len(decodes)-1]
		if at, _ := strconv.ParseFloat(startsAt(last), 64); at < 90 || at > 100 {
			t.Errorf("refit %d started the decoder at %q", n, startsAt(last))
		}
		if got := filterOf(last); got != Filter(info, scr, want) {
			t.Errorf("refit %d: the filter %s, want %s", n, got, Filter(info, scr, want))
		}
	}

	// A seek keeps the way the picture takes the screen.
	if _, err := f.Refit(id); err != nil {
		t.Fatal(err)
	}
	until(t, f, Playing)
	if err := f.Seek(id, 30*time.Second); err != nil {
		t.Fatal(err)
	}
	if st := until(t, f, Playing); st.Aspect != AspectFill {
		t.Errorf("after a seek: %v", st.Aspect)
	}
	_, decodes := runs(t, log)
	if !strings.Contains(filterOf(decodes[len(decodes)-1]), "crop=") {
		t.Errorf("the seek's decode was not filled: %v", decodes[len(decodes)-1])
	}

	// Paused, a refit leaves it paused.
	f.Pause()
	until(t, f, Paused)
	if got, err := f.Refit(id); err != nil || got != AspectStretch {
		t.Fatalf("refit while paused: %v %v", got, err)
	}
	if st := until(t, f, Paused); st.Aspect != AspectStretch {
		t.Errorf("paused: %+v", st)
	}
	f.Resume()
	until(t, f, Playing)

	// Another video's id changes nothing.
	if _, err := f.Refit(id + 1); !errors.Is(err, ErrNotSeekable) {
		t.Errorf("another id: %v", err)
	}
	if st := f.State(); st.Aspect != AspectStretch {
		t.Errorf("another id's refit changed this one: %v", st.Aspect)
	}

	// The next video starts whole, whatever the last one was.
	next, err := f.Play(Request{URL: "http://192.168.1.20/b.mp4"})
	if err != nil {
		t.Fatal(err)
	}
	if st := until(t, f, Playing); st.ID != next || st.Aspect != AspectFit {
		t.Errorf("the next video: %+v", st)
	}
	_, decodes = runs(t, log)
	if got := filterOf(decodes[len(decodes)-1]); got != Filter(info, scr, AspectFit) {
		t.Errorf("the next video's filter %s", got)
	}
}

// A live stream says no length: started again it would start from wherever its server starts it, not
// where it is on the screen, so its way cannot be changed once it plays.
func TestALiveStreamCannotBeRefitted(t *testing.T) {
	f := fake(t, 100000, true)
	decoderEnv = append(decoderEnv, "TECHO5_FAKE_LIVE=1")
	log := recordRuns(t)
	id, err := f.Play(Request{URL: "http://192.168.1.20/live.ts"})
	if err != nil {
		t.Fatal(err)
	}
	until(t, f, Playing)
	if ok, known := f.Refittable(id); ok || !known {
		t.Errorf("live: refittable %v, known %v", ok, known)
	}
	if got, err := f.Refit(id); !errors.Is(err, ErrNotSeekable) || got != AspectFit {
		t.Errorf("live: %v %v", got, err)
	}
	if st := f.State(); st.Aspect != AspectFit {
		t.Errorf("live: %v", st.Aspect)
	}
	if _, decodes := runs(t, log); len(decodes) != 1 {
		t.Errorf("live: decoded %d times", len(decodes))
	}
}

// Before the decoder starts, a change is only the way it starts with.
func TestARefitBeforeTheDecodeIsHowItStarts(t *testing.T) {
	f := fake(t, 10, false)
	s := newSession(9, Request{}, nil, f.scr, f.panelW, f.panelH)
	f.mu.Lock()
	f.cur = s
	f.mu.Unlock()
	t.Cleanup(func() {
		f.mu.Lock()
		f.cur = nil
		f.mu.Unlock()
	})
	if got, err := f.Refit(9); err != nil || got != AspectFill {
		t.Fatalf("%v %v", got, err)
	}
	if s.aspect0() != AspectFill {
		t.Errorf("the session takes the screen as %v", s.aspect0())
	}
}
