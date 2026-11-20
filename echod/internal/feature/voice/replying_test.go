package voice

import (
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"
)

// told collects what Replying says for the length of a test.
func told(t *testing.T) *[]Reply {
	t.Helper()
	var got []Reply
	t.Cleanup(Replying.Listen(func(r Reply) { got = append(got, r) }))
	return &got
}

// playingReply is a turn whose reply, fetched whole, has its first audio, the start told.
func playingReply(t *testing.T, got *[]Reply) *conversation {
	t.Helper()
	c := thinkingTurn(t)
	url, release := heldSpeech(t)
	close(release)
	c.handle(event{kind: evReplyText, text: "It's noon."})
	c.handle(event{kind: evReplyURL, url: url})
	if len(*got) != 0 {
		t.Fatalf("told %+v before the reply's audio came", *got)
	}
	c.handle(next(t, c, evPlaying))
	if len(*got) != 1 || (*got)[0].Began.IsZero() || !(*got)[0].Ended.IsZero() {
		t.Fatalf("told %+v as the audio started, want one reply begun and not ended", *got)
	}
	return c
}

// replyEnded checks that the second telling is the end of the reply the first began, ended why.
func replyEnded(t *testing.T, got []Reply, why string) {
	t.Helper()
	if len(got) != 2 {
		t.Fatalf("told %d times over a reply %s, want its start and its end: %+v", len(got), why, got)
	}
	end := got[1]
	if end.Why != why || end.Ended.IsZero() || !end.Began.Equal(got[0].Began) || end.Ended.Before(end.Began) {
		t.Errorf("the end told is %+v, want the reply begun at %v, %s", end, got[0].Began, why)
	}
}

// A reply is told once as its audio starts and once as its turn ends, with how it ended: said to its
// end here.
func TestAReplyIsToldAsItStartsAndAsItEnds(t *testing.T) {
	got := told(t)
	c := playingReply(t, got)

	// Its evPlaying handled twice is still one start.
	c.handle(event{kind: evPlaying, reply: c.reply.id})
	if len(*got) != 1 {
		t.Fatalf("told %+v for one reply's audio, want one start", *got)
	}

	c.handle(next(t, c, evPlayed))
	replyEnded(t, *got, "spoken")
}

// A reply that is canceled (the stop word, the action button) is told as ended, canceled.
func TestACanceledReplyIsToldAsEnded(t *testing.T) {
	got := told(t)
	c := playingReply(t, got)
	c.handle(event{kind: evCancel})
	replyEnded(t, *got, "canceled")
}

// A reply a wake word interrupts is told as ended, interrupted.
func TestAnInterruptedReplyIsToldAsEnded(t *testing.T) {
	got := told(t)
	c := playingReply(t, got)
	c.handle(event{kind: evStart, slot: 0, woke: true, at: time.Now()})
	replyEnded(t, *got, "interrupted")
}

// A reply an announcement takes the speaker from is told as ended, cut off.
func TestACutOffReplyIsToldAsEnded(t *testing.T) {
	got := told(t)
	c := playingReply(t, got)
	news, release := heldSpeech(t)
	close(release)
	c.announce(esphome.Announce{MediaID: news})
	end := time.Now().Add(5 * time.Second)
	for c.phase != phaseIdle && time.Now().Before(end) {
		settle(c, 10*time.Millisecond)
	}
	replyEnded(t, *got, "cut off")
}

// A reply stopped while its url is still being fetched had no audio, and is not told at all: there was
// nothing to talk over.
func TestAReplyStoppedBeforeItsAudioIsNotTold(t *testing.T) {
	got := told(t)
	c := thinkingTurn(t)
	url, _ := heldSpeech(t) // never released: Home Assistant still synthesizing it
	c.handle(event{kind: evReplyText, text: "It's noon."})
	c.handle(event{kind: evReplyURL, url: url})
	settle(c, 100*time.Millisecond)
	if c.phase != phaseReplying {
		t.Fatalf("the turn is %s, want replying with the reply's audio still to come", c.phase)
	}
	c.handle(event{kind: evCancel})
	settle(c, 100*time.Millisecond)
	if len(*got) != 0 {
		t.Errorf("told %+v for a reply stopped before its audio", *got)
	}
}

// streamedReply is a turn whose streamed reply has its first audio, the start told.
func streamedReply(t *testing.T, got *[]Reply) *conversation {
	t.Helper()
	c := thinkingTurn(t)
	c.handle(event{kind: evReplyText, text: "It's noon."})
	c.handle(event{kind: evStreamAudio, audio: make([]byte, 1024)})
	if c.phase != phaseReplying {
		t.Fatalf("the streamed audio left the turn %s, want replying", c.phase)
	}
	c.handle(next(t, c, evPlaying))
	if len(*got) != 1 {
		t.Fatalf("told %+v as the streamed audio started, want one start", *got)
	}
	return c
}

// A turn that speaks twice, its streamed reply taken over by the url that came after it, is one reply:
// told one start, as the streamed audio came, and one end.
func TestATurnThatSpeaksTwiceIsToldOneStartAndOneEnd(t *testing.T) {
	got := told(t)
	c := streamedReply(t, got)

	url, release := heldSpeech(t)
	close(release)
	c.handle(event{kind: evReplyURL, url: url})
	c.handle(next(t, c, evPlaying))
	if len(*got) != 1 {
		t.Fatalf("told %+v as the url's audio started, want only the streamed audio's start", *got)
	}

	c.handle(next(t, c, evPlayed))
	replyEnded(t, *got, "spoken")
}

// A streamed reply taken over by its url had audio, so a turn stopped before the url's audio came is
// still told as ended.
func TestAReplyTakenOverAndStoppedBeforeTheUrlsAudioIsToldAsEnded(t *testing.T) {
	got := told(t)
	c := streamedReply(t, got)

	url, _ := heldSpeech(t) // never released: Home Assistant still synthesizing it
	c.handle(event{kind: evReplyURL, url: url})
	settle(c, 100*time.Millisecond)
	if c.phase != phaseReplying {
		t.Fatalf("the turn is %s, want replying with the url's audio still to come", c.phase)
	}
	c.handle(event{kind: evCancel})
	replyEnded(t, *got, "canceled")
}
