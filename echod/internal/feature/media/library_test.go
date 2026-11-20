package media

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// aacStation is a station in a format the device does not decode.
func aacStation(t *testing.T) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/aac")
		w.Write([]byte{0xFF, 0xF1, 0x50, 0x80, 0x00, 0x1F, 0xFC, 0, 0, 0, 0, 0})
		w.(http.Flusher).Flush() // a station sends as it goes
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// A stream the device cannot decode is its own failure, told apart from a station that is down.
func TestAnUndecodableStreamIsToldApart(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	d := speaker.NewDriver(speaker.New())
	s := NewStream(d, speaker.New(), func() {}, func(string) {})
	err := s.PlayChecked(aacStation(t), 5*time.Second)
	if !IsUnplayable(err) {
		t.Errorf("an AAC station failed as %v", err)
	}
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer down.Close()
	if err := s.PlayChecked(down.URL, 5*time.Second); err == nil || IsUnplayable(err) {
		t.Errorf("a station that is down failed as %v", err)
	}
}

// One started without waiting (the radio page) goes to the music library when the device cannot
// decode it; with no library it is left failed.
func TestAnUndecodableStationIsHandedOn(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	d := speaker.NewDriver(speaker.New())
	s := NewStream(d, speaker.New(), func() {}, func(string) {})
	var mu sync.Mutex
	var handed []string
	s.handOff = func(url string, wanted func() bool) {
		mu.Lock()
		handed = append(handed, url)
		mu.Unlock()
	}
	url := aacStation(t)
	s.Play(url)
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		mu.Lock()
		n := len(handed)
		mu.Unlock()
		if n > 0 {
			break
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(handed) != 1 || handed[0] != url {
		t.Errorf("handed on %v", handed)
	}
}

// The library is asked to play the stream on this device, and the answer is whether it did.
func TestTheLibraryPlaysWhatTheDeviceCannot(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := viaLibrary("https://203.0.113.9/a.aac", nil); err != errNoLibrary {
		t.Errorf("with no library: %v", err)
	}
	var mu sync.Mutex
	var played []string
	lib := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Command string         `json:"command"`
			Args    map[string]any `json:"args"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		defer mu.Unlock()
		switch body.Command {
		case "players/all":
			state := "idle"
			if len(played) > 0 {
				state = "playing"
			}
			json.NewEncoder(w).Encode([]map[string]any{{"player_id": "upc1", "available": true, "playback_state": state,
				"output_protocols": []map[string]any{{"output_protocol_id": "aa:bb:cc:dd:ee:ff"}}}})
		case "player_queues/play_media":
			if body.Args["queue_id"] != "upc1" {
				t.Errorf("played on %v, not the device's player", body.Args["queue_id"])
			}
			played = append(played, body.Args["media"].(string))
			w.Write([]byte("null"))
		}
	}))
	defer lib.Close()
	tok := "tok"
	if err := config.Set().MusicAssistant().Server(lib.URL, &tok); err != nil {
		t.Fatal(err)
	}
	was := libraryPlayer
	libraryPlayer = func() (string, error) { return "aa:bb:cc:dd:ee:ff", nil }
	defer func() { libraryPlayer = was }()
	// An address into a home, or one handed on after something else was asked for, is not given.
	if err := viaLibrary("http://192.168.1.20:8000/a.aac", nil); err == nil {
		t.Error("an address in the home was handed to the library")
	}
	if err := viaLibrary("https://203.0.113.9/a.aac", func() bool { return false }); err != errOvertaken {
		t.Errorf("an overtaken hand-off: %v", err)
	}
	if err := viaLibrary("https://203.0.113.9/a.aac", nil); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(played) != 1 || played[0] != "https://203.0.113.9/a.aac" {
		t.Errorf("the library was given %v", played)
	}
}

// A checked play is answered by its caller, which hands on itself: the stream does not hand it off a
// second time. And a hand-off on its way knows once it is overtaken: by a stop, or another play.
func TestAHandOffIsOnceAndKnowsWhenItIsOvertaken(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	d := speaker.NewDriver(speaker.New())
	s := NewStream(d, speaker.New(), func() {}, func(string) {})
	var mu sync.Mutex
	var wants []func() bool
	s.handOff = func(url string, wanted func() bool) {
		mu.Lock()
		wants = append(wants, wanted)
		mu.Unlock()
	}
	url := aacStation(t)
	if err := s.PlayChecked(url, 5*time.Second); !IsUnplayable(err) {
		t.Fatalf("checked: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	if len(wants) != 0 {
		t.Error("a checked play was handed off by the stream as well")
	}
	mu.Unlock()

	s.Play(url)
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		mu.Lock()
		n := len(wants)
		mu.Unlock()
		if n > 0 {
			break
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(wants) != 1 {
		t.Fatalf("handed off %d times", len(wants))
	}
	if !wants[0]() {
		t.Error("a hand-off nothing has overtaken is not wanted")
	}
	s.Stop()
	if wants[0]() {
		t.Error("a hand-off is still wanted after a stop")
	}
}

// handedOff plays url on p's stream as the radio page does and waits for it to be handed on, giving
// back the hand-off's wanted.
func handedOff(t *testing.T, p *Player, url string) func() bool {
	t.Helper()
	var mu sync.Mutex
	var wanted func() bool
	p.stream.handOff = func(_ string, w func() bool) {
		mu.Lock()
		wanted = w
		mu.Unlock()
	}
	p.stream.Play(url)
	waitUntil(t, "the station was handed on", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return wanted != nil
	})
	mu.Lock()
	defer mu.Unlock()
	return wanted
}

// The music library starting the station here over Sendspin is the hand-off coming off, not something
// else asked for in its place: it must not call the hand-off off, or the library is told to stop the
// stream it has just started (#649). A Stop from the listener while it is on its way still does.
func TestTheLibraryStartingAHandedOnStationDoesNotOvertakeIt(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	d := speaker.NewDriver(speaker.New())
	p := &Player{stream: NewStream(d, speaker.New(), func() {}, func(string) {}), mp: &esphome.MediaPlayer{}}
	wanted := handedOff(t, p, aacStation(t))
	waitUntil(t, "the station that could not be decoded was cleared", func() bool {
		playing, paused := p.stream.Playing()
		return !playing && !paused
	})

	p.External() // Sendspin: the library's stream has arrived
	if !wanted() {
		t.Error("the library's own stream arriving overtook the hand-off that asked for it")
	}
	p.stream.Stop()
	if wanted() {
		t.Error("a hand-off is still wanted after the listener stopped it")
	}
}

// A remote starting while a hand-off is on its way, with a station of this device's own playing
// since, is the room going elsewhere: the station is stopped and the hand-off is not wanted either.
func TestARemoteStartingOverAStationPlayedSinceOvertakesTheHandOff(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // a station: it never ends
	}))
	defer srv.Close()
	d := speaker.NewDriver(speaker.New())
	p := &Player{stream: NewStream(d, speaker.New(), func() {}, func(string) {}), mp: &esphome.MediaPlayer{}}
	wanted := handedOff(t, p, aacStation(t))
	p.stream.handOff = nil
	p.stream.Play(srv.URL)
	if wanted() {
		t.Error("a hand-off is still wanted after another station was played")
	}
	p.External()
	if playing, paused := p.stream.Playing(); playing || paused {
		t.Errorf("the station outlived the remote starting: playing %v paused %v", playing, paused)
	}
}

// A station the device cannot decode has not ended by itself: the radio must not put it back on, as
// it would a stream that dropped, or each try starts another hand-off over the one before (#649).
func TestAnUndecodableStationHasNotEndedByItself(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	var mu sync.Mutex
	var ended []string
	s := NewStream(speaker.NewDriver(speaker.New()), speaker.New(), func() {}, func(url string) {
		mu.Lock()
		ended = append(ended, url)
		mu.Unlock()
	})
	s.handOff = func(string, func() bool) {}
	url := aacStation(t)
	// Started as the radio page starts it, and as the voice assistant does.
	waitDone(t, s.play(url, true, nil))
	if err := s.PlayChecked(url, 5*time.Second); !IsUnplayable(err) {
		t.Fatalf("checked: %v", err)
	}
	time.Sleep(200 * time.Millisecond) // finished runs just after the track is done
	mu.Lock()
	defer mu.Unlock()
	if len(ended) != 0 {
		t.Errorf("an undecodable station was reported ended by itself: %v", ended)
	}
}

// A station the device cannot decode, with no music library to hand it to, is said to be unplayable,
// since the radio does not put it back on and would otherwise go quiet with nothing said. With a library
// set up the hand-off goes as before (#649), and nothing is said here: here the library refuses an
// address that is not a public one.
func TestAnUndecodableStationWithNoLibraryIsSaidToBeUnplayable(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	d := speaker.NewDriver(speaker.New())
	p := &Player{stream: NewStream(d, speaker.New(), func() {}, func(string) {}), mp: &esphome.MediaPlayer{}}
	p.stream.handOff = p.handOff
	said := make(chan string, 4)
	p.OnUnplayable.Listen(func(url string) { said <- url })

	url := aacStation(t)
	p.stream.Play(url)
	select {
	case got := <-said:
		if got != url {
			t.Errorf("said %q is unplayable, want %q", got, url)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a station that cannot be decoded, with no music library, was not said to be unplayable")
	}

	token := "a-token"
	if err := config.Set().MusicAssistant().Server("http://music.example:8095", &token); err != nil {
		t.Fatal(err)
	}
	p.stream.Play(aacStation(t))
	waitUntil(t, "the second station that could not be decoded was cleared", func() bool {
		playing, paused := p.stream.Playing()
		return !playing && !paused
	})
	select {
	case got := <-said:
		t.Errorf("with a music library set up, %q was said to be unplayable", got)
	case <-time.After(300 * time.Millisecond):
	}
}
