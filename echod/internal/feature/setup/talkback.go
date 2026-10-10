package setup

import (
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/talkback"
	"github.com/HuskerMinion/techo5/echod/internal/feature/video"
)

// talkBackSection is where each camera is talked to (feature/talkback): an RTSP address per camera on
// the list, and the login they share. A camera on the Reolink recorder above needs nothing here. The
// password is never shown: it is written, or left alone.
func talkBackSection(w http.ResponseWriter, token string) {
	if !talkback.Here {
		return
	}
	c := config.Get()
	fmt.Fprint(w, `<fieldset><legend>Talk through cameras</legend><form method="post" action="/setup/save">`)
	hidden(w, token, "talkback", "connections")
	state := "off: turn on Talk through cameras under Privacy &amp; Security, on the device or in Home Assistant"
	if c.Security.TalkBack {
		state = "on"
	}
	fmt.Fprintf(w, `<p class="note" style="margin-top:0">Talk on the camera page sends this device's microphones
	  to the camera's own speaker, straight over its RTSP stream, for cameras that have a speaker and ONVIF
	  two-way audio (most Reolinks do). The switch is %s.</p>`, state)

	var found []string
	for _, cam := range c.Home.Reolink.Cameras {
		found = append(found, html.EscapeString(cam.Name))
	}
	if len(found) > 0 {
		fmt.Fprintf(w, `<p class="note">From the Reolink recorder, with nothing to set: %s.</p>`, strings.Join(found, ", "))
	}

	hint := "the cameras' password"
	if c.TalkBack.Pass != "" {
		hint = "set; leave empty to keep it"
	}
	fmt.Fprintf(w, `<label for="tbuser">User</label>
	 <input id="tbuser" name="user" value="%s" placeholder="admin" autocomplete="off">
	 <label for="tbpass">Password</label>
	 <input id="tbpass" name="pass" type="password" value="" placeholder="%s" autocomplete="new-password">`,
		html.EscapeString(c.TalkBack.User), hint)

	cams := talkBackCameras()
	for i, cam := range cams {
		fmt.Fprintf(w, `<label for="tbaddr%d">%s</label>
		 <input type="hidden" name="entity" value="%s">
		 <input id="tbaddr%d" name="addr" value="%s" placeholder="rtsp://192.168.1.40:554/h264Preview_01_main" autocomplete="off">`,
			i, html.EscapeString(cam.Name), html.EscapeString(cam.Entity), i, html.EscapeString(c.TalkBack.Cameras[cam.Entity]))
		if video.CanRTSP() {
			fmt.Fprintf(w, `<label for="tbpic%d">%s: picture (optional)</label>
			 <input id="tbpic%d" name="pic" value="%s" placeholder="rtsp://192.168.1.40:554/h264Preview_01_sub" autocomplete="off">`,
				i, html.EscapeString(cam.Name), i, html.EscapeString(c.TalkBack.Pictures[cam.Entity]))
		}
	}
	if len(cams) == 0 {
		fmt.Fprint(w, `<p class="note">No other cameras are on the list.</p>`)
	}
	fmt.Fprint(w, `<p class="note">A camera's address is its RTSP stream: for a Reolink,
	  rtsp://<i>address</i>:554/h264Preview_01_main, with RTSP turned on in the camera's network settings.
	  Leave it empty for a camera with no speaker, and it gets no Talk. Put the login in User and
	  Password, not in the address. The device only sends it protected (digest), never in the clear.</p>`)
	if video.CanRTSP() {
		fmt.Fprint(w, `<p class="note">A camera's picture, if given, is read from that address on the camera page
		  instead of Home Assistant's stream, which is seconds behind: a stream about the screen's size, for a
		  Reolink rtsp://<i>address</i>:554/h264Preview_01_sub. Its sound comes from the address above.</p>`)
	}
	fmt.Fprint(w, `<p><button type="submit">Save</button></p></form></fieldset>`)
}

// talkBackCameras are the cameras on the list that are given an address here: not the device's own, and
// not the Reolink recorder's, which have theirs.
func talkBackCameras() []config.Camera {
	var out []config.Camera
	for _, cam := range home.Get().Cameras() {
		if cam.Entity == home.LocalCamera {
			continue
		}
		if _, _, _, ok := home.ReolinkRTSP(cam.Entity); ok {
			continue
		}
		out = append(out, cam)
	}
	return out
}

func saveTalkBack(r *http.Request) string {
	user := strings.TrimSpace(r.PostFormValue("user"))
	pass := r.PostFormValue("pass")
	if len(user) > 128 || len(pass) > 128 {
		return "the user and password are 128 characters at most"
	}
	control := func(c rune) bool { return c < 0x20 || c == 0x7f }
	if strings.ContainsFunc(user+pass, control) || strings.ContainsAny(user, `"\`) {
		return "the user and password are one line each, and the user has no quotes or backslashes"
	}
	known := map[string]bool{}
	for _, cam := range talkBackCameras() {
		known[cam.Entity] = true
	}
	ents, addrs, pics := r.PostForm["entity"], r.PostForm["addr"], r.PostForm["pic"]
	if len(ents) != len(addrs) || (len(pics) != 0 && len(pics) != len(ents)) {
		return "the form came back incomplete; reload the page and try again"
	}
	kept, pictures := map[string]string{}, map[string]string{}
	for e, a := range config.Get().TalkBack.Pictures {
		if _, _, _, onRecorder := home.ReolinkRTSP(e); !known[e] && !onRecorder {
			pictures[e] = a
		}
	}
	// A camera no longer on the list keeps its address, for when it comes back; one now on the Reolink
	// recorder has the recorder's.
	for e, a := range config.Get().TalkBack.Cameras {
		if _, _, _, onRecorder := home.ReolinkRTSP(e); !known[e] && !onRecorder {
			kept[e] = a
		}
	}
	for i, e := range ents {
		if !known[e] {
			if strings.TrimSpace(addrs[i]) != "" {
				// Home Assistant's list is not to hand (it is offline, or the camera has left it):
				// saying so beats dropping the address that was typed.
				return "the camera list isn't available right now, so nothing was saved; try again in a minute"
			}
			continue
		}
		if len(pics) != 0 {
			p := strings.TrimSpace(pics[i])
			if why := rtspAddress(p); why != "" {
				return why
			}
			if p != "" {
				pictures[e] = p
			}
		}
		a := strings.TrimSpace(addrs[i])
		if why := rtspAddress(a); why != "" {
			return why
		}
		if a != "" {
			kept[e] = a
		}
	}
	var p *string
	if pass != "" {
		p = &pass
	}
	save := func() error { return config.Set().TalkBack().Save(user, p, kept) }
	if len(pics) != 0 {
		save = func() error { return config.Set().TalkBack().SaveWithPictures(user, p, kept, pictures) }
	}
	if err := save(); err != nil {
		return "could not save it: " + err.Error()
	}
	slog.Info("setup page: talk back set", "cameras", len(kept), "password", p != nil)
	return ""
}

// rtspAddress says what is wrong with a camera's address as typed: nothing for one that will do, or for
// none at all.
func rtspAddress(a string) string {
	if a == "" {
		return ""
	}
	u, err := url.Parse(a)
	if err != nil || u.Scheme != "rtsp" || u.Hostname() == "" || len(a) > 512 || strings.ContainsAny(a, "\r\n ") {
		return "a camera's address should be like rtsp://192.168.1.40:554/h264Preview_01_main"
	}
	if u.User != nil {
		return "put the login in User and Password, not in the address"
	}
	return ""
}
