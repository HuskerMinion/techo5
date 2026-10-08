//go:build !dot

package setup

import (
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/video"
)

// videoSection is the video player on the Screen & Photos tab: the two switches, and the addresses
// DLNA videos were allowed from, which can be forgotten.
func videoSection(w http.ResponseWriter, token string) {
	c := config.Get().Video
	checked := func(on bool) string {
		if on {
			return " checked"
		}
		return ""
	}
	fmt.Fprint(w, `<fieldset><legend>Video</legend><form method="post" action="/setup/save">`)
	hidden(w, token, "video", "photos")
	fmt.Fprint(w, `<p class="note" style="margin-top:0">Play a video full screen, with its sound, from an
	  address on your network or the internet. Home Assistant sends one with the play_video action.
	  H.264 at 720p or less plays best.</p>`)
	if !video.Installed() {
		fmt.Fprint(w, `<p class="note"><strong>This device's software has no video player yet.</strong> An
		  update brings it.</p>`)
	}
	fmt.Fprintf(w, `
	 <p><label><input type="checkbox" name="on" value="yes" style="width:auto"%s> Play videos: from Home Assistant, and
	  from DLNA when the next box is ticked too</label></p>
	 <p><label><input type="checkbox" name="dlna" value="yes" style="width:auto"%s> DLNA video: from apps and media
	  servers too (BubbleUPnP, Jellyfin, Windows Cast to device), while DLNA is on under Sound</label></p>
	 <p class="note">Anyone on your network can send a DLNA video, so the first one from each address asks on
	  the screen first.</p>`, checked(c.On), checked(c.DLNA))
	now := time.Now()
	var live []config.AllowedAddr
	for _, a := range c.Allowed {
		if c.IsAllowed(a.Addr, now) {
			live = append(live, a)
		}
	}
	if n := len(live); n > 0 {
		fmt.Fprint(w, `<p class="note">Allowed to send videos, each for 30 days after it last sent one:</p><ul class="note">`)
		for i := len(live) - 1; i >= 0; i-- { // the most recent first
			fmt.Fprintf(w, `<li>%s, last used %s</li>`, html.EscapeString(live[i].Addr), live[i].Used.Local().Format("Jan 2, 2006"))
		}
		fmt.Fprintf(w, `</ul><p><label><input type="checkbox" name="forget" value="yes" style="width:auto"> Forget the %d %s
		 allowed to send videos, so each asks again</label></p>`, n, plural(n, "address", "addresses"))
	}
	fmt.Fprint(w, `<p><button type="submit">Save</button></p></form></fieldset>`)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func saveVideo(r *http.Request) string {
	on := func(name string) bool { return r.PostFormValue(name) == "yes" }
	if on("on") != config.Get().Video.On {
		video.SetOn(on("on"))
	}
	if on("dlna") != config.Get().Video.DLNA {
		video.SetDLNA(on("dlna"))
	}
	if on("forget") {
		if err := config.Set().Video().ForgetAllowed(); err != nil {
			return "could not forget them"
		}
	}
	c := config.Get().Video
	if c.On != on("on") || c.DLNA != on("dlna") {
		return "could not save it"
	}
	slog.Info("setup page: video set", "on", c.On, "dlna", c.DLNA, "forgot", on("forget"))
	return ""
}
