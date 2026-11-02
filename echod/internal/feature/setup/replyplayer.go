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
	fmt.Fprint(w, `<fieldset><legend>Replies play on</legend><form method="post" action="/setup/save">`)
	hidden(w, token, "replyplayer", "sound")
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
	 <p><button type="submit">Save</button></p></form></fieldset>`, html.EscapeString(config.Get().Brain.ReplyPlayer))
}

// saveReplyPlayer keeps the player, or says why it is not one.
func saveReplyPlayer(v string) string {
	if err := config.Set().Brain().SetReplyPlayer(v); err != nil {
		return err.Error()
	}
	slog.Info("setup page: where replies play was set", "elsewhere", config.Get().Brain.ReplyPlayer != "")
	return ""
}
