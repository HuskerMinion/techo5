package home

import (
	"context"
	"log/slog"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
	"github.com/HuskerMinion/techo5/echod/internal/lib/musicassistant"
)

// resumeMusicAssistant asks Music Assistant to play again what it was playing here, the way its own
// app does.
//
// Asking over Sendspin does not work: a pause ends Music Assistant's stream to this device, and a play
// from the player it ended goes to that player alone, which has no queue of its own, so nothing
// happens. Music Assistant resumes the queue where it was: asked directly, with the token the device
// was given for its music library, or, without one, through Home Assistant's entity for it (#485: most
// devices have no Home Assistant token, and that way alone left play doing nothing).
func resumeMusicAssistant() {
	if m := config.Get().MusicAssistant; m.Set() {
		err := resumeDirect(musicassistant.Client{URL: m.URL, Token: m.Token})
		if err == nil {
			slog.Info("resume: asked Music Assistant to play")
			return
		}
		slog.Warn("resume: asking Music Assistant to play failed", "err", err)
		if !hass.Get().Ready() {
			return
		}
	}
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

// resumeDirect resumes this device's own player at the server, found by its id there (the factory MAC,
// as the voice assistant finds it). A variable for the tests.
var resumeDirect = func(c musicassistant.Client) error {
	own, err := layout.FactoryMAC()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	p, err := c.Player(ctx, own)
	if err != nil {
		return err
	}
	return c.Resume(ctx, p.ID)
}

// musicAssistantPlayer is Music Assistant's own player for this device, empty when there is none. It is
// found by the name this device calls itself, which is the name it announces to Music Assistant over
// Sendspin and so the name Music Assistant gives its player; see hass.Client.MusicAssistantFor.
func musicAssistantPlayer() (string, error) {
	return hass.Get().MusicAssistantFor(speakerEntity(), config.Get().Device.Name)
}
