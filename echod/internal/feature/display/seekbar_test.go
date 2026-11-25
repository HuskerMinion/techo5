//go:build !dot

package display

import (
	"errors"
	"image"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/dlna"
	"github.com/HuskerMinion/techo5/echod/internal/feature/video"
)

// A finger on a bar is at the place in proportion to how far along the track it is, and past either
// end it is at that end. It is on the bar within the bar's reach, and never on one that cannot seek.
func TestTheBarsPlaceUnderAFinger(t *testing.T) {
	z := seekZone{
		view:  seekView{route: seekVideo, dur: 400 * time.Second},
		bar:   image.Rect(100, 200, 500, 206),
		reach: image.Rect(92, 187, 508, 219),
	}
	for x, want := range map[int]time.Duration{100: 0, 300: 200 * time.Second, 450: 350 * time.Second, 500: 400 * time.Second, 40: 0, 900: 400 * time.Second} {
		if got := z.at(x); got != want {
			t.Errorf("at x %d: %v, want %v", x, got, want)
		}
	}
	for p, want := range map[image.Point]bool{{300, 203}: true, {95, 188}: true, {507, 218}: true, {300, 180}: false, {300, 225}: false, {80, 203}: false} {
		if got := z.on(p); got != want {
			t.Errorf("on %v: %v, want %v", p, got, want)
		}
	}
	shows := z
	shows.view.route = seekNone
	if shows.on(image.Pt(300, 203)) {
		t.Error("a bar that only shows took a finger")
	}
	if (seekZone{view: seekView{route: seekMusic}, bar: z.bar, reach: z.reach}).on(image.Pt(300, 203)) {
		t.Error("a bar with no length took a finger")
	}
	for _, c := range []struct {
		w        int
		pos, dur time.Duration
		want     int
	}{{400, 100 * time.Second, 400 * time.Second, 100}, {400, -time.Second, 400 * time.Second, 0}, {400, time.Hour, 400 * time.Second, 400}, {400, time.Second, 0, 0}} {
		if got := seekFill(c.w, c.pos, c.dur); got != c.want {
			t.Errorf("fill %d for %v of %v: %d, want %d", c.w, c.pos, c.dur, got, c.want)
		}
	}
}

// seekFakes puts ways to seek of the test's own in place of the video player's and the renderer's.
func seekFakes(t *testing.T) (videoTo chan [2]int64, dlnaTo chan time.Duration) {
	t.Helper()
	oldVideo, oldDLNA, oldCan, oldVideoWay, oldSongWay := seekVideoTo, seekDLNATo, videoCanSeek, dlnaVideoWay, dlnaSongWay
	oldStart := dlnaVideoStart
	t.Cleanup(func() {
		seekVideoTo, seekDLNATo, videoCanSeek, dlnaVideoWay, dlnaSongWay = oldVideo, oldDLNA, oldCan, oldVideoWay, oldSongWay
		dlnaVideoStart = oldStart
	})
	dlnaVideoStart = func(uint64) time.Duration { return 0 }
	videoTo, dlnaTo = make(chan [2]int64, 4), make(chan time.Duration, 4)
	seekVideoTo = func(id uint64, to time.Duration) error {
		videoTo <- [2]int64{int64(id), int64(to)}
		return nil
	}
	seekDLNATo = func(_ uint64, to time.Duration) bool {
		dlnaTo <- to
		return true
	}
	videoCanSeek = func(uint64) (bool, bool) { return false, true }
	dlnaVideoWay = func(uint64) (dlna.SeekWay, time.Duration) { return dlna.SeekNone, 0 }
	dlnaSongWay = func() (dlna.SeekWay, time.Duration) { return dlna.SeekNone, 0 }
	return videoTo, dlnaTo
}

// A video from Home Assistant is moved by the player, when the player can move through it; a DLNA
// video goes to the renderer, which moves it or asks its controller (either way it is the renderer's
// to decide, with its own length for a stream the player has none for). Nobody able to: no bar.
func TestHowAVideoIsMovedFromItsBar(t *testing.T) {
	seekFakes(t)
	ha := video.State{Phase: video.Playing, ID: 3, Origin: video.FromHomeAssistant, Dur: 10 * time.Minute}
	if v := videoSeekRoute(ha); v.route != seekNone {
		t.Errorf("a video the player cannot move through: %v", v.route)
	}
	videoCanSeek = func(id uint64) (bool, bool) { return id == 3, true }
	if v := videoSeekRoute(ha); v.route != seekVideo || v.id != 3 || v.dur != 10*time.Minute {
		t.Errorf("a video the player can move through: %+v", v)
	}
	live := ha
	live.Dur = 0
	if v := videoSeekRoute(live); v.tappable() {
		t.Errorf("a live stream: %+v", v)
	}
	asking := ha
	asking.Phase = video.Asking
	if v := videoSeekRoute(asking); v.route != seekNone {
		t.Errorf("a video still asking: %v", v.route)
	}

	cast := video.State{Phase: video.Playing, ID: 4, Origin: video.FromDLNA}
	if v := videoSeekRoute(cast); v.route != seekNone {
		t.Errorf("a DLNA video nobody can move: %v", v.route)
	}
	for _, way := range []dlna.SeekWay{dlna.SeekHere, dlna.SeekAsk} {
		dlnaVideoWay = func(id uint64) (dlna.SeekWay, time.Duration) {
			if id != 4 {
				return dlna.SeekNone, 0
			}
			return way, 42 * time.Minute
		}
		if v := videoSeekRoute(cast); v.route != seekDLNA || v.dur != 42*time.Minute {
			t.Errorf("a DLNA video (way %v): %+v", way, v)
		}
	}
}

