package home

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/component"
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
	}
	if !hass.Get().Ready() {
		resumeOverAPI()
		return
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

// resumeOverAPI asks Home Assistant to play Music Assistant's player for this device over the
// device's own connection to it, which needs no token: what most devices have (#485). Home Assistant
// finds the player itself, with a template doing what hass.Client.MusicAssistantFor does. It runs only
// when the device is allowed to perform Home Assistant actions (the ESPHome integration's option for
// it); otherwise Home Assistant says so in its own log and repairs.
func resumeOverAPI() {
	unheard := false
	component.CallService.Emit(component.Call{
		Service:   "media_player.media_play",
		Templates: map[string]string{"entity_id": maPlayerTemplate(speakerEntity(), config.Get().Device.Name)},
		Unheard:   func() { unheard = true },
	})
	if unheard {
		slog.Warn("resume: no Music Assistant token or Home Assistant access on this device, and Home Assistant is not connected")
		return
	}
	slog.Info("resume: asked Home Assistant to have Music Assistant play, over this device's connection")
}

// maPlayerTemplate is a Home Assistant template naming Music Assistant's player for the device whose
// own media player is own and whose name is name: the one whose active queue is own, or else the
// player of that name (a queue of that name only when nothing else is).
//
// own is skipped in the search, so a device whose own player is named for itself is not mistaken for
// Music Assistant's player of the same name. But where Home Assistant has given the clean entity id to
// Music Assistant's player and the device's own ESPHome entity a "_speaker" one, own is that Music
// Assistant player, and skipping it alone left nothing to resume (#485); so own stands as the last
// resort when it is itself a Music Assistant player of this name. An empty answer renders as 'none', a
// media_player target Home Assistant takes as nothing rather than rejecting the call.
func maPlayerTemplate(own, name string) string {
	q := func(s string) string { return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'" }
	return "{%- set own = " + q(own) + " -%}{%- set name = " + q(strings.ToLower(strings.TrimSpace(name))) + " -%}" +
		"{%- set ns = namespace(q='', p='', n='', o='') -%}" +
		"{%- for s in states.media_player if s.attributes.app_id == 'music_assistant' -%}" +
		"{%- if s.entity_id == own -%}" +
		"{%- if name != '' and (s.name | lower | trim) == name and s.attributes.mass_player_type == 'player' -%}{%- set ns.o = s.entity_id -%}{%- endif -%}" +
		"{%- elif s.attributes.active_queue == own -%}{%- set ns.q = s.entity_id -%}" +
		"{%- elif name != '' and (s.name | lower | trim) == name -%}" +
		"{%- if s.attributes.mass_player_type == 'player' -%}{%- set ns.p = s.entity_id -%}" +
		"{%- elif s.attributes.mass_player_type != 'queue' -%}{%- set ns.n = s.entity_id -%}{%- endif -%}" +
		"{%- endif -%}{%- endfor -%}{{ ns.q or ns.p or ns.n or ns.o or 'none' }}"
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

// MusicAssistantPlayer is Music Assistant's own player for this device (musicAssistantPlayer), for the
// screen's music library to play on when no player of the same name is found; empty when there is none.
func MusicAssistantPlayer() string {
	id, _ := musicAssistantPlayer()
	return id
}

// musicAssistantPlayer is Music Assistant's own player for this device, empty when there is none. It is
// found by the name this device calls itself, which is the name it announces to Music Assistant over
// Sendspin and so the name Music Assistant gives its player; see hass.Client.MusicAssistantFor.
func musicAssistantPlayer() (string, error) {
	return hass.Get().MusicAssistantFor(speakerEntity(), config.Get().Device.Name)
}
