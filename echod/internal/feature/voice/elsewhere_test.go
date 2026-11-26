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
		if c.playElsewhere(t.Context(), 1, url, func() { playing++ }, false) {
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
	if c.playElsewhere(t.Context(), 1, url, nil, false) {
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
	if c.playElsewhere(t.Context(), 1, url, nil, false) {
		t.Error("sent with no player chosen")
	}
	config.Set().Brain().SetReplyPlayer("media_player.dot")
	if c.playElsewhere(t.Context(), 1, "", nil, false) {
		t.Error("sent a stream, which has no url")
	}
	if len(*got) != 0 {
		t.Fatalf("sent %v", *got)
	}
}

// With Reply delivery set to Early, a chosen player is handed the reply's url as soon as Home Assistant
// offers it, while the agent is still writing (#707): before, the device waited for the whole reply to
// be written and downloaded, and a long answer started many seconds late. The url Home Assistant sends
// again once the reply is written is the same reply and is not sent a second time, and the turn still
// waits for the reply's length, counted from when the player was handed it, before it counts as heard.
func TestAnEarlyReplyGoesToThePlayerWhileItIsWritten(t *testing.T) {
	got := fakePlayer(t, nil)
	handed := make(chan struct{}, 4)
	fake := callPlayer
	callPlayer = func(e, u string) error {
		err := fake(e, u)
		handed <- struct{}{}
		return err
	}
	c := thinkingTurn(t)
	config.Set().Brain().SetReplyPlayer("media_player.living_room")
	if err := config.Set().Wake(0).Delivery(config.DeliveryEarly); err != nil {
		t.Fatal(err)
	}
	if wakeword.Delivery(0) != config.DeliveryEarly {
		t.Skip("could not set the delivery")
	}
	url, release := heldSpeech(t)
	c.runURL = url

	c.handle(event{kind: evEarlyReply})
	select {
	case <-handed:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("the player was not handed the reply while it was still being written")
	}
	sentAt := time.Now()

	// The agent finishes: the text, the url again and the streamed copy arrive.
	c.handle(event{kind: evReplyText, text: "Here are three short poems."})
	c.handle(event{kind: evReplyURL, url: url})
	c.handle(event{kind: evStreamStart})
	c.handle(event{kind: evStreamAudio, audio: make([]byte, 640)})
	close(release)

	next(t, c, evPlayed)
	if since := time.Since(sentAt); since < 900*time.Millisecond {
		t.Errorf("the turn counted the reply heard after %v, before its second was over", since)
	}
	if len(*got) != 1 || (*got)[0] != (sent{"media_player.living_room", url}) {
		t.Fatalf("sent %v, want the reply once", *got)
	}
}

// With Early chosen but no offer from Home Assistant (an agent that does not stream its answer), a
// chosen player gets the reply once it is written, fetched whole first, as before.
func TestAnEarlySlotWithoutTheOfferSendsThePlayerTheWholeReply(t *testing.T) {
	got := fakePlayer(t, nil)
	c := thinkingTurn(t)
	config.Set().Brain().SetReplyPlayer("media_player.dot")
	if err := config.Set().Wake(0).Delivery(config.DeliveryEarly); err != nil {
		t.Fatal(err)
	}
	url, release := heldSpeech(t)
	close(release)
	c.handle(event{kind: evReplyText, text: "Hi."})
	c.handle(event{kind: evReplyURL, url: url})
	if c.reply.early {
		t.Error("a reply with no early offer was taken as early")
	}
	next(t, c, evPlayed)
	if len(*got) != 1 {
		t.Fatalf("sent %v", *got)
	}
}

// An early reply the player does not take plays on this device, as it is written, rather than not at
// all.
func TestAnEarlyReplyThePlayerRefusesPlaysHere(t *testing.T) {
	got := fakePlayer(t, errors.New("unavailable"))
	c := thinkingTurn(t)
	config.Set().Brain().SetReplyPlayer("media_player.dot")
	if err := config.Set().Wake(0).Delivery(config.DeliveryEarly); err != nil {
		t.Fatal(err)
	}
	url, release := heldSpeech(t)
	close(release)
	c.runURL = url
	c.handle(event{kind: evEarlyReply})
	if !c.reply.early {
		t.Fatal("the early offer was not taken")
	}
	next(t, c, evPlayed)
	if len(*got) != 1 {
		t.Fatalf("calls %v", *got)
	}
	if !failedElsewhere.Load() {
		t.Error("the refusal was not remembered")
	}
}

// A turn stopped while its early reply plays on another player stops it there too: the player was
// handed it as the agent began writing, and would otherwise read a long answer to the end.
func TestAStoppedEarlyReplyStopsThePlayer(t *testing.T) {
	_ = fakePlayer(t, nil)
	handed := make(chan struct{}, 4)
	fake := callPlayer
	callPlayer = func(e, u string) error {
		err := fake(e, u)
		handed <- struct{}{}
		return err
	}
	stopped := make(chan string, 4)
	oldStop := stopPlayer
	stopPlayer = func(e string) error { stopped <- e; return nil }
	t.Cleanup(func() { stopPlayer = oldStop })

	c := thinkingTurn(t)
	config.Set().Brain().SetReplyPlayer("media_player.living_room")
	if err := config.Set().Wake(0).Delivery(config.DeliveryEarly); err != nil {
		t.Fatal(err)
	}
	if wakeword.Delivery(0) != config.DeliveryEarly {
		t.Skip("could not set the delivery")
	}
	url, release := heldSpeech(t)
	defer close(release)
	c.runURL = url

	c.handle(event{kind: evEarlyReply})
	select {
	case <-handed:
	case <-time.After(5 * time.Second):
		t.Fatal("the player was not handed the reply")
	}
	c.handle(event{kind: evCancel})
	select {
	case e := <-stopped:
		if e != "media_player.living_room" {
			t.Errorf("stopped %q", e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the player was not stopped with the turn")
	}
}
