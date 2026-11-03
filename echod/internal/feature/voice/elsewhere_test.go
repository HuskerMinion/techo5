package voice

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/wakeword"
)

type sent struct{ entity, url string }

// fakePlayer stands in for Home Assistant's play_media and quick waits.
func fakePlayer(t *testing.T, err error) *[]sent {
	t.Helper()
	var mu sync.Mutex
	var got []sent
	oldCall, oldMargin, oldUp := callPlayer, elsewhereMargin, playerUp
	playerUp = func(string) bool { return true }
	callPlayer = func(e, u string) error {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, sent{e, u})
		return err
	}
	elsewhereMargin = 0
	failedElsewhere.Store(false)
	t.Cleanup(func() { callPlayer, elsewhereMargin, playerUp = oldCall, oldMargin, oldUp })
	return &got
}

// A reply url goes to the chosen player as an announcement target, and the turn waits for its length
// (a second here) before it counts as played, then listens again as it does for any reply.
func TestAReplyGoesToTheChosenPlayer(t *testing.T) {
	got := fakePlayer(t, nil)
	c := thinkingTurn(t)
	config.Set().Brain().SetReplyPlayer("media_player.kitchen_dot")
	url, release := heldSpeech(t)
	close(release)

	c.handle(event{kind: evReplyText, text: "It's noon."})
	c.handle(event{kind: evReplyURL, url: url})
	start := time.Now()
	next(t, c, evPlayed)
	if time.Since(start) < 900*time.Millisecond {
		t.Errorf("the turn counted the reply heard after %v, before its second was over", time.Since(start))
	}
	if len(*got) != 1 || (*got)[0] != (sent{"media_player.kitchen_dot", url}) {
		t.Fatalf("sent %v", *got)
	}
}

// With a player chosen, a slot set to streamed replies still takes the url; the streamed copy is dropped.
func TestAStreamedSlotStillUsesTheUrlForAPlayer(t *testing.T) {
	got := fakePlayer(t, nil)
	c := thinkingTurn(t)
	config.Set().Brain().SetReplyPlayer("media_player.dot")
	if err := config.Set().Wake(0).Delivery(config.DeliveryStream); err != nil {
		t.Fatal(err)
	}
	if wakeword.Delivery(0) != config.DeliveryStream {
		t.Skip("could not set the delivery")
	}
	url, release := heldSpeech(t)
	close(release)
	c.handle(event{kind: evReplyText, text: "Hi."})
	c.handle(event{kind: evReplyURL, url: url})
	c.handle(event{kind: evStreamStart})
	c.handle(event{kind: evStreamAudio, audio: make([]byte, 640)})
	next(t, c, evPlayed)
	if len(*got) != 1 {
		t.Fatalf("sent %v", *got)
	}
}

// A player that does not take the reply is logged once and the reply plays on this device instead.
func TestAFailedPlayerFallsBackOnce(t *testing.T) {
	got := fakePlayer(t, errors.New("unavailable"))
	c := thinkingTurn(t)
	config.Set().Brain().SetReplyPlayer("media_player.dot")
	url, release := heldSpeech(t)
	close(release)
	playing := 0
	for i := 0; i < 2; i++ {
		if c.playElsewhere(t.Context(), url, func() { playing++ }) {
			t.Fatal("a failed call counted as played elsewhere")
		}
	}
	if len(*got) != 2 || playing != 0 {
		t.Fatalf("calls %d, playing %d", len(*got), playing)
	}
	if !failedElsewhere.Load() {
		t.Error("the failure was not remembered, so it would be logged every reply")
	}
}

// A player Home Assistant reports unavailable is not called, since the call would succeed and play
// nothing: the reply plays here.
func TestAnUnavailablePlayerIsNotCalled(t *testing.T) {
	got := fakePlayer(t, nil)
	playerUp = func(string) bool { return false }
	c := thinkingTurn(t)
	config.Set().Brain().SetReplyPlayer("media_player.dot")
	url, release := heldSpeech(t)
	close(release)
	if c.playElsewhere(t.Context(), url, nil) {
		t.Fatal("an unavailable player counted as played elsewhere")
	}
	if len(*got) != 0 {
		t.Fatalf("an unavailable player was called: %v", *got)
	}
}

// Nothing chosen, or no url (the built-in assistant and realtime voice stream), stays on the device.
func TestRepliesStayHereWithoutAPlayerOrUrl(t *testing.T) {
	got := fakePlayer(t, nil)
	c := thinkingTurn(t)
	url, release := heldSpeech(t)
	close(release)
	if c.playElsewhere(t.Context(), url, nil) {
		t.Error("sent with no player chosen")
	}
	config.Set().Brain().SetReplyPlayer("media_player.dot")
	if c.playElsewhere(t.Context(), "", nil) {
		t.Error("sent a stream, which has no url")
	}
	if len(*got) != 0 {
		t.Fatalf("sent %v", *got)
	}
}
