package dlna

import (
	"html"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/video"
)

// A video the renderer can move through is moved by the renderer itself when a place is chosen on the
// screen, the way a controller's Seek goes (the burst settles first), and no controller is asked to.
// Another video than the renderer's has no way from here at all.
func TestAScreenSeekInAVideoIsTheRenderers(t *testing.T) {
	f := playingRenderer(t)
	stubs(t)
	oldSettle := seekSettle
	seekSettle = time.Hour
	var mu sync.Mutex
	var moved []time.Duration
	seekVideo = func(id uint64, to time.Duration) error {
		mu.Lock()
		defer mu.Unlock()
		moved = append(moved, to)
		return nil
	}
	videoSeekable = func(uint64) (bool, bool) { return true, true }
	currentVideo = func() video.State {
		return video.State{ID: 7, Phase: video.Playing, Pos: 10 * time.Second, Dur: 10 * time.Minute, Frames: true}
	}
	f.r.cur = song{uri: "http://192.0.2.10/film.mp4", video: true}
	f.r.videoID = 7
	t.Cleanup(func() {
		f.r.mu.Lock()
		f.r.clearSeekLocked()
		f.r.mu.Unlock()
		seekSettle = oldSettle
	})
	avt, got := subscriber(t)
	f.e.subs = map[string]*subscription{"uuid:avt": {service: "AVTransport", callback: avt, until: time.Now().Add(time.Hour)}}

	if way, dur := f.VideoSeekWay(7); way != SeekHere || dur != 10*time.Minute {
		t.Fatalf("the renderer's video: %v, %v", way, dur)
	}
	if way, _ := f.VideoSeekWay(8); way != SeekNone {
		t.Errorf("another video: %v", way)
	}
	if f.SeekFromDevice(0, 90*time.Second) {
		t.Error("a song's seek went to a video")
	}
	if f.SeekFromDevice(8, 90*time.Second) {
		t.Error("another video's seek went to the renderer's")
	}
	if !f.SeekFromDevice(7, 90*time.Second) {
		t.Fatal("the seek was not made")
	}
	f.r.mu.Lock()
	armed := f.r.seekTimer != nil && f.r.seekTimer.Stop()
	f.r.mu.Unlock()
	if !armed {
		t.Fatal("the seek did not settle as a controller's does")
	}
	f.r.seekNow()
	mu.Lock()
	if len(moved) != 1 || moved[0] != 90*time.Second {
		t.Errorf("moved %v, want 1m30s", moved)
	}
	mu.Unlock()
	// What follows is the state changing, never a request: the renderer did it.
	for {
		select {
		case n := <-got:
			if strings.Contains(html.UnescapeString(n.body), "X_TECHO5_Request") {
				t.Fatalf("a controller was asked: %s", html.UnescapeString(n.body))
			}
			continue
		case <-time.After(300 * time.Millisecond):
		}
		break
	}
}

// A song the renderer cannot move through goes to the controller listening, as TECHO5's own request:
// "seek", the place in whole seconds (no further than the song's end), and the request's number, which
// goes on from next's and previous's. Only a controller that said it acts on seeks (X-TECHO5-Requests)
// counts: with nobody listening, one that did not say so, or no length to place it in, there is no way
// to seek from the screen at all.
func TestAScreenSeekTheRendererCannotMakeIsAskedOfTheController(t *testing.T) {
	f := playingRenderer(t)
	if way, _ := f.SeekWay(); way != SeekNone {
		t.Errorf("a song with no length: %v", way)
	}
	f.r.cur.dur = 5 * time.Minute
	if way, _ := f.SeekWay(); way != SeekNone {
		t.Errorf("nobody listening: %v", way)
	}
	if f.SeekFromDevice(0, time.Minute) {
		t.Error("a seek with nobody to make it said it was made")
	}
	avt, got := subscriber(t)
	f.e.subs = map[string]*subscription{"uuid:avt": {service: "AVTransport", callback: avt, until: time.Now().Add(time.Hour)}}
	if way, _ := f.SeekWay(); way != SeekNone {
		t.Errorf("a controller that did not say it seeks: %v", way)
	}
	f.e.subs["uuid:avt"].asks = askNext | askPrevious
	if way, _ := f.SeekWay(); way != SeekNone {
		t.Errorf("a controller that said only next and previous: %v", way)
	}
	f.e.subs["uuid:avt"].asks = askSeek
	if way, dur := f.SeekWay(); way != SeekAsk || dur != 5*time.Minute {
		t.Fatalf("a controller listening: %v, %v", way, dur)
	}
	if way, _ := f.VideoSeekWay(7); way != SeekNone {
		t.Errorf("a song taken for a video: %v", way)
	}

	if !f.SeekFromDevice(0, 95*time.Second+600*time.Millisecond) {
		t.Fatal("nobody was asked")
	}
	what, place, n1 := seekRequestIn(t, nextEvent(t, got).body)
	if what != "seek" || place != "95" {
		t.Errorf("request %s:%s, want seek:95", what, place)
	}
	f.SeekFromDevice(0, time.Hour)
	_, place, n2 := seekRequestIn(t, nextEvent(t, got).body)
	if place != "299" || n2 == n1 {
		t.Errorf("past the end: seek:%s:%s after %s, want seek:299 (a second short of the end) under a new number", place, n2, n1)
	}
	f.r.mu.Lock()
	ask := f.r.seeking
	f.r.mu.Unlock()
	if ask {
		t.Error("the renderer set out to move a song it cannot move through")
	}
}

// seekRequestIn reads a seek request out of an event: "seek", the place and the request's number.
func seekRequestIn(t *testing.T, body string) (what, place, n string) {
	t.Helper()
	asked, n := requestIn(t, body)
	what, place, ok := strings.Cut(asked, ":")
	if !ok {
		t.Fatalf("request %s:%s has no place", asked, n)
	}
	return what, place, n
}

// A film's description can say where in the whole film the stream begins (TECHO5's start attribute
// on res), and a place chosen on the screen is in the whole film's time: the controller is asked for
// that place, not the place within the stream.
func TestAStreamThatBeginsFurtherIn(t *testing.T) {
	meta := `<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/" xmlns:techo5="urn:techo5:dlna">` +
		`<item><res protocolInfo="http-get:*:video/mp2t:*" duration="1:37:00.000" techo5:start="0:53:00">x</res></item></DIDL-Lite>`
	if s := parseSong("http://192.0.2.10/t", meta); s.start != 53*time.Minute || s.dur != 97*time.Minute {
		t.Errorf("start %v, length %v", s.start, s.dur)
	}
	other := `<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/" xmlns:x="urn:other">` +
		`<item><res duration="0:10:00" x:start="0:05:00">x</res></item></DIDL-Lite>`
	if s := parseSong("http://192.0.2.10/t", other); s.start != 0 {
		t.Errorf("another namespace's start was taken: %v", s.start)
	}
}

// A controller says which of TECHO5's own requests it acts on when it subscribes: a word it does not
// know, or one close to a name, is not one.
func TestASubscriptionSaysWhatItActsOn(t *testing.T) {
	for h, want := range map[string]requests{
		"":                     0,
		"next, previous":       askNext | askPrevious,
		"next, previous, seek": askNext | askPrevious | askSeek,
		"SEEK":                 askSeek,
		"seeks, nexts":         0,
		" Next ,,stop":         askNext,
	} {
		if got := namedRequests(h); got != want {
			t.Errorf("%q: %b, want %b", h, got, want)
		}
	}
}
