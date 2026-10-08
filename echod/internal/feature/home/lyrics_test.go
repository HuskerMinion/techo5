package home

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/lib/lyrics"
)

// The words show only with the switch on and a source that says where the song is; the line is the one
// being sung at that point, and the time to the next is the wait for the next redraw.
func TestTheWordsFollowTheSong(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	f := Get()
	r := Radio{Title: "Winding Road", Artist: "The Made-Up Band"}
	rememberLyrics(r.Title+"\x00"+r.Artist, lyricsEntry{lines: []lyrics.Line{
		{At: 5 * time.Second, Text: "first"}, {At: 9 * time.Second, Text: "second"},
	}})
	start := time.Now()
	media.SetPosition(media.Position{Title: r.Title, At: start, Pos: 6 * time.Second, Dur: time.Minute, Rate: 1})
	t.Cleanup(media.ClearPosition)

	if _, ok := f.LyricNow(r, start); ok {
		t.Fatal("words shown with the switch off")
	}
	if err := config.Set().Home().Lyrics(true); err != nil {
		t.Fatal(err)
	}
	l, ok := f.LyricNow(r, start.Add(time.Second))
	if !ok || l.Line != "first" || l.Next != "second" || l.In != 2*time.Second {
		t.Errorf("at 7 s: %+v %v", l, ok)
	}
	if _, ok := f.LyricNow(Radio{Title: "Another Song", Artist: "Someone"}, start); ok {
		t.Error("words shown for a song whose position nobody gave")
	}
}
