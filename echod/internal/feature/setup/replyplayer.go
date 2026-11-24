package setup

import (
	"fmt"
	"html"
	"log/slog"
	"net/http"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// replyPlayerSection is where the assistant's spoken replies play: this device, or another Home
// Assistant media player.
func replyPlayerSection(w http.ResponseWriter, token string) {
	sectionOpen(w, "replyplayer", "Replies play on", replyPlayerSummary(config.Get().Brain.ReplyPlayer)...)
	fmt.Fprint(w, `<form method="post" action="/setup/save">`)
	hidden(w, token, "replyplayer")
	fmt.Fprintf(w, `<label for="player">Media player</label>
	 <input id="player" name="player" value="%s" maxlength="100" placeholder="media_player.kitchen_dot" autocomplete="off">
	 <p class="note">Leave it empty to hear replies from this device. Type a Home Assistant media player,
	  like <code>media_player.kitchen_dot</code>, and the replies Home Assistant sends play there instead,
	  as an announcement: music there is turned down and carries on afterwards.</p>
	 <p class="note">Only replies from Home Assistant's voice assistant can go. The built-in assistant and
	  realtime voice stay on this device. So do its own sounds: the wake sound, timers and alarms.</p>
	 <p class="note">This device cannot hear when the other speaker finishes, so it listens again after
	  the length of the reply plus a couple of seconds. If the player is in the same room, turn
	  continued conversation off. A player that does not answer is skipped for that reply, and it plays
	  here.</p>
	 <p><button type="submit">Save</button></p></form>`, html.EscapeString(config.Get().Brain.ReplyPlayer))
	sectionClose(w)
}

// replyPlayerSummary is the line on the shut card: this device, or the player the replies go to.
func replyPlayerSummary(player string) []string {
	if player == "" {
		return []string{"This device"}
	}
	return []string{player}
}

// saveReplyPlayer keeps the player, or says why it is not one.
//
// Choosing a player where there was none, or clearing it, changes what the device tells Home Assistant
// it is (voice.FeaturesFor, media.FormatsFor), so that replies sent elsewhere are made at full quality
// (#715). Home Assistant reads both only as it connects, so a reconnect follows once the device is
// idle (reconnect.go). One player for another changes neither, and needs none.
func saveReplyPlayer(v string) string {
	was := config.Get().Brain.RepliesElsewhere()
	if err := config.Set().Brain().SetReplyPlayer(v); err != nil {
		return err.Error()
	}
	elsewhere := config.Get().Brain.RepliesElsewhere()
	slog.Info("setup page: where replies play was set", "elsewhere", elsewhere)
	if elsewhere != was {
		whenIdle.ask()
	}
	return ""
}