// A place chosen goes the bar's way, and only that way.
func TestASeekGoesTheBarsWay(t *testing.T) {
	videoTo, dlnaTo := seekFakes(t)
	seekRun(seekView{route: seekVideo, id: 3, dur: time.Hour}, 90*time.Second)
	select {
	case got := <-videoTo:
		if got != [2]int64{3, int64(90 * time.Second)} {
			t.Errorf("the player was asked %v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the player was not asked")
	}
	seekRun(seekView{route: seekDLNA, dur: time.Hour}, 95*time.Second)
	select {
	case got := <-dlnaTo:
		if got != 95*time.Second {
			t.Errorf("the renderer was asked %v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the renderer was not asked")
	}
	seekRun(seekView{}, time.Minute)
	select {
	case <-videoTo:
		t.Error("a bar that only shows moved the video")
	case <-dlnaTo:
		t.Error("a bar that only shows moved the renderer")
	case <-time.After(50 * time.Millisecond):
	}
	// The player saying no is only logged.
	// It is waited for, so that the player of the test's own is not put back while it is being asked.
	refused := make(chan struct{})
	seekVideoTo = func(uint64, time.Duration) error {
		defer close(refused)
		return errors.New("no")
	}
	seekRun(seekView{route: seekVideo, id: 3, dur: time.Hour}, time.Minute)
	select {
	case <-refused:
	case <-time.After(5 * time.Second):
		t.Fatal("the player was not asked")
	}
}

// The bar shows the place under a finger sliding along it; once sent, it shows that place until the
// source has caught up with it or seekHeld has passed, never another bar's.
func TestTheBarShowsThePlaceChosen(t *testing.T) {
	d := &Display{}
	v := seekView{route: seekDLNA, dur: 10 * time.Minute}
	now := time.Now()
	d.mu.Lock()
	defer d.mu.Unlock()
	if got := d.seekShowLocked(v, time.Minute, now); got.shown {
		t.Errorf("nothing chosen: %+v", got)
	}
	d.seekF.moving, d.seekF.zone, d.seekF.to = true, seekZone{view: v}, 4*time.Minute
	if got := d.seekShowLocked(v, time.Minute, now); !got.shown || got.at(time.Minute) != 4*time.Minute {
		t.Errorf("a finger at 4:00: %+v", got)
	}
	if got := d.seekShowLocked(seekView{route: seekMusic, dur: time.Hour}, time.Minute, now); got.shown {
		t.Errorf("another bar showed the finger's place: %+v", got)
	}
	if got := d.seekShowLocked(seekView{}, time.Minute, now); got.shown {
		t.Errorf("a bar that only shows showed a place: %+v", got)
	}
	d.seekF.moving = false
	d.seekSentLocked(v, 4*time.Minute)
	if got := d.seekShowLocked(v, time.Minute, now); got.at(time.Minute) != 4*time.Minute {
		t.Errorf("just sent: %+v", got)
	}
	if got := d.seekShowLocked(v, time.Minute, now.Add(seekHeld+time.Second)); got.shown {
		t.Errorf("long after it was sent: %+v", got)
	}
	if got := d.seekShowLocked(v, 4*time.Minute+time.Second, now); got.shown {
		t.Errorf("once the source has caught up: %+v", got)
	}
	if got := d.seekShowLocked(v, time.Minute, now); got.shown {
		t.Errorf("after it had caught up: %+v", got)
	}
}

// A DLNA film sent again from a place, as a stream of the rest of it, is shown in the film's own time:
// the bar is the whole film's, and the player's position (the stream's) is placed after where the
// stream begins.
func TestAFilmStreamedFromAPlaceShowsTheFilmsTime(t *testing.T) {
	seekFakes(t)
	dlnaVideoWay = func(uint64) (dlna.SeekWay, time.Duration) { return dlna.SeekAsk, 97 * time.Minute }
	dlnaVideoStart = func(uint64) time.Duration { return 53 * time.Minute }
	st := video.State{ID: 5, Origin: video.FromDLNA, Phase: video.Playing, Pos: 9 * time.Second, Frames: true}
	v := videoSeekRoute(st)
	if v.dur != 150*time.Minute || v.start != 53*time.Minute {
		t.Fatalf("length %v from %v, want 2h30m from 53m", v.dur, v.start)
	}
	if got := v.at(st.Pos); got != 53*time.Minute+9*time.Second {
		t.Errorf("shown at %v, want 53m9s", got)
	}
}
