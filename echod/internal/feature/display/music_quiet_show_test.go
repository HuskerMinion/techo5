//go:build !dot && !spot

package display

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// showFrame is what the scene decides for the idle page each frame: the full page, the strip, or
// neither (the clock).
func showFrame(d *Display, now time.Time, rd home.Radio, playing bool) (page, strip bool) {
	d.quietAway(now, rd, true, true, playing)
	page = !d.putAway(rd, true, playing)
	if d.stripDue(now, rd, page) {
		return false, true
	}
	return page, false
}

// With the setting on Full page, a paused track's page goes back to the clock after half a minute and
// comes back when it plays again; the track is not touched (#150).
func TestAPausedPageGoesToTheClock(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	d := &Display{}
	song := home.Radio{Title: "Africa", Now: "Music Assistant"}
	t0 := time.Unix(1_790_000_000, 0)

	if page, _ := showFrame(d, t0, song, true); !page {
		t.Fatal("no page while playing")
	}
	if page, _ := showFrame(d, t0.Add(10*time.Minute), song, true); !page {
		t.Fatal("the page went while the music played on")
	}
	paused := t0.Add(11 * time.Minute)
	if page, _ := showFrame(d, paused, song, false); !page {
		t.Error("the page went the moment the music paused")
	}
	if page, _ := showFrame(d, paused.Add(29*time.Second), song, false); !page {
		t.Error("the page went before the half minute")
	}
	if page, strip := showFrame(d, paused.Add(31*time.Second), song, false); page || strip {
		t.Errorf("after the half minute: page %v, strip %v; want the clock", page, strip)
	}
	if page, _ := showFrame(d, paused.Add(10*time.Minute), song, false); page {
		t.Error("the page came back while the music stayed paused")
	}
	if page, _ := showFrame(d, paused.Add(11*time.Minute), song, true); !page {
		t.Error("playing again did not bring the page back")
	}
}

// With the strip in use the paused page goes to the strip, and playing again is whatever the setting
// says for a playing track: here the strip, since the track has played past its 30 s.
func TestAPausedPageGoesToTheStrip(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Screen().MusicStrip(30); err != nil {
		t.Fatal(err)
	}
	d := &Display{}
	song := home.Radio{Title: "Africa", Now: "Music Assistant"}
	t0 := time.Unix(1_790_000_000, 0)

	showFrame(d, t0, song, true)
	paused := t0.Add(5 * time.Second)
	showFrame(d, paused, song, false)
	if page, _ := showFrame(d, paused.Add(20*time.Second), song, false); !page {
		t.Fatal("the page went before either time was up")
	}
	if _, strip := showFrame(d, paused.Add(31*time.Second), song, false); !strip {
		t.Error("the paused page did not go to the strip")
	}
	if _, strip := showFrame(d, paused.Add(40*time.Second), song, true); !strip {
		t.Error("playing again did not follow the setting")
	}
}

// A page brought back by hand while paused, by the clock's word for the music, is given its own half
// minute rather than being taken away again under the finger.
func TestAPausedPageBroughtBackByHand(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	d := &Display{}
	song := home.Radio{Title: "Africa", Now: "Music Assistant"}
	paused := time.Unix(1_790_000_000, 0)

	showFrame(d, paused, song, false)
	if page, _ := showFrame(d, paused.Add(31*time.Second), song, false); page {
		t.Fatal("the paused page stayed past the half minute")
	}

	// What the tap on the clock's word for the music does.
	back := paused.Add(5 * time.Minute)
	d.mu.Lock()
	d.away, d.awayTrack, d.awayStation, d.awayPlaying = false, "", "", false
	d.musicQuiet.reopened(back)
	d.mu.Unlock()

	if page, _ := showFrame(d, back, song, false); !page {
		t.Fatal("the page did not come back for the tap")
	}
	if page, _ := showFrame(d, back.Add(29*time.Second), song, false); !page {
		t.Error("the page brought back by hand went before its half minute")
	}
	if page, _ := showFrame(d, back.Add(31*time.Second), song, false); page {
		t.Error("the page brought back by hand stayed past its half minute")
	}
}
