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
func (c *conversation) playElsewhere(ctx context.Context, url string, playing func()) bool {
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
	// The length is what the turn waits for. A reply that cannot be fetched here fails the same way on
	// the speaker, which reports it.
	samples, err := media.Fetch(ctx, url)
	if err != nil {
		return false
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
	failedElsewhere.Store(false)
	slog.Info("reply sent to another player", "player", entity, "samples", len(samples))
	if playing != nil {
		playing()
	}
	wait := time.Duration(len(samples))*time.Second/speaker.VoiceRate + elsewhereMargin
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
	return true
}
