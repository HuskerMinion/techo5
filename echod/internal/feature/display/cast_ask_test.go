//go:build !dot && !spot

package display

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// With the stream deck up when a phone asks, the deck gives way: its taps are not the question's, so a tap
// on Accept cannot press a deck button under it.
func TestTheDeckGivesWayToACastQuestion(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Deck().Button(0, 0, config.DeckButton{Action: config.DeckOBSStream}); err != nil {
		t.Fatal(err)
	}
	d := &Display{deckUp: true, poke: make(chan struct{}, 1)}
	d.view.Phase = "idle"
	s := scene{phase: "idle"}
	d.deckScene(&s, time.Now())
	if !d.deckShowing() {
		t.Fatal("the deck is not up in a plain scene, so the test proves nothing")
	}
	s = scene{phase: "idle", showCastAsk: true}
	d.deckScene(&s, time.Now())
	if d.deckShowing() {
		t.Fatal("the deck kept the touches with a phone waiting to be accepted")
	}
}

func TestACastQuestionCoversAVideo(t *testing.T) {
	if !videoCovered(scene{showCastAsk: true}) {
		t.Fatal("a video was left playing over a phone's question")
	}
}

// The settle starts only when the question is on top: behind the setup page's or a DLNA video's question
// it would be answerable the moment the other page went.
func TestTheCastQuestionSettlesOnlyWhenDrawn(t *testing.T) {
	d := &Display{}
	now := time.Now()
	d.castLatch(&scene{showCastAsk: true, setupAsking: true}, 7, now)
	if _, ok := d.cask.answerable(now.Add(time.Second)); ok {
		t.Fatal("a question behind the setup page's was answerable")
	}
	d.castLatch(&scene{showCastAsk: true, showVideoAsk: true}, 7, now)
	if _, ok := d.cask.answerable(now.Add(time.Second)); ok {
		t.Fatal("a question behind a DLNA video's was answerable")
	}
	// On top: the settle runs from when it is first drawn.
	d.castLatch(&scene{showCastAsk: true}, 7, now.Add(2*time.Second))
	if _, ok := d.cask.answerable(now.Add(2*time.Second + 100*time.Millisecond)); ok {
		t.Fatal("answerable before it had settled")
	}
	if id, ok := d.cask.answerable(now.Add(3 * time.Second)); !ok || id != 7 {
		t.Fatal("not answerable after it had settled")
	}
}
