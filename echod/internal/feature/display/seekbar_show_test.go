//go:build !dot && !spot

package display

import (
	"github.com/HuskerMinion/techo5/echod/internal/feature/video"
	"image"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/dlna"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
)

// seekDisplay is a lit Show with a bar on its panel from x 100 to 500, 400 seconds long, that sends
// to the DLNA renderer (seekFakes), and what the renderer is asked.
func seekDisplay(t *testing.T) (*Display, chan time.Duration) {
	t.Helper()
	_, dlnaTo := seekFakes(t)
	d := &Display{r: newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh))), poke: make(chan struct{}, 1), on: true}
	d.r.seekBars.drew(seekZone{
		view:  seekView{route: seekDLNA, dur: 400 * time.Second},
		bar:   image.Rect(100, 200, 500, 206),
		reach: image.Rect(92, 187, 508, 219),
	})
	d.r.seekBars.publish()
	t.Cleanup(func() { touch.Get().SetFollow(false) })
	return d, dlnaTo
}

func sent(t *testing.T, ch chan time.Duration, want time.Duration) {
	t.Helper()
	select {
	case got := <-ch:
		if got != want {
			t.Errorf("sent %v, want %v", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("nothing was sent (want %v)", want)
	}
}

func nothingSent(t *testing.T, ch chan time.Duration) {
	t.Helper()
	select {
	case got := <-ch:
		t.Errorf("sent %v", got)
	case <-time.After(50 * time.Millisecond):
	}
}

// A tap on the bar moves to the place under it at once; a tap beside it is not the bar's.
func TestATapOnTheBarSeeks(t *testing.T) {
	d, to := seekDisplay(t)
	if !d.seekGesture(touch.Gesture{Kind: touch.Tap, X: 300, Y: 210}) {
		t.Fatal("a tap on the bar was not its")
	}
	sent(t, to, 200*time.Second)
	if d.seekGesture(touch.Gesture{Kind: touch.Tap, X: 300, Y: 300}) {
		t.Error("a tap under the bar was taken for it")
	}
	nothingSent(t, to)
	d.mu.Lock()
	v := d.seekShowLocked(seekView{route: seekDLNA, dur: 400 * time.Second}, 10*time.Second, time.Now())
	d.mu.Unlock()
	if v.at(10*time.Second) != 200*time.Second {
		t.Errorf("the bar shows %v after the tap, want the place sent", v.at(10*time.Second))
	}
}

// A finger that comes down on the bar and slides along it shows the place it is over, and sends only
// where it lifts, past the end being the end. One that came down elsewhere is not the bar's even when
// it slides over it, and on a night light nothing is.
func TestAFingerSlidingAlongTheBarSeeksWhereItLifts(t *testing.T) {
	d, to := seekDisplay(t)
	view := seekView{route: seekDLNA, dur: 400 * time.Second}
	shown := func() time.Duration {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.seekShowLocked(view, 0, time.Now()).at(0)
	}
	d.seekLanded(image.Pt(150, 203))
	if !d.seekGesture(touch.Gesture{Kind: touch.Hold, X: 150, Y: 203, Moved: true}) {
		t.Fatal("the finger on the bar was not its")
	}
	if got := shown(); got != 50*time.Second {
		t.Errorf("as it starts: %v", got)
	}
	if !d.seekGesture(touch.Gesture{Kind: touch.Drag, X: 400, Y: 260}) {
		t.Fatal("the slide was not the bar's")
	}
	if got := shown(); got != 300*time.Second {
		t.Errorf("slid to x 400 (and off the bar's height): %v", got)
	}
	nothingSent(t, to)
	if !d.seekGesture(touch.Gesture{Kind: touch.Release, X: 640, Y: 260}) {
		t.Fatal("the lift was not the bar's")
	}
	sent(t, to, 399*time.Second) // the end is a second short of the length
	d.seekLifted()
	if d.seekGesture(touch.Gesture{Kind: touch.Drag, X: 300, Y: 203}) || d.seekGesture(touch.Gesture{Kind: touch.Release, X: 300, Y: 203}) {
		t.Error("a finger after the lift was taken for the bar's")
	}

	d.seekLanded(image.Pt(300, 400))
	if d.seekGesture(touch.Gesture{Kind: touch.Hold, X: 300, Y: 400, Moved: true}) ||
		d.seekGesture(touch.Gesture{Kind: touch.Drag, X: 300, Y: 203}) || d.seekGesture(touch.Gesture{Kind: touch.Release, X: 300, Y: 203}) {
		t.Error("a finger that came down off the bar moved it")
	}
	d.seekLifted()

	d.mu.Lock()
	d.nightGlow = true
	d.mu.Unlock()
	d.seekLanded(image.Pt(150, 203))
	if d.seekGesture(touch.Gesture{Kind: touch.Hold, X: 150, Y: 203, Moved: true}) {
		t.Error("a finger on the night light was taken for the bar's")
	}
	d.seekLifted()
	nothingSent(t, to)
}

// Now Playing's bar seeks only where somebody can: the DLNA renderer's song when the renderer or its
// controller can move it, Music Assistant's track on this device's queue, and nothing else (Spotify,
// AirPlay, a station, another room's player), whose bar only shows.
func TestHowNowPlayingsBarSeeks(t *testing.T) {
	seekFakes(t)
	oldRecv := receivingNow
	t.Cleanup(func() { receivingNow = oldRecv })
	receiving := ""
	receivingNow = func() string { return receiving }
	pos := media.Position{Title: "Some Jazz", Pos: time.Minute, Dur: 4 * time.Minute, Rate: 1}

	receiving = home.DLNAName
	dlnaRd := home.Radio{Now: home.DLNAName, Title: "Some Jazz"}
	if v := musicSeekRoute(dlnaRd, false, pos); v.route != seekNone {
		t.Errorf("a DLNA song nobody can move: %v", v.route)
	}
	dlnaSongWay = func() (dlna.SeekWay, time.Duration) { return dlna.SeekAsk, 4 * time.Minute }
	if v := musicSeekRoute(dlnaRd, false, pos); v.route != seekDLNA || v.dur != 4*time.Minute {
		t.Errorf("a DLNA song its controller can move: %+v", v)
	}
	if v := musicSeekRoute(dlnaRd, false, media.Position{}); v.route != seekNone {
		t.Errorf("a song with no length: %v", v.route)
	}

	receiving = "Spotify"
	if v := musicSeekRoute(home.Radio{Now: "Spotify", Title: "Some Jazz"}, false, pos); v.route != seekNone {
		t.Errorf("Spotify: %v", v.route)
	}

	receiving = ""
	asked := useSpoken(t, func(command string, _ map[string]any) (int, string) {
		if command == "player_queues/get" {
			return 200, spokenQueue("track", "Some Jazz", "playing")
		}
		return 200, "null"
	})
	d := &Display{poke: make(chan struct{}, 1)}
	ma := home.Radio{Now: "Music Assistant", Title: "Some Jazz"}
	d.spokenWord(ma, false, time.Now())
	waitUntil(t, "Music Assistant's answer", func() bool { return seekable(ma, false) })
	if v := musicSeekRoute(ma, false, pos); v.route != seekMusic || v.dur != 4*time.Minute {
		t.Errorf("Music Assistant's track: %+v", v)
	}
	if v := musicSeekRoute(home.Radio{Title: "Some Jazz", Followed: true}, false, pos); v.route != seekNone {
		t.Errorf("another room's player: %v", v.route)
	}
	seekRun(seekView{route: seekMusic, dur: 4 * time.Minute}, 95*time.Second+400*time.Millisecond)
	waitUntil(t, "the seek", func() bool { return len(asked()["player_queues/seek"]) == 1 })
	if a := asked()["player_queues/seek"][0]; a["queue_id"] != "q1" || a["position"] != 95.0 {
		t.Errorf("seek asked %v; want 95 on q1", a)
	}
}

// Now Playing's bar, in either style and on each panel: a place to seek only when what plays can be
// moved from here, clear of every button a finger lands on, and none under what slides in over the page.
func TestNowPlayingsBarIsOnlyTappableWhereItSeeks(t *testing.T) {
	at := time.Date(2026, 9, 16, 14, 7, 0, 0, time.Local)
	pos := media.Position{At: at, Pos: 86 * time.Second, Dur: 222 * time.Second, Rate: 1}
	seeks := seekView{route: seekMusic, dur: 222 * time.Second}
	for _, panel := range []struct {
		name       string
		wide, high int
	}{{"show", showWide, showHigh}, {"show8", show8Wide, show8High}, {"skylight", skylightWide, skylightHigh}} {
		for _, modern := range []bool{false, true} {
			for _, c := range []struct {
				name   string
				seek   seekView
				drawer bool
				want   bool
			}{{"shows only", seekView{}, false, false}, {"seeks", seeks, false, true}, {"under the drawer", seeks, true, false}} {
				s := scene{now: at, phase: "idle", nowPlaying: true, modernMusic: modern, playing: true, libraryWord: "Library", queue: true, seek: true,
					radio:         home.Radio{Playing: true, Now: "Music Assistant", Title: "Some Jazz", Artist: "The Quartet", Music: true},
					musicPosition: pos, musicSeek: c.seek, showDrawer: c.drawer}
				r := newRenderer(image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high)))
				r.draw(s)
				z := r.seekBars.zone()
				what := panel.name + " " + c.name
				if modern {
					what += " (modern)"
				}
				if got := z.view.route != seekNone; got != c.want {
					t.Errorf("%s: a bar to seek on %v, want %v", what, got, c.want)
				}
				if !c.want {
					continue
				}
				back, play, next := r.musicButtons(modern)
				sb, sf := r.seekButtons(modern)
				for _, b := range []image.Rectangle{back, play, next, sb, sf, r.doneButton(), r.favButton(), r.libraryButton("Library", modern)} {
					if z.reach.Overlaps(r.buttonReach(b)) {
						t.Errorf("%s: the bar's reach %v takes a button's %v", what, z.reach, b)
					}
				}
				if !z.reach.In(r.dst.Rect) || z.bar.Dx() < r.s(200) {
					t.Errorf("%s: the bar %v (reach %v) on a %dx%d panel", what, z.bar, z.reach, panel.wide, panel.high)
				}
			}
		}
	}
}

// The time line under a film streamed from a place gives the whole film's length, not the stream's.
func TestAFilmStreamedFromAPlaceHasTheFilmsLength(t *testing.T) {
	st := video.State{ID: 5, Origin: video.FromDLNA, Phase: video.Playing, Pos: 9 * time.Second, Dur: 97 * time.Minute, Frames: true}
	v := seekView{route: seekDLNA, id: 5, dur: 150 * time.Minute, start: 53 * time.Minute}
	if got := videoLength(scene{video: st, videoSeek: v}); got != 150*time.Minute {
		t.Errorf("the time line's length %v, want 2h30m", got)
	}
}
