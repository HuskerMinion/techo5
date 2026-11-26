//go:build !dot && !spot

package display

import (
	"image"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/dlna"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/feature/video"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
)

// A finger that comes down on the bar and sets off up or down is a swipe (the volume on Now Playing,
// a swipe down to stop a video), not a seek: the bar lets it go at its first Drag, with follow mode
// off again, and nothing is sent. One that sets off along the bar stays the bar's wherever it wanders.
func TestASwipeStartingOnTheBarIsNotASeek(t *testing.T) {
	d, to := seekDisplay(t)
	d.seekLanded(image.Pt(300, 203))
	d.mu.Lock()
	followed := d.seekF.follow
	d.mu.Unlock()
	if !followed {
		t.Fatal("the finger on the bar was not followed")
	}
	if !d.seekGesture(touch.Gesture{Kind: touch.Hold, X: 300, Y: 203, Moved: true}) {
		t.Fatal("the hold was not the bar's")
	}
	if !d.seekGesture(touch.Gesture{Kind: touch.Drag, X: 304, Y: 218}) {
		t.Fatal("the drag that hands the finger back was left to the page")
	}
	d.mu.Lock()
	f := d.seekF
	d.mu.Unlock()
	if f.moving || f.landed || f.follow {
		t.Errorf("after a drag down: moving %v, landed %v, follow %v", f.moving, f.landed, f.follow)
	}
	if d.seekGesture(touch.Gesture{Kind: touch.SwipeDown, X: 300, Y: 203}) ||
		d.seekGesture(touch.Gesture{Kind: touch.Drag, X: 400, Y: 260}) || d.seekGesture(touch.Gesture{Kind: touch.Release, X: 400, Y: 260}) {
		t.Error("the swipe was taken for the bar's")
	}
	d.seekLifted()
	nothingSent(t, to)

	// Along the bar first: a seek, even when it then wanders down.
	d.seekLanded(image.Pt(300, 203))
	d.seekGesture(touch.Gesture{Kind: touch.Hold, X: 300, Y: 203, Moved: true})
	if !d.seekGesture(touch.Gesture{Kind: touch.Drag, X: 320, Y: 206}) || !d.seekGesture(touch.Gesture{Kind: touch.Drag, X: 340, Y: 300}) {
		t.Fatal("a slide along the bar was let go")
	}
	if !d.seekGesture(touch.Gesture{Kind: touch.Release, X: 400, Y: 300}) {
		t.Fatal("the lift was not the bar's")
	}
	sent(t, to, 300*time.Second)
	d.seekLifted()
}

// A finger that comes down on the bar where a pull starts (the drawer's edge, the dashboard's) is the
// pull's: it is not followed, so it swipes the drawer in. A tap there still seeks.
func TestAPullFromTheEdgeIsNotTheBars(t *testing.T) {
	d, to := seekDisplay(t)
	edge := d.r.drawerEdge()
	d.r.seekBars.drew(seekZone{
		view:  seekView{route: seekDLNA, dur: 400 * time.Second},
		bar:   image.Rect(20, 200, d.r.w-20, 206),
		reach: image.Rect(12, 187, d.r.w-12, 219),
	})
	d.r.seekBars.publish()
	for _, x := range []int{edge - 10, d.r.w - edge + 10} {
		d.seekLanded(image.Pt(x, 203))
		d.mu.Lock()
		landed, follow := d.seekF.landed, d.seekF.follow
		d.mu.Unlock()
		if landed || follow {
			t.Errorf("a finger at x %d (the edge is %d wide) was followed", x, edge)
		}
		if d.seekGesture(touch.Gesture{Kind: touch.Hold, X: x, Y: 203, Moved: true}) {
			t.Errorf("a pull from x %d was taken for the bar's", x)
		}
		d.seekLifted()
	}
	if !d.seekGesture(touch.Gesture{Kind: touch.Tap, X: edge - 10, Y: 203}) {
		t.Error("a tap on the bar at the edge was not its")
	}
	<-to
	// Off the edges it is the bar's, as before.
	d.seekLanded(image.Pt(d.r.w/2, 203))
	d.mu.Lock()
	landed := d.seekF.landed
	d.mu.Unlock()
	if !landed {
		t.Error("a finger in the middle of the bar was not followed")
	}
	d.seekLifted()
	// The video page has no drawer: its bar is the bar's to its ends.
	d.mu.Lock()
	d.videoOnScreen = true
	d.mu.Unlock()
	d.seekLanded(image.Pt(edge-10, 203))
	d.mu.Lock()
	landed = d.seekF.landed
	d.mu.Unlock()
	if !landed {
		t.Error("on the video page, a finger at the bar's end was not followed")
	}
	d.seekLifted()
}

