//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
)

// A field on the streamed page getting focus brings the keyboard up with the field's text; each word
// from the server is acted on once, so the keyboard put away here stays away until the next; the page
// going brings it down. Keys type into the field here as they go to the page.
func TestTheStreamedPagesKeyboard(t *testing.T) {
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	d := &Display{r: r, poke: make(chan struct{}, 1)}
	sceneWith := func(fl dashboard.Field, streamed bool) scene {
		s := scene{dash: dashboard.View{Ready: true, Field: fl}}
		d.dashKeyboardScene(&s, streamed)
		return s
	}

	up := dashboard.Field{Up: true, Kind: "search", Value: "Abba", Label: "Search music", Seq: 1}
	if s := sceneWith(up, true); !s.dashKbd.up || s.dashKbd.text != "Abba" || s.dashKbd.label != "Search music" {
		t.Fatalf("a field with focus: %+v", s.dashKbd)
	}
	for _, k := range []string{"shift", "g", "space", "backspace", "x"} {
		d.dashKey(k)
	}
	if s := sceneWith(up, true); s.dashKbd.text != "AbbaGx" || s.dashKbd.shifted {
		t.Errorf("typed %q (shift %v), want shift to make one capital and backspace to take one away", s.dashKbd.text, s.dashKbd.shifted)
	}
	d.dashKey("join")
	if s := sceneWith(up, true); s.dashKbd.up {
		t.Error("Done left the keyboard up, or the same word from the server brought it back")
	}
	up.Seq = 3
	if s := sceneWith(up, true); !s.dashKbd.up || s.dashKbd.text != "Abba" {
		t.Errorf("the field tapped again: %+v", s.dashKbd)
	}
	if s := sceneWith(up, false); s.dashKbd.up {
		t.Error("the keyboard stayed up with the page gone")
	}
	if s := sceneWith(up, true); s.dashKbd.up {
		t.Error("the page back brought the keyboard up again without a new word")
	}
	if s := sceneWith(dashboard.Field{Seq: 4}, true); s.dashKbd.up {
		t.Error("a field let go of left the keyboard up")
	}
}

// The keyboard left with no key pressed puts itself away, and a key pressed keeps it up; the same
// word from the server does not bring it back, a new one does.
func TestTheStreamedPagesKeyboardLeftAlone(t *testing.T) {
	defer func(was time.Duration) { dashKeyboardIdle = was }(dashKeyboardIdle)
	dashKeyboardIdle = 150 * time.Millisecond
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	d := &Display{r: r, poke: make(chan struct{}, 1)}
	sceneWith := func(fl dashboard.Field) scene {
		s := scene{dash: dashboard.View{Ready: true, Field: fl}}
		d.dashKeyboardScene(&s, true)
		return s
	}
	up := dashboard.Field{Up: true, Kind: "search", Label: "Search music", Seq: 1}
	if s := sceneWith(up); !s.dashKbd.up {
		t.Fatal("a field with focus left the keyboard down")
	}
	time.Sleep(100 * time.Millisecond)
	d.dashKey("a")
	time.Sleep(100 * time.Millisecond)
	if s := sceneWith(up); !s.dashKbd.up {
		t.Error("the keyboard went away though a key was pressed in time")
	}
	select {
	case <-d.poke:
	default:
	}
	select {
	case <-d.poke:
	case <-time.After(time.Second):
		t.Fatal("nothing looked at the screen again once the keyboard was left")
	}
	if s := sceneWith(up); s.dashKbd.up {
		t.Error("the keyboard stayed up, left alone")
	}
	up.Seq = 2
	if s := sceneWith(up); !s.dashKbd.up {
		t.Error("a tap on the field again left the keyboard down")
	}
}

// The keyboard over the streamed page fills the screen, and its keys are the ones keyAt reads. A
// password shows as stars. With SHOW_PREVIEW set, the pages are written there to look at.
func TestTheStreamedPagesKeyboardDraws(t *testing.T) {
	for _, size := range []image.Point{{showWide, showHigh}, {show8Wide, show8High}} {
		for _, c := range []struct {
			name string
			kbd  dashKeyboard
		}{
			{"search", dashKeyboard{up: true, kind: "search", label: "Search music", text: "Abba"}},
			{"password", dashKeyboard{up: true, kind: "password", label: "Password", text: "secret"}},
		} {
			img := image.NewRGBA(image.Rect(0, 0, size.X, size.Y))
			r := newRenderer(img)
			r.draw(scene{phase: "idle", showDash: true, dashMode: config.DashboardStreamed, dashKbd: c.kbd})
			if img.Pix[4*(size.X/2)] == 0 && img.Pix[4*(size.X/2)+1] == 0 && img.Pix[4*(size.X/2)+2] == 0 {
				t.Errorf("%s %v: nothing drawn at the top", c.name, size)
			}
			if k := r.keyAt(r.keyMargin()+r.keyW()/2, r.keyTop()+r.keyH()/2, false); k != "q" {
				t.Errorf("%s %v: the first key reads %q", c.name, size, k)
			}
			writePreview(t, img, fmt.Sprintf("dash-keyboard-%s-%dx%d.png", c.name, size.X, size.Y))
		}
	}
}
