//go:build !dot && !spot

package display

import (
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/obsws"
)

// slowDeck stands in for the deck's presses: each waits for an answer handed to it.
func slowDeck(t *testing.T) (answers chan error, sent *atomic.Int32) {
	t.Helper()
	answers, sent = make(chan error), &atomic.Int32{}
	was := deckDo
	deckDo = func(int, int) error {
		sent.Add(1)
		return <-answers
	}
	t.Cleanup(func() { deckDo = was })
	return answers, sent
}

// shownPress waits for the press on the display to be as asked.
func shownPress(t *testing.T, d *Display, what string, ok func(deckPress) bool) deckPress {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(2 * time.Millisecond) {
		d.mu.Lock()
		p := d.deckPress
		d.mu.Unlock()
		if ok(p) {
			return p
		}
	}
	t.Fatalf("%s: never", what)
	return deckPress{}
}

// A button looks pressed for as long as its press is going out, however long that is, and takes no
// second press meanwhile; a failure then keeps it red for a while, with why; and a press that worked
// leaves it plain.
func TestADeckPressShowsUntilItIsAnswered(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	answers, sent := slowDeck(t)
	d := &Display{poke: make(chan struct{}, 1)}

	d.pressDeck(3)
	p := shownPress(t, d, "the press going out", func(p deckPress) bool { return p.going })
	if p.button != 3 || !p.pressed(time.Now().Add(4*time.Second)) || p.failedAt(time.Now()) {
		t.Fatalf("a press four seconds out: %+v", p)
	}
	d.pressDeck(3)
	time.Sleep(20 * time.Millisecond)
	if n := sent.Load(); n != 1 {
		t.Errorf("a second tap on a button still going out sent %d presses", n)
	}

	answers <- obsws.ErrNotConnected
	p = shownPress(t, d, "the failure", func(p deckPress) bool { return !p.going })
	now := time.Now()
	if !p.failedAt(now) || p.pressed(now) || p.reason != tr("deck_obs_away") {
		t.Errorf("a press that failed: %+v", p)
	}
	if p.failedAt(now.Add(deckFailedFor+time.Millisecond)) || !p.failedAt(now.Add(deckFailedFor-200*time.Millisecond)) {
		t.Errorf("the failed look lasts until %v, want about %v from %v", p.until, deckFailedFor, now)
	}

	d.pressDeck(3)
	shownPress(t, d, "the next press going out", func(p deckPress) bool { return p.going })
	answers <- nil
	p = shownPress(t, d, "the answer", func(p deckPress) bool { return !p.going })
	if p.failed || p.reason != "" || p.pressed(time.Now().Add(deckPressMin+time.Millisecond)) {
		t.Errorf("a press that worked: %+v", p)
	}
}

// A press answered after another button was tapped is not shown on that button: the look is the later
// press's, and a failure of the earlier one does not turn it red.
func TestALatePressAnswerIsNotTheNextButtons(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	d := &Display{poke: make(chan struct{}, 1)}
	first, second := make(chan error), make(chan error)
	was := deckDo
	deckDo = func(_, i int) error {
		if i == 1 {
			return <-first
		}
		return <-second
	}
	t.Cleanup(func() { deckDo = was })

	d.pressDeck(1)
	shownPress(t, d, "the first going out", func(p deckPress) bool { return p.going && p.button == 1 })
	d.pressDeck(2)
	shownPress(t, d, "the second going out", func(p deckPress) bool { return p.going && p.button == 2 })
	first <- errors.New("the first one's failure")
	time.Sleep(20 * time.Millisecond)
	d.mu.Lock()
	p := d.deckPress
	d.mu.Unlock()
	if !p.going || p.failed || p.button != 2 {
		t.Errorf("the first press's failure showed on the second: %+v", p)
	}
	second <- nil
	p = shownPress(t, d, "the second answered", func(p deckPress) bool { return !p.going })
	if p.button != 2 || p.failed {
		t.Errorf("the second press, answered: %+v", p)
	}
}

