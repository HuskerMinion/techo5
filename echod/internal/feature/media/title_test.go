package media

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// titledPlayer is a player with a stream, and a url that plays until the test ends.
func titledPlayer(t *testing.T) (*Player, string) {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	d := speaker.NewDriver(speaker.New())
	p := &Player{stream: NewStream(d, speaker.New(), func() {}, func(string) {}), mp: &esphome.MediaPlayer{}}
	t.Cleanup(p.stream.Stop)
	return p, srv.URL
}

// playMedia is Home Assistant's play_media, not an announcement.
func playMedia(p *Player, url string) {
	p.command(esphome.MediaCommand{HasMediaURL: true, MediaURL: url})
}

var aMessage = Title{Title: "Message from Alex", Subtitle: "today 14:12", Source: "Answering machine"}

// The next play_media takes the title, for as long as its track is loaded, paused too; the one after it
// has none.
func TestTheNextPlayMediaTakesTheTitle(t *testing.T) {
	p, url := titledPlayer(t)
	p.TitleNext(aMessage)

	playMedia(p, url)
	if got := p.OwnTitle(); got == nil || *got != aMessage {
		t.Fatalf("playing: named %v, want %v", got, aMessage)
	}
	p.stream.Pause()
	if got := p.OwnTitle(); got == nil || *got != aMessage {
		t.Fatalf("paused: named %v, want %v", got, aMessage)
	}

	playMedia(p, url)
	if got := p.OwnTitle(); got != nil {
		t.Fatalf("the next play_media is named %v, want nothing", got)
	}
}

// A stopped track takes its title with it.
func TestTheTitleGoesWithItsTrack(t *testing.T) {
	p, url := titledPlayer(t)
	p.TitleNext(aMessage)
	playMedia(p, url)

	p.stream.Stop()
	if got := p.OwnTitle(); got != nil {
		t.Fatalf("after a stop: named %v, want nothing", got)
	}
}

// A station started first uses the title up: it was not for the station, and not for what comes after.
func TestAStationDropsATitleNotYetUsed(t *testing.T) {
	p, url := titledPlayer(t)
	p.TitleNext(aMessage)

	p.PlayURL(url)
	if got := p.OwnTitle(); got != nil {
		t.Fatalf("the station is named %v, want nothing", got)
	}
	playMedia(p, url)
	if got := p.OwnTitle(); got != nil {
		t.Fatalf("the play_media after the station is named %v, want nothing", got)
	}
}

// A title whose play_media did not come within titleFor names nothing: not that play_media, nor any
// after it.
func TestATitleExpires(t *testing.T) {
	p, url := titledPlayer(t)
	p.nextTitle.Store(&pendingTitle{Title: aMessage, at: time.Now().Add(-titleFor - time.Second)})

	playMedia(p, url)
	if got := p.OwnTitle(); got != nil {
		t.Fatalf("named %v, want nothing", got)
	}
	playMedia(p, url)
	if got := p.OwnTitle(); got != nil {
		t.Fatalf("the next play_media is named %v, want nothing", got)
	}
}

// media_title with no title names nothing, and drops one given before.
func TestAnEmptyTitleNamesNothing(t *testing.T) {
	p, url := titledPlayer(t)
	p.TitleNext(aMessage)

	run := p.Actions()[0].Run
	if _, err := run(esphome.Call{}); err != nil { // every argument empty
		t.Fatal(err)
	}
	playMedia(p, url)
	if got := p.OwnTitle(); got != nil {
		t.Fatalf("named %v, want nothing", got)
	}
}
