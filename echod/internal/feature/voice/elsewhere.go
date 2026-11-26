package voice

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// Replies play on: a Home Assistant media player can say the assistant's replies instead of this
// device's speaker (config.Brain.ReplyPlayer).
//
// Only a reply that is a url can go: Home Assistant's pipeline sends one with every spoken reply. The
// direct assistant and realtime voice make their audio here, as a stream with no address, so they stay
// on this device. This device's own sounds (wake sound, timers, alarms, announcements) always do.

// callPlayer asks Home Assistant to play url on entity. announce has music there lower and carry on
// afterwards. It is a variable so a test can stand in for Home Assistant.
var callPlayer = func(entity, url string) error {
	return hass.Get().Call("media_player", "play_media", map[string]any{
		"entity_id":          entity,
		"media_content_id":   url,
		"media_content_type": "music",
		"announce":           true,
	})
}

// stopPlayer asks Home Assistant to stop entity. A variable for the tests, as callPlayer is.
var stopPlayer = func(entity string) error {
	return hass.Get().Call("media_player", "media_stop", map[string]any{"entity_id": entity})
}

// playerUp is whether the player is there to take a reply. Home Assistant answers a play_media call to
// an unavailable player without an error and plays nothing, so the state is asked first. A state that
// cannot be read is taken as up: the call then says for itself. A player Home Assistant does not have at
// all, a name mistyped, is not up: the call to it would be answered without an error too.
var playerUp = func(entity string) bool {
	s, err := hass.Get().State(entity)
	if errors.Is(err, hass.ErrNotFound) {
		return false
	}
	if err != nil {
		return true
	}
	return s.State != "unavailable" && s.State != "unknown"
}

// elsewhereMargin is added to the length of the reply before the turn counts it as heard: the player
// takes a moment to start. The device cannot hear when another speaker finishes, so a turn that goes
// on to listen again does so after this estimate, not after a signal.
var elsewhereMargin = 2 * time.Second

// failedElsewhere is whether the last reply could not be sent to the player, so the log says it once
// per run of failures rather than once per reply.
var failedElsewhere atomic.Bool

// replyPlayer is the entity replies play on, empty for this device's speaker.
func replyPlayer() string { return config.Get().Brain.ReplyPlayer }

// playElsewhere plays the reply at url on the chosen player, and returns once it should have been
// heard. It reports false when the reply is to play here instead: no player chosen, no url, or the
// player did not take it, which the caller then plays on this device so the reply is not lost.
//
// early is a reply Home Assistant is still writing (Reply delivery: Early, #707). The player is handed
// its url at once, as this device's own speaker would start on it, and fetches it as it is written, so
// a long answer starts with its first sentence rather than once the agent has finished and the whole
// reply has been downloaded. The turn still needs the reply's length, to know when it has been heard
// and the follow-up can listen, so this device fetches the same url alongside the player and counts
// the audio as it comes in: Home Assistant serves a reply still being written to more than one reader.
// Otherwise the reply is fetched whole first, as it always was: a reply that cannot be fetched here
// fails the same way on the speaker, which reports it, rather than being handed to a player that
// would play nothing.
func (c *conversation) playElsewhere(ctx context.Context, id uint64, url string, playing func(), early bool) bool {
	entity := replyPlayer()
	if entity == "" || url == "" {
		return false
	}
	if !playerUp(entity) {
		if !failedElsewhere.Swap(true) {
			slog.Error("replies: the player is unavailable, playing the reply here", "player", entity)
		}
		return false
	}
	var samples int
	if !early {
		whole, err := media.Fetch(ctx, url)
		if err != nil {
			return false
		}
		samples = len(whole)
	}
	if err := callPlayer(entity, url); err != nil {
		if ctx.Err() != nil {
			return true
		}
		if !failedElsewhere.Swap(true) {
			slog.Error("replies: the player did not take the reply, playing it here", "player", entity, "err", err)
		}
		return false
	}
	// The player starts about now, so the reply's length is counted from here, not from when this
	// device's own fetch of an early reply finishes: that ends as the agent stops writing, which is
	// well before the player has said it all when the agent writes faster than it is spoken.
	started := time.Now()
	failedElsewhere.Store(false)
	c.elsewhere.Store(id) // which reply: one given up on that gets here late marks only itself
	if early {
		slog.Info("reply sent to another player as it is written", "player", entity)
	} else {
		slog.Info("reply sent to another player", "player", entity, "samples", samples)
	}
	if playing != nil {
		playing()
	}
	if early {
		// FetchVoice hands on 16-bit samples, two bytes each, at speaker.VoiceRate, the same as the
		// whole fetch, and has the patience for an agent still writing that a whole fetch does not.
		// The player already has the reply, so a fetch that fails here does not send it here as
		// well: the turn waits for what did arrive and goes on.
		var bytes int
		err := media.FetchVoice(ctx, url, func(b []byte) { bytes += len(b) })
		if ctx.Err() != nil {
			stopElsewhere(entity)
			return true
		}
		if err != nil {
			slog.Warn("replies: could not follow the reply's length, listening again sooner", "player", entity, "err", err)
		}
		samples = bytes / 2
		slog.Info("reply on another player written", "player", entity, "samples", samples)
	}
	wait := time.Until(started.Add(time.Duration(samples)*time.Second/speaker.VoiceRate + elsewhereMargin))
	if wait <= 0 {
		return true
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
		if early {
			stopElsewhere(entity)
		}
	}
	return true
}

// stopElsewhere stops a reply handed to the player early, when the turn is stopped (the stop word, a
// cancel, a new wake word): the player was given it as the agent began to write, so without this a long
// answer somebody asked to stop would be read out there to the end. A reply sent whole was only handed
// over once written, and stopped before that, as it always was.
func stopElsewhere(entity string) {
	if err := stopPlayer(entity); err != nil {
		slog.Warn("replies: could not stop the reply on another player", "player", entity, "err", err)
		return
	}
	slog.Info("reply on another player stopped", "player", entity)
}
