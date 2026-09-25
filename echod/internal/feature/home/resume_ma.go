package home

import (
	"log/slog"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// resumeMusicAssistant asks Music Assistant to play again what it was playing here, the way its own
// app does.
//
// Asking over Sendspin does not work: a pause ends Music Assistant's stream to this device, and a play
// from the player it ended goes to that player alone, which has no queue of its own, so nothing
// happens. Music Assistant's own entity for this device resumes the queue where it was. It is the
// media player Music Assistant runs whose active queue is this device's own media player.
func resumeMusicAssistant() {
	ma, err := musicAssistantPlayer()
	if err != nil || ma == "" {
		slog.Warn("resume: no Music Assistant player found for this device", "err", err)
		return
	}
	if err := hass.Get().MediaPlay(ma); err != nil {
		slog.Warn("resume: asking Music Assistant to play failed", "player", ma, "err", err)
		return
	}
	slog.Info("resume: asked Music Assistant to play", "player", ma)
}

// musicAssistantPlayer is Music Assistant's own player for this device, empty when there is none. It is
// found by the name this device calls itself, which is the name it announces to Music Assistant over
// Sendspin and so the name Music Assistant gives its player; see hass.Client.MusicAssistantFor.
func musicAssistantPlayer() (string, error) {
	return hass.Get().MusicAssistantFor(speakerEntity(), config.Get().Device.Name)
}
