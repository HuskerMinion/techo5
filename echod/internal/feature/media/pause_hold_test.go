package media

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// other is a second background, the way Music Assistant's stream is one: it records nothing, it only
// has to be there to be stood down.
type other struct{}

func (other) Suspend()  {}
func (other) Resume()   {}
func (other) Duck(bool) {}
func (other) Requeue()  {}

// An order a Show went through on 2026-09-24, with music set to pause for a turn: Music
// Assistant is playing, a turn starts, and while its reply is sounding Home Assistant starts a station
// on this player. After the reply and the turn are over the station has to be free to play. It stayed
// held instead, and every later request to play was answered "Playing" to a silent speaker.
func TestAStationStartedDuringAPausedTurnPlaysAfterIt(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Media().OnTurn(config.OnTurnPause); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // a station: it never ends
	}))
	defer srv.Close()

	d := speaker.NewDriver(speaker.New())
	a := d.Backgrounds()
	s := NewStream(d, speaker.New(), func() {}, func(string) {})
	a.Took(other{}) // Music Assistant, playing

	a.Duck("turn", true) // the turn starts
	release := make(chan struct{})
	reply := d.ClaimSpeech("reply", func(ctx context.Context, _ *speaker.Player) error {
		<-release
		return nil
	})
	s.Play(srv.URL) // Home Assistant starts the station while the reply sounds
	close(release)
	select {
	case <-reply.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the reply never finished")
	}
	a.Duck("turn", false) // the turn ends

	s.mu.Lock()
	holds, gated := s.holds, s.gate != nil
	s.mu.Unlock()
	if holds != 0 || gated {
		t.Fatalf("the station is still held after the turn: holds=%d gated=%v", holds, gated)
	}
	s.Stop()
}

// "Stop playing" by voice, with music set to pause for a turn: the turn pauses the station and the
// reply stands it down, then Home Assistant stops it before either is over. A stopped player has left
// the speaker's backgrounds, so neither release reaches it; the holds it kept then held every later
// station, and "Playing" was answered to a silent speaker until the device restarted.
func TestAStationPlaysAfterBeingStoppedDuringAPausedTurn(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Media().OnTurn(config.OnTurnPause); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	d := speaker.NewDriver(speaker.New())
	a := d.Backgrounds()
	s := NewStream(d, speaker.New(), func() {}, func(string) {})
	s.Play(srv.URL) // the station is playing

	a.Duck("turn", true) // "stop playing" starts a turn
	release := make(chan struct{})
	reply := d.ClaimSpeech("reply", func(ctx context.Context, _ *speaker.Player) error {
		<-release
		return nil
	})
	s.Stop() // Home Assistant stops the player while the reply is sounding
	close(release)
	select {
	case <-reply.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the reply never finished")
	}
	a.Duck("turn", false)

	s.Play(srv.URL) // later: "play the station"
	s.mu.Lock()
	holds, gated := s.holds, s.gate != nil
	s.mu.Unlock()
	if holds != 0 || gated {
		t.Fatalf("a station started after the stop is held: holds=%d gated=%v", holds, gated)
	}
	s.Stop()
}
