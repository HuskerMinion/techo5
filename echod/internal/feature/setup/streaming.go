package setup

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/dlna"
	"github.com/HuskerMinion/techo5/echod/internal/feature/streaming"
)

// streamingSection is the device as a speaker other apps play to, under its own name: AirPlay and
// Spotify Connect where the device has them (feature/streaming), and DLNA on every device
// (feature/dlna).
func streamingSection(w http.ResponseWriter, token string) {
	c := config.Get().Streaming
	checked := func(on bool) string {
		if on {
			return " checked"
		}
		return ""
	}
	legend := "Play to this device"
	fmt.Fprintf(w, `<fieldset><legend>%s</legend><form method="post" action="/setup/save">`, legend)
	hidden(w, token, "streaming", "sound")
	fmt.Fprint(w, `<p class="note" style="margin-top:0">Play to this device from other apps. It shows up under its
	  own name, on the same network.</p>`)
	if streaming.Here {
		fmt.Fprintf(w, `
	 <p><label><input type="checkbox" name="airplay" value="yes" style="width:auto"%s> AirPlay: from an iPhone, iPad or Mac</label></p>
	 <p><label><input type="checkbox" name="spotify" value="yes" style="width:auto"%s> Spotify Connect: from the Spotify app (needs Spotify Premium)</label></p>`,
			checked(c.AirPlay), checked(c.Spotify))
	}
	fmt.Fprintf(w, `
	 <p><label><input type="checkbox" name="dlna" value="yes" style="width:auto"%s> DLNA: from music apps and servers
	  (BubbleUPnP, Jellyfin, Plex, a NAS); MP3, FLAC and WAV</label></p>
	 <p class="note">Anyone on the same network can play to it while one is on, as with any speaker of that kind.`, checked(c.DLNA))
	if streaming.Here {
		fmt.Fprint(w, ` Spotify Connect keeps the login a phone hands it until Spotify Connect is turned off.`)
	}
	fmt.Fprint(w, `</p>
	 <p class="note">New, and not yet tried with every app: if something does not work, say so on GitHub.</p>
	 <p><button type="submit">Save</button></p></form></fieldset>`)
}

func saveStreaming(r *http.Request) string {
	on := func(name string) bool { return r.PostFormValue(name) == "yes" }
	if streaming.Here {
		if on("airplay") != config.Get().Streaming.AirPlay {
			streaming.Get().SetAirPlay(on("airplay"))
		}
		if on("spotify") != config.Get().Streaming.Spotify {
			streaming.Get().SetSpotify(on("spotify"))
		}
		if config.Get().Streaming.AirPlay != on("airplay") || config.Get().Streaming.Spotify != on("spotify") {
			return "could not save it"
		}
	}
	if on("dlna") != config.Get().Streaming.DLNA {
		dlna.Get().Set(on("dlna"))
	}
	if config.Get().Streaming.DLNA != on("dlna") {
		return "could not save it"
	}
	slog.Info("setup page: streaming set", "airplay", on("airplay"), "spotify", on("spotify"), "dlna", on("dlna"))
	return ""
}
