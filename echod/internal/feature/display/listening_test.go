//go:build !dot && !spot

package display

import (
	"image"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
	"github.com/HuskerMinion/techo5/echod/internal/lib/musicassistant"
)

// useSpoken points the Now Playing page's questions at a Music Assistant of the test's own, on a queue
// q1 of player media_player.ma_echo, and starts from no answer.
func useSpoken(t *testing.T, answer func(command string, args map[string]any) (int, string)) func() map[string][]map[string]any {
	noLibrary(t)
	c, asked := listenFakeMA(t, answer)
	libClient = func() (musicassistant.Client, bool) { return c, true }
	findWas := spokenFind
	spokenFind = func() (string, string) { return "media_player.ma_echo", "q1" }
	spoken.mu.Lock()
	spoken.s = spokenState{}
	spoken.mu.Unlock()
	t.Cleanup(func() {
		for { // an answer still on its way is let land before the next test starts afresh
			spoken.mu.Lock()
			asking := spoken.s.asking
			if !asking {
				// Cleared only once nothing is asking: clearing asking itself let the wait end with a
				// question still reading spokenFind as it was put back.
				spoken.s = spokenState{}
			}
			spoken.mu.Unlock()
			if !asking {
				break
			}
			time.Sleep(time.Millisecond)
		}
		spokenFind = findWas
	})
	return asked
}

// spokenQueue is the queue's answer: an item of media type kind named name, in state.
func spokenQueue(kind, name, state string) string {
	return `{"queue_id":"q1","active":true,"state":"` + state + `","playback_speed":1.5,"current_item":{"queue_item_id":"x","name":"Author - ` + name +
		`","media_item":{"media_type":"` + kind + `","uri":"library://` + kind + `/1","name":"` + name + `"}}}`
}

// What plays is asked of Music Assistant without waiting: an audiobook playing on the device's queue is
// spoken word, at the queue's speed, once the answer has come; a track is not.
func TestNowPlayingKnowsAnAudiobook(t *testing.T) {
	var mu sync.Mutex
	kind, name := "audiobook", "Northern Lights"
	useSpoken(t, func(command string, _ map[string]any) (int, string) {
		mu.Lock()
		defer mu.Unlock()
		return 200, spokenQueue(kind, name, "playing")
	})
	d := &Display{poke: make(chan struct{}, 1)}
	rd := home.Radio{Now: "Music Assistant", Title: "Northern Lights"}
	if on, _ := d.spokenWord(rd, false, time.Now()); on {
		t.Fatal("spoken word before Music Assistant said so")
	}
	waitUntil(t, "the answer", func() bool { on, _ := d.spokenWord(rd, false, time.Now()); return on })
	if _, speed := d.spokenWord(rd, false, time.Now()); speed != 1.5 {
		t.Errorf("speed = %v, want 1.5", speed)
	}
	mu.Lock()
	kind, name = "track", "A Song"
	mu.Unlock()
	song := home.Radio{Now: "Music Assistant", Title: "A Song"}
	d.spokenWord(song, false, time.Now())
	time.Sleep(50 * time.Millisecond)
	if on, _ := d.spokenWord(song, false, time.Now()); on {
		t.Error("a track was taken for spoken word")
	}
	if on, _ := d.spokenWord(home.Radio{Title: "Northern Lights", Followed: true}, false, time.Now()); on {
		t.Error("a player followed from another room was taken for this device's book")
	}
}

// A book left paused on Music Assistant's queue is not what plays when the page shows something else
// playing, a station of the device's own: neither by its state nor by its name.
func TestABookPausedOnTheQueueIsNotTheStation(t *testing.T) {
	var mu sync.Mutex
	state := "paused"
	useSpoken(t, func(string, map[string]any) (int, string) {
		mu.Lock()
		defer mu.Unlock()
		return 200, spokenQueue("audiobook", "Northern Lights", state)
	})
	d := &Display{poke: make(chan struct{}, 1)}
	book := home.Radio{Now: "Music Assistant", Title: "Northern Lights"}
	waitUntil(t, "the paused book", func() bool { on, _ := d.spokenWord(book, true, time.Now()); return on })
	if on, _ := d.spokenWord(book, false, time.Now()); on {
		t.Error("the queue paused was taken for the page playing")
	}
	mu.Lock()
	state = "playing"
	mu.Unlock()
	station := home.Radio{Now: "KXYZ 101.1", Title: "Morning Show"}
	d.spokenWord(station, false, time.Now())
	time.Sleep(50 * time.Millisecond)
	if on, _ := d.spokenWord(station, false, time.Now()); on {
		t.Error("a station was given the book's back and forward")
	}
}

