package media

import (
	"net/http"
	"net/http/httptest"
	"testing"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// Starting a track of this player's own while a remote still holds the speaker is the room going its own
// way. The remote's stream has to end rather than wait behind the new one, and in a grouped room that
// means leaving the group before anything is stopped - which is not this player's to work out, and is not
// something the protocol says either, so it says what happened and whoever knows how does it. Like
// OnResumeRemote, for the same kind of reason.
func TestStartingThisPlayersOwnTrackSaysTheRoomWasTakenOver(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // a station: it never ends
	}))
	defer srv.Close()

	d := speaker.NewDriver(speaker.New())
	p := &Player{stream: NewStream(d, speaker.New(), func() {}, func(string) {}), mp: &esphome.MediaPlayer{}}
	took := 0
	p.OnTakeOver.Listen(func(struct{}) { took++ })

	p.PlayURL(srv.URL)
	if took != 0 {
		t.Errorf("a room on its own reported a takeover %d times, want none", took)
	}
	p.stream.Stop()

	p.remote.take() // Music Assistant holds the speaker
	p.PlayURL(srv.URL)
	if took != 1 {
		t.Errorf("a remote holding the speaker took the news %d times, want once", took)
	}
	p.stream.Stop()
}
