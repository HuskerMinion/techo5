package voice

import (
	"context"
	"errors"
	"log/slog"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// announce plays what Home Assistant asks for, then reports back. It runs off the connection's
// read loop: fetching and playing must not block it.
func (t *conversation) announce(a esphome.Announce) {
	slog.Info("announce", "text", a.Text, "start_conversation", a.StartConversation)
	// One that asks for an answer is a question to whoever is here: its words go on the screen.
	asks := a.StartConversation && a.Text != ""
	if asks {
		t.post(event{kind: evAnnouncing, text: a.Text})
	}

	// One claim covers both urls, so silencing an announcement stops the whole thing rather than
	// letting the second one start once the first has been drained.
	t.player.Sounding(true)
	claim := t.sound.ClaimSpeech("announce", func(ctx context.Context, _ *speaker.Player) error {
		for _, url := range []string{a.PreannounceMediaID, a.MediaID} {
			if url == "" {
				continue
			}
			if err := t.play(ctx, url, nil, true); err != nil {
				return err
			}
		}
		return nil
	})

	safe.Go("announce", func() {
		<-claim.Done()
		t.player.Sounding(false)

		if err := claim.Err(); err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("playing the announcement failed", "err", err) // a stop is not a failure
		}
		if err := t.vs.AnnounceFinished(claim.Err() == nil && !claim.Stopped()); err != nil {
			slog.Error("reporting the announcement failed", "err", err)
		}

		// Home Assistant uses this to open a conversation without a wake word. An announcement that
		// was cut off was cut off on purpose, so nothing follows it.
		if a.StartConversation && !claim.Stopped() {
			t.post(event{kind: evStart, slot: 0, asked: true, at: time.Now()})
		} else if asks {
			t.post(event{kind: evAnnounced})
		}
	})
}

// play fetches audio and plays it as it arrives (#559), through the same cushion and running speech
// gain as a streamed reply. Home Assistant serves it converted to media.Formats; one that is not a WAVE
// body (MP3) is read whole first. Waiting for it to be heard is the driver's, not ours: this returns as
// soon as the last of it has been handed over. playing is told just before the first audio is queued,
// which is a reply's evPlaying; an announcement has none, since it is not what the turn's deadline is
// waiting for. intoCall sends it into a call that is up as well, piece by piece as the room hears it:
// always for an announcement, and for a reply when Assistant replies go into calls is on.
func (t *conversation) play(ctx context.Context, url string, playing func(), intoCall bool) error {
	s := &media.SpeechStream{Buffer: media.AnnounceBuffer, Gain: true, Louder: true, Started: playing}
	// An announcement made while a call is up is for the call as well, which is how an automation
	// tells whoever answered a call it placed what the call is about.
	if intoCall {
		said := false
		s.Say = func(out []int16) {
			// Waits for room: the pieces come as fast as the network brings them, faster than the call
			// sends them, and Say would drop what does not fit.
			if phone.Get().SayWithin(out, 2*time.Second) && !said {
				said = true
				slog.Info("sent into the call", "reply", playing != nil)
			}
		}
	}
	slog.Info("playing announcement", "reply", playing != nil) // not the address: it carries a token
	return media.PlaySpeech(ctx, url, s, t.speaker)
}
