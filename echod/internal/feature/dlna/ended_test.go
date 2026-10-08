package dlna

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

type closer struct{ closed bool }

func (c *closer) Close() error { c.closed = true; return nil }

// A read disarms the deadline that came before it, so a pause of any length after it does not close
// the song; the stream running out says so, once it does.
func TestTheSourceSaysWhenItRanOut(t *testing.T) {
	body := &closer{}
	ended := 0
	s := newSource(strings.NewReader("abc"), body, func() { ended++ })
	_ = s.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
	buf := make([]byte, 8)
	if n, err := s.Read(buf); n != 3 || err != nil {
		t.Fatalf("read = %d, %v", n, err)
	}
	time.Sleep(60 * time.Millisecond) // a pause, as the speaker's
	if body.closed {
		t.Fatal("the deadline of a finished read closed the song")
	}
	if ended != 0 {
		t.Fatal("ended before the end")
	}
	if _, err := s.Read(buf); !errors.Is(err, io.EOF) {
		t.Fatalf("read at the end = %v", err)
	}
	if ended != 1 {
		t.Fatalf("ended told %d times", ended)
	}
}

// A song stopped from elsewhere (the screen, a station) is not an end to go on to the next song from;
// one whose stream ran out is.
func TestOnlyARunOutSongHasEnded(t *testing.T) {
	f := &Feature{}
	f.r.f = f
	r := &f.r
	r.cur = song{uri: "http://192.0.2.10/song.flac", title: "Winding Road"}
	r.gen, r.state = 3, stPlaying
	r.sync()
	if r.ended || r.state != stStopped {
		t.Errorf("stopped from elsewhere: ended %v, state %s", r.ended, r.state)
	}
	r.state, r.endedGen = stPlaying, 3
	r.sync()
	if !r.ended {
		t.Error("a song that ran out has not ended")
	}
	// A new song set clears it: the old end is not the new song's.
	if err := r.set("http://192.0.2.10/other.flac", "", "test"); err != nil {
		t.Fatal(err)
	}
	if r.ended || r.state != stStopped || r.cur.uri != "http://192.0.2.10/other.flac" {
		t.Errorf("after a new song: ended %v, state %s, uri %s", r.ended, r.state, r.cur.uri)
	}
}