// The video's bar takes a finger only while the controls it is drawn with are up: with them away, a
// tap where it was brings them back, and does not seek as well.
func TestAVideosBarIsOnlyThereWithItsControls(t *testing.T) {
	d, _ := seekDisplay(t)
	playing := video.State{ID: 3, Phase: video.Playing, Frames: true}
	if d.videoBarUp(playing) {
		t.Error("the bar took a finger with the controls away")
	}
	if !d.videoBarUp(video.State{ID: 3, Phase: video.Paused, Frames: true}) {
		t.Error("the bar did not take a finger while paused, with its controls up")
	}
	d.mu.Lock()
	d.videoUntil = time.Now().Add(time.Minute)
	d.mu.Unlock()
	if !d.videoBarUp(playing) {
		t.Error("the bar did not take a finger with the controls up")
	}
	// A finger already on it keeps it, should the controls time out under it.
	d.mu.Lock()
	d.videoUntil = time.Time{}
	d.seekF.moving = true
	d.mu.Unlock()
	if !d.videoBarUp(playing) {
		t.Error("a finger sliding the bar lost it as the controls went")
	}
}

// A frame that keeps the page as it was (Rainbow's cue over Now Playing) keeps its bar on the panel
// too: drawn as one to seek on, it takes a finger.
func TestAKeptPageKeepsItsBar(t *testing.T) {
	var b seekBars
	z := seekZone{view: seekView{route: seekMusic, dur: time.Minute}, bar: image.Rect(10, 10, 100, 16), reach: image.Rect(2, 0, 108, 26)}
	b.begin()
	b.drew(z)
	b.publish()
	b.begin()
	b.keep()
	b.publish()
	if b.zone() != z {
		t.Errorf("after a kept frame: %+v", b.zone())
	}
}

// A song Music Assistant sends over DLNA that the renderer cannot move through is moved through its
// queue, as the back and forward buttons beside the bar do.
func TestADLNASongFromMusicAssistantSeeksOnItsQueue(t *testing.T) {
	seekFakes(t)
	oldRecv := receivingNow
	t.Cleanup(func() { receivingNow = oldRecv })
	receivingNow = func() string { return home.DLNAName }
	pos := media.Position{Title: "Some Jazz", Pos: time.Minute, Dur: 4 * time.Minute, Rate: 1}
	useSpoken(t, func(command string, _ map[string]any) (int, string) {
		if command == "player_queues/get" {
			return 200, spokenQueue("track", "Some Jazz", "playing")
		}
		return 200, "null"
	})
	d := &Display{poke: make(chan struct{}, 1)}
	rd := home.Radio{Now: home.DLNAName, Title: "Some Jazz"}
	d.spokenWord(rd, false, time.Now())
	waitUntil(t, "Music Assistant's answer", func() bool { return seekable(rd, false) })
	if v := musicSeekRoute(rd, false, pos); v.route != seekMusic || v.dur != 4*time.Minute {
		t.Errorf("a DLNA song on Music Assistant's queue the renderer cannot move: %+v", v)
	}
	// One the renderer can move is still the renderer's.
	dlnaSongWay = func() (dlna.SeekWay, time.Duration) { return dlna.SeekHere, 4 * time.Minute }
	if v := musicSeekRoute(rd, false, pos); v.route != seekDLNA {
		t.Errorf("a DLNA song the renderer can move: %v", v.route)
	}
}
