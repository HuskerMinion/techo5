package setup

import (
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"

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
	sectionOpen(w, "streaming", "Play to this device", streamingSummary(c, streaming.Here)...)
	fmt.Fprint(w, `<form method="post" action="/setup/save">`)
	hidden(w, token, "streaming")
	fmt.Fprint(w, `<p class="note" style="margin-top:0">Play to this device from other apps. It shows up under its
	  own name, on the same network.</p>`)
	if streaming.Here {
		fmt.Fprintf(w, `
	 <p><label><input type="checkbox" name="airplay" value="yes" style="width:auto"%s> AirPlay: from an iPhone, iPad or Mac</label></p>
	 <p><label><input type="checkbox" name="spotify" value="yes" style="width:auto"%s> Spotify Connect: from the Spotify app (needs Spotify Premium)</label></p>`,
			checked(c.AirPlay), checked(c.Spotify))
		// Quality and Normalize do nothing without Spotify Connect: the page leaves them out while its
		// box is not ticked (.spotifyonly, tabs.go).
		fmt.Fprint(w, `
	 <div class="spotifyonly"><p><label for="spotifybitrate">Spotify Connect quality</label><select id="spotifybitrate" name="spotifybitrate">`)
		for _, b := range []int{96, 160, 320} {
			sel := ""
			if b == c.SpotifyKbps() {
				sel = " selected"
			}
			fmt.Fprintf(w, `<option value="%d"%s>%s</option>`, b, sel, spotifyQuality(b))
		}
		fmt.Fprintf(w, `</select></p>
	 <p><label><input type="checkbox" name="spotifynormalize" value="yes" style="width:auto"%s> Spotify Connect evens
	  out the volume between songs</label></p>
	 <p class="note">As the Spotify app's own Normalize volume: quiet and loud songs play at about the same loudness,
	  and an album played in order keeps its own. Changing either starts Spotify Connect again for a moment.</p></div>`,
			checked(c.SpotifyNormalize))
		if streaming.Has2() {
			fmt.Fprintf(w, `
	 <p><label><input type="checkbox" name="airplay2" value="yes" style="width:auto"%s> AirPlay 2: group it with
	  other AirPlay 2 speakers; off keeps classic AirPlay, which plays on its own</label></p>`, checked(!c.AirPlayClassic))
		}
		fmt.Fprintf(w, `
	 <p><label><input type="checkbox" name="airplayvolume" value="yes" style="width:auto"%s> AirPlay sets the volume</label></p>
	 <p class="note">On, the volume slider on an iPhone, iPad or Mac playing to this device moves this device's
	  volume, the one Home Assistant shows, and nothing is scaled twice. The slider does not follow the device's
	  volume buttons or Home Assistant: it stays where the phone left it. Off, as it comes, the slider only makes
	  the stream quieter and this device keeps its own volume. Changing it restarts AirPlay for a moment.</p>`, checked(c.AirPlaySetsVolume))
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
	 <p><button type="submit">Save</button></p></form>`)
	sectionClose(w)
}

// streamingSummary is the line on the shut card: what other apps can play to this device with, or Off.
// AirPlay and Spotify Connect only where the device has them (here).
func streamingSummary(c config.Streaming, here bool) []string {
	var on []string
	if here && c.AirPlay {
		on = append(on, "AirPlay")
	}
	if here && c.Spotify {
		on = append(on, "Spotify Connect")
	}
	if c.DLNA {
		on = append(on, "DLNA")
	}
	if len(on) == 0 {
		return []string{"Off"}
	}
	return []string{strings.Join(on, ", ")}
}

// spotifyQuality names a Spotify Connect quality as the Spotify app does.
func spotifyQuality(kbps int) string {
	switch kbps {
	case 96:
		return "Normal (96 kbit/s)"
	case 320:
		return "Very high (320 kbit/s)"
	}
	return "High (160 kbit/s, as it comes)"
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
		kbps, _ := strconv.Atoi(r.PostFormValue("spotifybitrate"))
		if !slices.Contains(config.SpotifyBitrates, kbps) {
			kbps = config.Get().Streaming.SpotifyKbps()
		}
		if s := config.Get().Streaming; kbps != s.SpotifyKbps() || on("spotifynormalize") != s.SpotifyNormalize {
			if err := streaming.Get().SetSpotifyChoices(kbps, on("spotifynormalize")); err != nil {
				slog.Error("setup page: saving Spotify Connect's choices failed", "err", err)
				return "could not save it"
			}
		}
		if streaming.Has2() && on("airplay2") == config.Get().Streaming.AirPlayClassic {
			streaming.Get().SetAirPlay2(on("airplay2"))
		}
		if on("airplayvolume") != config.Get().Streaming.AirPlaySetsVolume {
			streaming.Get().SetAirPlaySetsVolume(on("airplayvolume"))
		}
		if config.Get().Streaming.AirPlaySetsVolume != on("airplayvolume") ||
			config.Get().Streaming.AirPlay != on("airplay") || config.Get().Streaming.Spotify != on("spotify") ||
			streaming.Has2() && config.Get().Streaming.AirPlayClassic == on("airplay2") {
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