// When the title changes (a book's next chapter), the last answer stands until the new one comes:
// back and forward stay 30 seconds rather than going to another book meanwhile.
func TestTheLastAnswerStandsUntilTheNext(t *testing.T) {
	gate := make(chan struct{})
	var once sync.Once
	first := true
	var mu sync.Mutex
	useSpoken(t, func(string, map[string]any) (int, string) {
		mu.Lock()
		wait := !first
		first = false
		mu.Unlock()
		if wait {
			<-gate
		}
		return 200, spokenQueue("audiobook", "Northern Lights", "playing")
	})
	t.Cleanup(func() { once.Do(func() { close(gate) }) })
	d := &Display{poke: make(chan struct{}, 1)}
	waitUntil(t, "the book", func() bool {
		on, _ := d.spokenWord(home.Radio{Title: "Northern Lights"}, false, time.Now())
		return on
	})
	chapter := home.Radio{Title: "Chapter 2"}
	if on, _ := d.spokenWord(chapter, false, time.Now()); !on {
		t.Error("a new title turned the book's buttons off before Music Assistant answered")
	}
	once.Do(func() { close(gate) })
}

// Back and forward skip 30 seconds through Music Assistant's queue; a token that may not falls back to
// Home Assistant's seek from where the player is, and any other refusal does not. The speed steps on
// through the presets.
func TestSpokenWordSkipsAndSpeeds(t *testing.T) {
	var mu sync.Mutex
	refuse := 0
	asked := useSpoken(t, func(command string, _ map[string]any) (int, string) {
		mu.Lock()
		defer mu.Unlock()
		if command == "player_queues/get" {
			return 200, spokenQueue("podcast_episode", "Episode 12", "playing")
		}
		if refuse != 0 {
			return refuse, ""
		}
		return 200, "null"
	})
	taps := recordTaps(t)
	d := &Display{poke: make(chan struct{}, 1)}
	rd := home.Radio{Title: "Episode 12"}
	waitUntil(t, "the answer", func() bool { on, _ := d.spokenWord(rd, false, time.Now()); return on })

	spokenSkipBy(-spokenSkip)
	spokenSkipBy(spokenSkip)
	d.spokenSpeedUp()
	waitUntil(t, "two skips and a speed", func() bool {
		a := asked()
		return len(a["player_queues/skip"]) == 2 && len(a["player_queues/set_playback_speed"]) == 1
	})
	got := map[float64]bool{}
	for _, a := range asked()["player_queues/skip"] {
		got[a["seconds"].(float64)] = a["queue_id"] == "q1"
	}
	if !got[-30] || !got[30] {
		t.Errorf("skips asked %v; want -30 and 30 on q1", asked()["player_queues/skip"])
	}
	if s := asked()["player_queues/set_playback_speed"][0]["speed"]; s != 2.0 {
		t.Errorf("speed asked %v; want the one after 1.5", s)
	}

	stateWas := libPlayerState
	t.Cleanup(func() { libPlayerState = stateWas })
	libPlayerState = func(string) (hass.State, error) {
		return hass.State{State: "paused", Attributes: map[string]any{"media_position": 100.0}}, nil
	}
	// A skip the server could not make (past the end, a queue gone quiet) is not made another way.
	mu.Lock()
	refuse = 500
	mu.Unlock()
	spokenSkipBy(spokenSkip)
	waitUntil(t, "the refused skip", func() bool { return len(asked()["player_queues/skip"]) == 3 })
	time.Sleep(30 * time.Millisecond)
	if len(taps()) != 0 {
		t.Errorf("a failed skip went to Home Assistant: %+v", taps())
	}
	mu.Lock()
	refuse = 403
	mu.Unlock()
	spokenSkipBy(-spokenSkip)
	waitUntil(t, "Home Assistant's seek", func() bool { return len(taps()) == 1 })
	if a := taps()[0]; a.Entity != "media_player.ma_echo" || a.Service != "media_player.media_seek" || a.Data["seek_position"] != 70.0 {
		t.Errorf("asked %+v; want a seek to 70 on the device's player", a)
	}
}

func TestSpokenNextSpeedAndWord(t *testing.T) {
	for now, want := range map[float64]float64{1: 1.25, 1.25: 1.5, 2: 0.75, 0.5: 0.75, 1.1: 1.25} {
		if got := spokenNextSpeed(now); got != want {
			t.Errorf("after %v: %v, want %v", now, got, want)
		}
	}
	if speedWord(1) != "1×" || speedWord(1.25) != "1.25×" {
		t.Errorf("words %q %q", speedWord(1), speedWord(1.25))
	}
}

// The speed button stays clear of the three buttons and the Library button on both pages and both
// screens, in a language with a long word for the library.
func TestTheSpeedButtonFits(t *testing.T) {
	for _, size := range []image.Point{{showWide, showHigh}, {show8Wide, show8High}} {
		r := newRenderer(image.NewRGBA(image.Rect(0, 0, size.X, size.Y)))
		for _, modern := range []bool{false, true} {
			for _, lib := range []string{"", "Library", "Bibliothèque"} {
				b := r.speedButton("1.25×", lib, modern)
				back, play, next := r.musicButtons(modern)
				others := []image.Rectangle{back, play, next, r.doneButton(), r.favButton()}
				if lib != "" {
					others = append(others, r.libraryButton(lib, modern))
				}
				for _, o := range others {
					if b.Overlaps(o) {
						t.Errorf("%v modern %v %q: speed %v overlaps %v", size, modern, lib, b, o)
					}
				}
				if !b.In(image.Rect(0, 0, size.X, size.Y)) {
					t.Errorf("%v modern %v %q: speed %v off the screen", size, modern, lib, b)
				}
			}
		}
	}
}
