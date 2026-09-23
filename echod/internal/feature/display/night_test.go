//go:build !dot && !spot

package display

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
)

// The night schedule asks when the conversation last moved, and the conversation is what answers.
//
// changed runs on the conversation's goroutine and night on the display loop's, so the two run at the
// same time all day. The read has to be under the same lock as the write; it was not, and the race
// detector is what says so, but the behavior is checkable here too: a turn that has only just moved
// keeps the screen on inside the night window, every time, with the turns still arriving.
func TestNightSeesAFreshConversationFromTheOtherGoroutine(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "config.json"))

	// An hour either side of now, so the window covers the current hour whatever minute it is. Half
	// the time it crosses midnight, which is the case worth getting right.
	h := time.Now().Hour()
	if err := config.Set().Screen().Night(fmt.Sprintf("%d-%d", (h+23)%24, (h+2)%24)); err != nil {
		t.Fatal(err)
	}

	// The light entity as build() makes it: night can decide to work the screen, and apply sets the
	// light without asking whether there is one. A Display put together by hand in a test has to
	// carry it, or the test passes or dies depending on what the tests before it left in the
	// configuration - which is how this one first behaved.
	d := &Display{
		poke: make(chan struct{}, 1),
		view: voice.State{Phase: "idle"},
		light: &esphome.Light{
			Base:                esphome.Base{ObjectID: "screen", Name: "Screen", Icon: "mdi:monitor"},
			SupportedColorModes: []esphome.ColorMode{esphome.ColorModeBrightness},
		},
	}
	// A finger long enough ago that only the conversation can be what keeps the screen on.
	d.touchedAt = time.Now().Add(-time.Hour)

	// One turn before the loop starts. viewAt is zero on a Display that has just been made, which
	// reads as a conversation that last moved in 1970 - so if the checking loop wins the race to the
	// first iteration, the screen goes dark for a perfectly good reason and the test blames the code.
	d.changed(voice.State{Phase: "idle"})

	var turns sync.WaitGroup
	turns.Add(1)
	done := make(chan struct{})
	go func() {
		defer turns.Done()
		for {
			select {
			case <-done:
				return
			default:
			}
			d.changed(voice.State{Phase: "idle"})
		}
	}()

	for range 2000 {
		if d.night(time.Now(), true, voice.State{Phase: "idle"}) {
			close(done)
			turns.Wait()
			t.Fatal("the screen went dark during a conversation")
		}
	}
	close(done)
	turns.Wait()

	d.mu.Lock()
	dark := d.nightDark
	d.mu.Unlock()
	if dark {
		t.Error("the night schedule recorded the screen as dark")
	}
}

// A screen the night put out, then switched off by hand, stays off when the night ends: the night only
// puts back what it took.
func TestTheNightDoesNotRelightAScreenSwitchedOffByHand(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "config.json"))
	// A window that has already ended by now.
	h := time.Now().Hour()
	if err := config.Set().Screen().Night(fmt.Sprintf("%d-%d", (h+20)%24, (h+22)%24)); err != nil {
		t.Fatal(err)
	}
	d := &Display{
		poke: make(chan struct{}, 1),
		view: voice.State{Phase: "idle"},
		light: &esphome.Light{
			Base:                esphome.Base{ObjectID: "screen", Name: "Screen", Icon: "mdi:monitor"},
			SupportedColorModes: []esphome.ColorMode{esphome.ColorModeBrightness},
		},
	}
	d.nightDark = true
	d.apply(false, 50, true) // switched off by hand

	if d.night(time.Now(), false, voice.State{Phase: "idle"}) {
		t.Error("the end of the night switched on a screen somebody had switched off")
	}
}

// With the night light chosen, the night leaves the screen on at a glow rather than putting it out; the
// first touch only brings it up, and after that the screen stays up while it is being used.
func TestTheNightLightGlowsAndATouchBringsItUp(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "config.json"))
	h := time.Now().Hour()
	if err := config.Set().Screen().Night(fmt.Sprintf("%d-%d", (h+23)%24, (h+2)%24)); err != nil {
		t.Fatal(err)
	}
	if err := config.Set().Screen().NightLight(true); err != nil {
		t.Fatal(err)
	}
	d := &Display{
		poke: make(chan struct{}, 1),
		view: voice.State{Phase: "idle"},
		light: &esphome.Light{
			Base:                esphome.Base{ObjectID: "screen", Name: "Screen", Icon: "mdi:monitor"},
			SupportedColorModes: []esphome.ColorMode{esphome.ColorModeBrightness},
		},
		on: true, ceiling: 60,
	}
	d.touchedAt = time.Now().Add(-time.Hour)
	d.viewAt = time.Now().Add(-time.Hour)

	if !d.night(time.Now(), true, voice.State{Phase: "idle"}) {
		t.Fatal("the night did nothing to an idle screen")
	}
	d.mu.Lock()
	glowing, on, dark := d.nightGlow, d.on, d.nightDark
	d.mu.Unlock()
	if !glowing || !on || dark {
		t.Fatalf("want a glowing screen that is still on: glow=%v on=%v dark=%v", glowing, on, dark)
	}

	d.gesture(touch.Gesture{Kind: touch.Tap, X: 10, Y: 10})
	d.mu.Lock()
	glowing = d.nightGlow
	d.mu.Unlock()
	if glowing {
		t.Fatal("a touch left the screen at the night light")
	}
	if d.night(time.Now(), true, voice.State{Phase: "idle"}) {
		t.Error("the screen went back down while it was being used")
	}
}
