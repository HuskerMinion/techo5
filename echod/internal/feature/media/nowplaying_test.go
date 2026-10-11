package media

import (
	"testing"

	esphome "github.com/ygelfand/go-esphome-device"
)

// What names the room's music - a receiver's song, a remote's, a held track let go, a remote going -
// is told to whoever listens, so Home Assistant's sensors follow it.
func TestAChangeInWhatPlaysIsToldToWhoeverListens(t *testing.T) {
	p := &Player{stream: &Stream{}, mp: &esphome.MediaPlayer{}}
	told := 0
	p.OnNowPlaying.Listen(func(struct{}) { told++ })

	for _, c := range []struct {
		what   string
		change func()
	}{
		{"a receiver's song", func() { p.SetReceivedTrack("AirPlay", "Africa", "Toto", "Toto IV") }},
		{"a remote's song", func() { p.ExternalTrack("Hold the Line", "Toto", "Toto") }},
		{"a held track let go", p.ForgetHeld},
		{"a remote going", p.RemoteGone},
	} {
		was := told
		c.change()
		if told != was+1 {
			t.Errorf("%s: told %d times, want once", c.what, told-was)
		}
	}
}

// A receiver's song names the room only while the receiver's own track is the one playing: a name
// left from an AirPlay session that is over does not stand for what plays after it.
func TestAReceivedSongIsNamedOnlyWhileItsTrackPlays(t *testing.T) {
	p := &Player{stream: &Stream{}, mp: &esphome.MediaPlayer{}}
	p.SetReceivedTrack("AirPlay", "Rosanna", "Toto", "Toto IV")

	p.stream.track = &track{item: "AirPlay", received: true}
	if from, title, artist, album := p.ReceivedTrack(); from != "AirPlay" || title != "Rosanna" || artist != "Toto" || album != "Toto IV" {
		t.Errorf("while its track plays: %q %q %q %q, want AirPlay's song", from, title, artist, album)
	}

	p.stream.track = &track{item: "Spotify", received: true}
	if from, title, artist, album := p.ReceivedTrack(); from != "Spotify" || title != "" || artist != "" || album != "" {
		t.Errorf("while another receiver plays: %q %q %q %q, want its name and no song", from, title, artist, album)
	}

	p.stream.track = &track{item: "http://radio.example/stream"}
	if from, title, _, _ := p.ReceivedTrack(); from != "" || title != "" {
		t.Errorf("while a station plays: %q %q, want nothing", from, title)
	}

	p.stream.track = nil
	if from, title, _, _ := p.ReceivedTrack(); from != "" || title != "" {
		t.Errorf("with nothing playing: %q %q, want nothing", from, title)
	}
}
