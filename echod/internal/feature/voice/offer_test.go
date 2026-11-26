package voice

import (
	"path/filepath"
	"testing"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/wakeword"
)

// A device whose replies play on another player says it has no speaker of its own, which is what has
// Home Assistant make the reply in the media player's announcement format rather than 16 kHz (#715),
// and nothing else changes: announcements, starting a conversation and timers stay. One whose replies
// play here says exactly what it always has.
func TestFeaturesForAPlayerDropOnlyTheSpeaker(t *testing.T) {
	if FeaturesFor(false) != Features {
		t.Errorf("replies here offer %b, want %b", FeaturesFor(false), Features)
	}
	got := FeaturesFor(true)
	if got&esphome.FeatureSpeaker != 0 {
		t.Error("replies elsewhere still offer the speaker, so Home Assistant makes them at 16 kHz")
	}
	if got|esphome.FeatureSpeaker != Features {
		t.Errorf("replies elsewhere offer %b, want %b without only the speaker", got, Features)
	}
}

// What is offered follows the setting each time it is asked, and is remembered for the turns on that
// connection.
func TestOfferFollowsWhereRepliesPlay(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { unstreamed.Store(false) })
	if Offer() != Features || !streamedReplies() {
		t.Fatal("with no player the offer is not the usual one")
	}
	if err := config.Set().Brain().SetReplyPlayer("media_player.kitchen"); err != nil {
		t.Fatal(err)
	}
	if !streamedReplies() {
		t.Error("the setting changed what this connection was told before it was told again")
	}
	if Offer() != FeaturesFor(true) || streamedReplies() {
		t.Fatal("with a player the offer still has the speaker")
	}
	if err := config.Set().Brain().SetReplyPlayer(""); err != nil {
		t.Fatal(err)
	}
	if Offer() != Features || !streamedReplies() {
		t.Fatal("clearing the player did not bring the usual offer back")
	}
}

// A player cleared on the setup page leaves Home Assistant on a connection told the device has no
// speaker until it reconnects: no streamed copy of the reply comes, so a slot set to streamed replies
// plays the url here rather than waiting for a stream and staying silent.
func TestAStreamedSlotPlaysTheUrlWhenNoStreamWasOffered(t *testing.T) {
	got := fakePlayer(t, nil)
	c := thinkingTurn(t)
	if err := config.Set().Wake(0).Delivery(config.DeliveryStream); err != nil {
		t.Fatal(err)
	}
	if wakeword.Delivery(0) != config.DeliveryStream {
		t.Skip("could not set the delivery")
	}
	unstreamed.Store(true)
	t.Cleanup(func() { unstreamed.Store(false) })
	url, release := heldSpeech(t)
	close(release)

	c.handle(event{kind: evReplyText, text: "Hi."})
	c.handle(event{kind: evReplyURL, url: url})
	if c.phase != phaseReplying || c.reply.url != url {
		t.Fatalf("the url was ignored: phase %v, reply url %q", c.phase, c.reply.url)
	}
	next(t, c, evPlayed)
	if len(*got) != 0 {
		t.Fatalf("a reply with no player chosen was sent to one: %v", *got)
	}
}

// On a connection offered the speaker, a streamed slot with no player still leaves the url alone and
// waits for the stream, as it always has.
func TestAStreamedSlotStillWaitsForTheStreamWhenOffered(t *testing.T) {
	c := thinkingTurn(t)
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
	if c.reply.url != "" {
		t.Fatalf("a streamed slot took the url %q", c.reply.url)
	}
}