// What a failed press says is the kind of failure, and nothing of the error it was given.
func TestADeckReasonHasNothingOfTheError(t *testing.T) {
	err := errors.New("dial tcp 192.0.2.7:4455: connect: no route to host")
	for _, b := range []config.DeckButton{
		{Action: config.DeckPCKeys, Computer: "Studio"},
		{Action: config.DeckOBSScene, Scene: "Live"},
	} {
		if got := deckReason(b, err); got == "" || strings.Contains(got, "192.0.2.7") || strings.Contains(got, "dial") {
			t.Errorf("the reason for %v is %q", b.Action, got)
		}
	}
}

// The foot's line is the failure while there is one, then what is away, OBS before a computer; a deck
// with a computer away is not told that OBS was never set up.
func TestTheDeckFootLine(t *testing.T) {
	for _, c := range []struct {
		what    string
		v       deckView
		want    string
		failure bool
	}{
		{"all well", deckView{obsSet: true, connected: true}, "", false},
		{"a press failed", deckView{obsSet: true, problem: "OBS refused the password", away: "Studio", reason: "OBS refused that"}, "OBS refused that", true},
		{"OBS away, with why", deckView{obsSet: true, problem: "OBS refused the password", away: "Studio"}, "OBS refused the password", false},
		{"OBS on its way", deckView{obsSet: true}, tr("connecting_to_obs"), false},
		{"a computer away", deckView{obsSet: true, connected: true, away: "Studio"}, "Studio is not answering", false},
		{"a computer away and no OBS", deckView{away: "Studio"}, "Studio is not answering", false},
		{"no OBS", deckView{}, "OBS isn't set up: setup page → Screen & Photos → Deck", false},
	} {
		if got, failure := c.v.footLine(); got != c.want || failure != c.failure {
			t.Errorf("%s: %q (a failure %v), want %q (%v)", c.what, got, failure, c.want, c.failure)
		}
	}
}

// The reason is drawn in the accent in its half of the foot, cut to fit, clear of the way back to the
// clock. SHOW_PREVIEW=<dir> writes the pages out to look at.
func TestTheDeckSaysWhyAPressFailed(t *testing.T) {
	dir := os.Getenv("SHOW_PREVIEW")
	at := time.Date(2026, 10, 5, 14, 7, 0, 0, time.Local)
	for _, panel := range []struct {
		name       string
		wide, high int
	}{{"", showWide, showHigh}, {"-show8", show8Wide, show8High}, {"-skylight", skylightWide, skylightHigh}} {
		frame := func(name string, v deckView) (*image.RGBA, *renderer) {
			img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
			r := newRenderer(img)
			r.draw(scene{now: at, phase: "idle", showDeck: true, deck: v})
			if dir != "" && name != "" {
				f, err := os.Create(filepath.Join(dir, name+panel.name+".png"))
				if err != nil {
					t.Fatal(err)
				}
				png.Encode(f, img)
				f.Close()
			}
			return img, r
		}
		with := func(change func(v *deckView)) deckView {
			v := streamerDeck(true)
			v.buttons[7].pressed, v.buttons[9].failed = false, false
			change(&v)
			return v
		}
		base, r := frame("", with(func(*deckView) {}))
		foot := image.Rect(0, panel.high-r.s(deckFoot), panel.wide, panel.high)
		left := image.Rect(0, foot.Min.Y, panel.wide/2, foot.Max.Y)

		said, _ := frame("deck-press-failed", with(func(v *deckView) {
			v.buttons[9].failed = true
			v.reason = "The key for a computer with a long name on the desk upstairs doesn't match what it has"
		}))
		if inkIn(said, left, amber) == 0 {
			t.Errorf("%s: a failed press has no line in the accent at the foot", panel.name)
		}
		// Against the same red button with no reason, whose glow reaches a little into the foot.
		red, _ := frame("", with(func(v *deckView) { v.buttons[9].failed = true }))
		if n := changedIn(red, said, image.Rect(panel.wide/2, foot.Min.Y, panel.wide, foot.Max.Y)); n != 0 {
			t.Errorf("%s: the reason reaches %d pixels of the dots' and the clock's half of the foot", panel.name, n)
		}

		gone, _ := frame("deck-computer-away", with(func(v *deckView) { v.away = "Studio" }))
		if changedIn(base, gone, left) == 0 || inkIn(gone, left, amber) != 0 {
			t.Errorf("%s: a computer away has no line at the foot, or one in the failure's color", panel.name)
		}
		frame("deck-press-going", with(func(v *deckView) { v.buttons[5].pressed = true }))
	}
}
