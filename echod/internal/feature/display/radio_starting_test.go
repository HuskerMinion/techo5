//go:build !dot && !spot

package display

import (
	"image"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// A station is starting from its tap until it is the one playing, and not once it is, or with none
// asked for.
func TestRadioStarting(t *testing.T) {
	for _, c := range []struct {
		rd   home.Radio
		want string
	}{
		{home.Radio{}, ""},
		{home.Radio{Chosen: "KXYZ 101.1"}, "KXYZ 101.1"},
		{home.Radio{Chosen: "KXYZ 101.1", Now: "Morning News 850", Playing: true}, "KXYZ 101.1"},
		{home.Radio{Chosen: "KXYZ 101.1", Now: "KXYZ 101.1", Playing: true}, ""},
		{home.Radio{Now: "KXYZ 101.1", Playing: true}, ""},
	} {
		if got := radioStarting(c.rd); got != c.want {
			t.Errorf("chosen %q, now %q: starting %q, want %q", c.rd.Chosen, c.rd.Now, got, c.want)
		}
	}
}

// The now-playing page of a station that is starting says so in its status line and names the station
// asked for, not the one before or its song; once it plays the page is the usual one.
func TestTheNowPlayingPageSaysStarting(t *testing.T) {
	at := time.Date(2026, 10, 20, 9, 30, 0, 0, time.Local)
	for _, modern := range []bool{false, true} {
		for _, panel := range showPanels() {
			frame := func(rd home.Radio, playing bool) *image.RGBA {
				img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
				newRenderer(img).draw(scene{now: at, phase: "idle", nowPlaying: true, modernMusic: modern, playing: playing, radio: rd})
				return img
			}
			starting := frame(home.Radio{Chosen: "KXYZ 101.1"}, false)
			named := frame(home.Radio{Now: "KXYZ 101.1"}, false)
			if changedIn(starting, named, starting.Rect) == 0 {
				t.Errorf("%s modern %v: a station starting looks like one only named", panel.name, modern)
			}
			over := frame(home.Radio{Chosen: "KXYZ 101.1", Now: "Morning News 850", Title: "The Headlines", Artist: "A Reader", Playing: true}, false)
			if n := changedIn(starting, over, starting.Rect); n != 0 {
				t.Errorf("%s modern %v: the station before shows in %d pixels of the starting page", panel.name, modern, n)
			}
			playing := frame(home.Radio{Chosen: "KXYZ 101.1", Now: "KXYZ 101.1", Playing: true}, true)
			usual := frame(home.Radio{Now: "KXYZ 101.1", Playing: true}, true)
			if n := changedIn(playing, usual, playing.Rect); n != 0 {
				t.Errorf("%s modern %v: a station that plays still differs in %d pixels", panel.name, modern, n)
			}
		}
	}
}
