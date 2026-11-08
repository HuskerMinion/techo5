//go:build !dot && !spot

package display

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
)

// fakeCover is a cover a test opens and closes.
type fakeCover struct{ here, closed bool }

func (c *fakeCover) Here() bool            { return c.here }
func (c *fakeCover) Closed() bool          { return c.closed }
func (c *fakeCover) Listen(changed func()) {}

// useCover registers c for a test and takes it away after.
func useCover(t *testing.T, c screenCover) {
	was := screenLid.c
	screenLid.c, screenLid.dark = c, false
	t.Cleanup(func() { screenLid.c, screenLid.dark = was, false })
}

// With no cover registered, or one whose switch is not there, nothing changes.
func TestNoCoverNoChange(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	d := roomLightsDisplay()
	d.apply(true, 40, true)
	useCover(t, nil)
	if d.coverTick(true) || !d.on {
		t.Fatal("no cover changed the screen")
	}
	useCover(t, &fakeCover{here: false, closed: true})
	if d.coverTick(true) || !d.on {
		t.Fatal("a cover not there changed the screen")
	}
}

// Closed, the cover puts the screen out, not saved; opened, it lights it again at its brightness. A
// screen put out by something else stays out when the cover opens.
func TestTheCoverPutsTheScreenOutAndBack(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	d := roomLightsDisplay()
	d.apply(true, 40, true)
	c := &fakeCover{here: true}
	useCover(t, c)

	if d.coverTick(true) {
		t.Fatal("an open cover changed the screen")
	}
	c.closed = true
	if !d.coverTick(true) || d.on {
		t.Fatalf("closed: on %v; want out", d.on)
	}
	if !config.Get().Screen.On {
		t.Fatal("the cover's off was saved as Screen power's")
	}
	if d.coverTick(false) {
		t.Fatal("a second frame under the cover changed the screen")
	}
	c.closed = false
	if !d.coverTick(false) || !d.on || d.ceiling != 40 {
		t.Fatalf("opened: on %v at %d; want lit at 40", d.on, d.ceiling)
	}

	d.apply(false, 40, true) // put out by hand
	c.closed = true
	d.coverTick(false)
	c.closed = false
	if d.coverTick(false) || d.on {
		t.Fatal("a screen put out by hand was lit when the cover opened")
	}
}

// A ring by day under a closed cover leaves the screen out, frame after frame, rather than lighting
// it for the cover to put it out again; once the cover opens, the screen is lit as before.
func TestARingUnderAClosedCoverStaysDark(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	d := roomLightsDisplay()
	d.apply(true, 40, true)
	c := &fakeCover{here: true, closed: true}
	useCover(t, c)
	ring := ringState{timer: "Pasta"}
	now := time.Now()

	on := true
	for frame := range 3 {
		if d.coverTick(on) {
			on = d.on
		}
		on = d.lightForEvents(now, on, false, phone.State{}, ring, false)
		if on || d.on {
			t.Fatalf("frame %d: the ring lit the screen under the closed cover", frame)
		}
	}

	c.closed = false
	if !d.coverTick(on) || !d.on {
		t.Fatal("the cover opened and the screen stayed out")
	}
	d.apply(false, 40, false)
	if !d.lightForEvents(now, false, false, phone.State{}, ring, false) || !d.on {
		t.Fatal("with the cover open, the ring did not light the screen")
	}
}
