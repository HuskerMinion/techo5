package serve

import (
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/pkg/addonapi"
)

// quick makes the media.state timing short enough for a test, and puts it back after.
func quick(t *testing.T, coalesce, position time.Duration) {
	t.Helper()
	oc, op := mediaCoalesce, positionEvery
	mediaCoalesce, positionEvery = coalesce, position
	t.Cleanup(func() { mediaCoalesce, positionEvery = oc, op })
}

// setStatus is the fake device's media changing, and the device saying so.
func (r *rig) setStatus(st addonapi.MediaState) {
	r.d.mu.Lock()
	r.d.status = st
	r.d.mu.Unlock()
	r.s.MediaChanged("meals")
}

func next(t *testing.T, ch <-chan addonapi.MediaState) addonapi.MediaState {
	t.Helper()
	select {
	case st, ok := <-ch:
		if !ok {
			t.Fatal("the connection ended")
		}
		return st
	case <-time.After(5 * time.Second):
		t.Fatal("no media.state came")
	}
	return addonapi.MediaState{}
}

func none(t *testing.T, ch <-chan addonapi.MediaState, within time.Duration) {
	t.Helper()
	select {
	case st := <-ch:
		t.Fatalf("a media.state came that should not have: %+v", st)
	case <-time.After(within):
	}
}

// Media an add-on started is followed through its states, its end is said once with its reason, and
// media.status answers the same snapshot.
func TestMediaState(t *testing.T) {
	quick(t, 10*time.Millisecond, time.Hour)
	r := newRig(t, everything)
	c := r.dial()
	states := c.WatchMedia()
	if st, err := c.MediaStatus(ctx(t)); err != nil || st.State != addonapi.MediaIdle || st.Media != "" {
		t.Fatalf("status before any media: %+v %v", st, err)
	}
	media, err := c.StartMedia(ctx(t), addonapi.MediaVideo, "https://plex.local/film.mkv", "Film")
	if err != nil || media == "" {
		t.Fatalf("media.play's id %q: %v", media, err)
	}
	if st := next(t, states); st.Media != media || st.State != addonapi.MediaBuffering || st.Kind != addonapi.MediaVideo {
		t.Errorf("first state %+v", st)
	}
	r.setStatus(addonapi.MediaState{Media: media, Kind: "video", State: addonapi.MediaPlaying,
		PositionMs: addonapi.Ms(0), DurationMs: addonapi.Ms(2 * time.Hour)})
	if st := next(t, states); st.State != addonapi.MediaPlaying || st.DurationMs == nil || *st.DurationMs != 7200000 {
		t.Errorf("playing %+v", st)
	}
	paused := addonapi.MediaState{Media: media, Kind: "video", State: addonapi.MediaPaused,
		PositionMs: addonapi.Ms(90 * time.Second), DurationMs: addonapi.Ms(2 * time.Hour)}
	r.setStatus(paused)
	st := next(t, states)
	if at, ok := st.Position(); st.State != addonapi.MediaPaused || !ok || at != 90*time.Second {
		t.Errorf("paused %+v", st)
	}
	if got, err := c.MediaStatus(ctx(t)); err != nil || got.State != addonapi.MediaPaused || *got.PositionMs != 90000 {
		t.Errorf("status while paused: %+v %v", got, err)
	}
	// Nothing new: nothing said.
	r.s.MediaChanged("meals")
	none(t, states, 50*time.Millisecond)

	end := addonapi.MediaState{Media: media, Kind: "video", State: addonapi.MediaStopped, Reason: addonapi.MediaStoppedOnDevice,
		PositionMs: addonapi.Ms(91 * time.Second)}
	r.d.mu.Lock()
	r.d.status = end
	r.d.mu.Unlock()
	r.s.MediaEnded("meals", end)
	if st := next(t, states); st.State != addonapi.MediaStopped || st.Reason != addonapi.MediaStoppedOnDevice {
		t.Errorf("the end %+v", st)
	}
	// Its end is said once: the device going on reporting it says nothing more.
	r.s.MediaChanged("meals")
	r.s.MediaEnded("meals", end)
	none(t, states, 50*time.Millisecond)
	if got, _ := c.MediaStatus(ctx(t)); got.State != addonapi.MediaStopped || got.Reason != addonapi.MediaStoppedOnDevice {
		t.Errorf("status after the end: %+v", got)
	}
}

