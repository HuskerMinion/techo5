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
		spokenFind = findWas
		for { // an answer still on its way is let land before the next test starts afresh
			spoken.mu.Lock()
			asking := spoken.s.asking
			spoken.s = spokenState{}
			spoken.mu.Unlock()
			if !asking {
				return
			}
			time.Sleep(time.Millisecond)
		}
	})
	return asked
}

// What plays is asked of Music Assistant without waiting: an audiobook playing on the device's queue is
// spoken word, at the queue's speed, once the answer has come; a track is not.
func TestNowPlayingKnowsAnAudiobook(t *testing.T) {
	var mu sync.Mutex
	kind := "audiobook"
	useSpoken(t, func(command string, _ map[string]any) (int, string) {
		mu.Lock()
		defer mu.Unlock()
		return 200, `{"queue_id":"q1","active":true,"state":"playing","playback_speed":1.5,
			"current_item":{"queue_item_id":"x","media_item":{"media_type":"` + kind + `","uri":"library://` + kind + `/1","name":"N"}}}`
	})
	d := &Display{poke: make(chan struct{}, 1)}
	rd := home.Radio{Now: "Music Assistant", Title: "Northern Lights"}
	if on, _ := d.spokenWord(rd, time.Now()); on {
		t.Fatal("spoken word before Music Assistant said so")
	}
	waitUntil(t, "the answer", func() bool { on, _ := d.spokenWord(rd, time.Now()); return on })
	if _, speed := d.spokenWord(rd, time.Now()); speed != 1.5 {
		t.Errorf("speed = %v, want 1.5", speed)
	}
	mu.Lock()
	kind = "track"
	mu.Unlock()
	song := home.Radio{Now: "Music Assistant", Title: "A Song"}
	d.spokenWord(song, time.Now())
	time.Sleep(50 * time.Millisecond)
	if on, _ := d.spokenWord(song, time.Now()); on {
		t.Error("a track was taken for spoken word")
	}
	if on, _ := d.spokenWord(home.Radio{Title: "Northern Lights", Followed: true}, time.Now()); on {
		t.Error("a player followed from another room was taken for this device's book")
	}
}

// Back and forward skip 30 seconds through Music Assistant's queue; a token that may not falls back to
// Home Assistant's seek from where the player is. The speed steps on through the presets.
func TestSpokenWordSkipsAndSpeeds(t *testing.T) {
	var mu sync.Mutex
	refuse := false
	asked := useSpoken(t, func(command string, _ map[string]any) (int, string) {
		mu.Lock()
		defer mu.Unlock()
		if refuse && command != "player_queues/get" {
			return 403, ""
		}
		if command == "player_queues/get" {
			return 200, `{"active":true,"state":"playing","playback_speed":1.25,"current_item":{"media_item":{"media_type":"podcast_episode","uri":"rss://e/1"}}}`
		}
		return 200, "null"
	})
	taps := recordTaps(t)
	d := &Display{poke: make(chan struct{}, 1)}
	rd := home.Radio{Title: "Episode 12"}
	d.spokenWord(rd, time.Now())
	waitUntil(t, "the answer", func() bool { on, _ := d.spokenWord(rd, time.Now()); return on })

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
	if s := asked()["player_queues/set_playback_speed"][0]["speed"]; s != 1.5 {
		t.Errorf("speed asked %v; want the one after 1.25", s)
	}
	waitUntil(t, "the new speed", func() bool { _, s := d.spokenWord(rd, time.Now()); return s == 1.5 })

	mu.Lock()
	refuse = true
	mu.Unlock()
	stateWas := libPlayerState
	t.Cleanup(func() { libPlayerState = stateWas })
	libPlayerState = func(string) (hass.State, error) {
		return hass.State{State: "paused", Attributes: map[string]any{"media_position": 100.0}}, nil
	}
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
