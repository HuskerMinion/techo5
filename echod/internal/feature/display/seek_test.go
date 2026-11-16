//go:build !dot && !spot

package display

import (
	"image"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// A song can be moved through when it is the one on this device's Music Assistant queue, doing what the
// page says, and not a radio stream; another room's player and a station are not.
func TestOnlyATrackOnTheQueueCanBeSkippedThrough(t *testing.T) {
	var mu sync.Mutex
	kind, name := "track", "A Song"
	useSpoken(t, func(string, map[string]any) (int, string) {
		mu.Lock()
		defer mu.Unlock()
		return 200, spokenQueue(kind, name, "playing")
	})
	d := &Display{poke: make(chan struct{}, 1)}
	song := home.Radio{Now: "Music Assistant", Title: "A Song"}
	if seekable(song, false) {
		t.Error("skip offered before Music Assistant answered")
	}
	d.spokenWord(song, false, time.Now())
	waitUntil(t, "the answer", func() bool { return seekable(song, false) })
	if seekable(song, true) {
		t.Error("skip offered for a page that says paused over a queue that plays")
	}
	if seekable(home.Radio{Now: "Music Assistant", Title: "A Song", Followed: true}, false) {
		t.Error("skip offered for another room's player")
	}
	if seekable(home.Radio{Now: "Music Assistant"}, false) {
		t.Error("skip offered with no title to match")
	}
	mu.Lock()
	kind, name = "radio", "KXYZ 101.1"
	mu.Unlock()
	station := home.Radio{Now: "KXYZ 101.1", Title: "KXYZ 101.1"}
	d.spokenWord(station, false, time.Now())
	time.Sleep(50 * time.Millisecond)
	if seekable(station, false) {
		t.Error("skip offered for a radio stream")
	}
}

// The skip buttons sit on the screen and on nothing else the page draws, on every panel and on both
// pages, with the Library button, and with the speed button too where the two could meet.
func TestTheSeekButtonsFindRoom(t *testing.T) {
	for _, panel := range []image.Point{{X: 960, Y: 480}, {X: 1280, Y: 800}, {X: 1920, Y: 1080}} {
		for _, modern := range []bool{false, true} {
			r := newRenderer(image.NewRGBA(image.Rect(0, 0, panel.X, panel.Y)))
			back, forward := r.seekButtons(modern)
			where := map[bool]string{false: "classic", true: "modern"}[modern]
			screen := image.Rect(0, 0, panel.X, panel.Y)
			if !back.In(screen) || !forward.In(screen) {
				t.Errorf("%s %v: buttons %v %v off the screen", where, panel, back, forward)
			}
			if back.Overlaps(forward) {
				t.Errorf("%s %v: the two overlap", where, panel)
			}
			tb, tp, tn := r.musicButtons(modern)
			others := map[string]image.Rectangle{"back": tb, "play": tp, "next": tn, "done": r.doneButton(), "star": r.favButton()}
			if modern {
				others["library"] = r.libraryButton("Library", true)
			} else {
				others["library"] = r.libraryButton("Library", false)
			}
			for name, o := range others {
				for _, b := range []image.Rectangle{back, forward} {
					if b.Overlaps(o) {
						t.Errorf("%s %v: a skip button %v overlaps %s %v", where, panel, b, name, o)
					}
				}
			}
		}
	}
}