// A burst of changes within the coalescing window is one event, the latest.
func TestMediaStateCoalesced(t *testing.T) {
	quick(t, 200*time.Millisecond, time.Hour)
	r := newRig(t, everything)
	c := r.dial()
	states := c.WatchMedia()
	r.setStatus(addonapi.MediaState{Media: "7", Kind: "audio", State: addonapi.MediaBuffering})
	r.setStatus(addonapi.MediaState{Media: "7", Kind: "audio", State: addonapi.MediaPlaying})
	r.setStatus(addonapi.MediaState{Media: "7", Kind: "audio", State: addonapi.MediaPaused})
	r.setStatus(addonapi.MediaState{Media: "7", Kind: "audio", State: addonapi.MediaPlaying})
	if st := next(t, states); st.State != addonapi.MediaPlaying {
		t.Errorf("the burst said %+v", st)
	}
	none(t, states, 300*time.Millisecond)
}

// While media plays its position is said at most every positionEvery, and not at all while paused.
func TestMediaStatePosition(t *testing.T) {
	quick(t, 10*time.Millisecond, 100*time.Millisecond)
	r := newRig(t, everything)
	c := r.dial()
	states := c.WatchMedia()
	began := time.Now()
	playing := func() addonapi.MediaState {
		return addonapi.MediaState{Media: "3", Kind: "audio", State: addonapi.MediaPlaying, PositionMs: addonapi.Ms(time.Since(began))}
	}
	r.setStatus(playing())
	// The device nudging far more often than the position may be said.
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(5 * time.Millisecond):
				r.setStatus(playing())
			}
		}
	}()
	var got []time.Time
	for deadline := time.After(550 * time.Millisecond); ; {
		select {
		case <-states:
			got = append(got, time.Now())
			continue
		case <-deadline:
		}
		break
	}
	close(stop)
	if len(got) < 3 || len(got) > 7 {
		t.Errorf("%d positions in 550 ms at one every 100 ms", len(got))
	}
	for i := 1; i < len(got); i++ {
		if d := got[i].Sub(got[i-1]); d < 80*time.Millisecond {
			t.Errorf("positions %v apart", d)
		}
	}
	// Paused, the looks stop: no timer goes on asking the device.
	r.setStatus(addonapi.MediaState{Media: "3", Kind: "audio", State: addonapi.MediaPaused, PositionMs: addonapi.Ms(time.Second)})
	for len(states) > 0 {
		<-states
	}
	if st := next(t, states); st.State != addonapi.MediaPaused {
		// The last playing one may have been on its way; the next is the pause.
		if st = next(t, states); st.State != addonapi.MediaPaused {
			t.Errorf("after the pause %+v", st)
		}
	}
	none(t, states, 250*time.Millisecond)
}

// An add-on's own next media ends its last first: the end is said before the next one's state, and the
// add-on can tell them apart by id.
func TestMediaStateReplacedOrder(t *testing.T) {
	quick(t, 10*time.Millisecond, time.Hour)
	r := newRig(t, everything)
	c := r.dial()
	states := c.WatchMedia()
	r.setStatus(addonapi.MediaState{Media: "1", Kind: "video", State: addonapi.MediaPlaying})
	next(t, states)
	r.s.MediaEnded("meals", addonapi.MediaState{Media: "1", Kind: "video", State: addonapi.MediaStopped, Reason: addonapi.MediaReplaced})
	r.setStatus(addonapi.MediaState{Media: "2", Kind: "audio", State: addonapi.MediaPlaying})
	if st := next(t, states); st.Media != "1" || st.Reason != addonapi.MediaReplaced {
		t.Errorf("first %+v", st)
	}
	if st := next(t, states); st.Media != "2" || st.State != addonapi.MediaPlaying {
		t.Errorf("then %+v", st)
	}
}

// An add-on closed stops being looked at.
func TestMediaStateClosed(t *testing.T) {
	quick(t, 10*time.Millisecond, 20*time.Millisecond)
	r := newRig(t, everything)
	r.dial()
	r.setStatus(addonapi.MediaState{Media: "1", Kind: "audio", State: addonapi.MediaPlaying, PositionMs: addonapi.Ms(0)})
	time.Sleep(30 * time.Millisecond)
	a := r.s.addon("meals")
	r.s.Close("meals")
	a.media.mu.Lock()
	timer, closed := a.media.timer, a.media.closed
	a.media.mu.Unlock()
	if timer != nil || !closed {
		t.Error("a closed add-on's media is still looked at")
	}
	r.s.MediaChanged("meals") // nothing there: nothing happens
}
