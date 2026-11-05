package voice

import (
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Home Assistant streams a reply over the API as well as serving its url, and stops the copy only when
// the next run starts. An early reply is heard out first: what is left of the copy belongs to no turn,
// and the follow-up after it neither plays it nor ends on its TTS_STREAM_END.
func TestTheStreamedCopyOfAnEarlyReplyStaysOutOfTheFollowUp(t *testing.T) {
	c := thinkingTurn(t)
	if err := config.Set().Wake(0).Delivery(config.DeliveryEarly); err != nil {
		t.Fatal(err)
	}
	url, release := heldSpeech(t)
	close(release)

	c.handle(event{kind: evReplyText, text: "Next question."})
	c.handle(event{kind: evReplyURL, url: url})
	c.handle(event{kind: evStreamStart})
	c.handle(next(t, c, evPlayed))

	// The follow-up, listening.
	c.enter(phaseListening)
	c.handle(event{kind: evStreamAudio, audio: make([]byte, 1024)})
	if c.phase != phaseListening {
		t.Fatalf("the last reply's streamed copy opened a reply in the follow-up: phase %s", c.phase)
	}
	c.handle(event{kind: evStreamEnd})

	// The follow-up's own streamed reply is taken again.
	c.handle(event{kind: evStreamStart})
	c.handle(event{kind: evStreamAudio, audio: make([]byte, 1024)})
	if c.phase != phaseReplying {
		t.Fatalf("the follow-up's own stream was dropped: phase %s", c.phase)
	}
}
