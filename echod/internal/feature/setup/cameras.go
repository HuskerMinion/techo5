package setup

import (
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/doorbell"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// haCamerasSection is which of Home Assistant's cameras the device lists (the home_cameras action):
// None, All, or These. It says plainly which ones that is right now. A device never told lists All.
func haCamerasSection(w http.ResponseWriter, token string) {
	h := config.Get().Home
	mode, list := "these", h.Cameras
	switch {
	case h.NoHACameras():
		mode = "none"
	case h.AllHACameras():
		mode = "all"
		list = nil
		for _, c := range home.Get().Cameras() {
			if isHACamera(c.Entity) {
				list = append(list, c)
			}
		}
	}
	var names []string
	for _, c := range list {
		names = append(names, html.EscapeString(c.Name))
	}
	fmt.Fprint(w, `<fieldset><legend>Home Assistant cameras</legend><form method="post" action="/setup/save">`)
	hidden(w, token, "hacameras", "connections")
	var now string
	switch {
	case mode == "none":
		now = "none of Home Assistant's cameras."
	case mode == "all" && len(names) == 0:
		now = "every camera Home Assistant has, and it has listed none yet (it needs a Home Assistant token)."
	case mode == "all":
		now = fmt.Sprintf("every camera Home Assistant has: %d now, %s.", len(names), strings.Join(names, ", "))
	default:
		now = fmt.Sprintf("these %d: %s.", len(names), strings.Join(names, ", "))
	}
	fmt.Fprintf(w, `<p style="margin:0">This device lists %s</p>`, now)
	var lines []string
	for _, c := range list {
		lines = append(lines, c.Entity+"="+c.Name)
	}
	text := strings.Join(lines, "\n")
	fmt.Fprintf(w, `<p><label><input type="radio" name="mode" value="none"%s> None</label>
	 <label><input type="radio" name="mode" value="all"%s> All of Home Assistant's cameras</label>
	 <label><input type="radio" name="mode" value="these"%s> These:</label></p>
	 <input type="hidden" name="was_mode" value="%s">
	 <textarea id="hacams" name="list" rows="4" maxlength="4000" placeholder="camera.front_door=Front door">%s</textarea>
	 <input type="hidden" name="was_list" value="%s">
	 <p class="note">One camera a line, its entity and the name to say for it: camera.front_door=Front door.
	  The name can be left off. A device that shares a Home Assistant with other homes is best set to None
	  or a list, so it does not show the other homes' cameras. Cameras set up below (Reolink, go2rtc) and
	  the device's own camera are listed either way. An automation can still put any camera up with
	  home_show_camera.</p>
	 <p><button type="submit">Save</button></p></form></fieldset>`,
		checkedIf(mode == "none"), checkedIf(mode == "all"), checkedIf(mode == "these"), mode,
		html.EscapeString(text), html.EscapeString(text))
}

// isHACamera is whether a camera on the list is one of Home Assistant's: not the device's own, a
// Reolink recorder's or go2rtc's.
func isHACamera(entity string) bool {
	if entity == home.LocalCamera || home.IsGo2RTC(entity) {
		return false
	}
	_, _, _, onRecorder := home.ReolinkRTSP(entity)
	return !onRecorder
}

// cameraLines reads the These box: a camera a line (commas work too), as home_cameras reads its list.
func cameraLines(s string) ([]config.Camera, bool) {
	return home.ParseCameras(strings.NewReplacer("\r\n", ",", "\r", ",", "\n", ",").Replace(s))
}

func saveHACameras(r *http.Request) string {
	mode := r.PostFormValue("mode")
	cams, listNone := cameraLines(r.PostFormValue("list"))
	if was, _ := cameraLines(r.PostFormValue("was_list")); mode == r.PostFormValue("was_mode") && (mode != "these" || slices.Equal(cams, was)) {
		return "" // nothing changed
	}
	none := false
	switch mode {
	case "none":
		cams, none = nil, true
	case "all":
		cams = nil
	case "these":
		if listNone || len(cams) == 0 {
			return "list at least one camera under These, or choose None"
		}
	default:
		return "choose None, All or These"
	}
	if err := home.SetCameras(cams, none); err != nil {
		return "could not save the cameras: " + err.Error()
	}
	slog.Info("setup page: Home Assistant cameras set", "mode", mode, "count", len(cams))
	return ""
}

// reolinkSection is a Reolink NVR, Home Hub or camera read directly (feature/home/reolink.go). The
// password is never shown: it is written, or left alone.
func reolinkSection(w http.ResponseWriter, token string) {
	r := config.Get().Home.Reolink
	fmt.Fprint(w, `<fieldset><legend>Reolink cameras</legend><form method="post" action="/setup/save">`)
	hidden(w, token, "reolink", "connections")
	if r.Base != "" {
		var names []string
		for _, c := range r.Cameras {
			names = append(names, html.EscapeString(c.Name))
		}
		fmt.Fprintf(w, `<p style="margin:0">Set up: %d camera(s): %s. Say "show the" and a camera's name, or
		 swipe in from the right edge.</p>`, len(r.Cameras), strings.Join(names, ", "))
	}
	host := strings.TrimPrefix(strings.TrimPrefix(r.Base, "https://"), "http://")
	hint := "the recorder's password"
	if r.Pass != "" {
		hint = "set; leave empty to keep it"
	}
	fmt.Fprintf(w, `<label for="rlhost">NVR, Home Hub or camera address</label>
	 <input id="rlhost" name="host" value="%s" placeholder="192.168.1.30" autocomplete="off">
	 <label for="rluser">User</label>
	 <input id="rluser" name="user" value="%s" placeholder="admin" autocomplete="off">
	 <label for="rlpass">Password</label>
	 <input id="rlpass" name="pass" type="password" value="" placeholder="%s" autocomplete="off">
	 <p class="note">The device logs in to the recorder and lists its cameras by the names they have in the
	  Reolink app. A user of its own with only "view" rights is the tidy way. Clear the address and save
	  to take the cameras away.</p>
	 <p><button type="submit">Save</button></p></form></fieldset>`,
		html.EscapeString(host), html.EscapeString(r.User), hint)
}

func saveReolink(r *http.Request) string {
	host := strings.TrimSpace(r.PostFormValue("host"))
	user := strings.TrimSpace(r.PostFormValue("user"))
	if strings.ContainsAny(host+user+r.PostFormValue("pass"), "\r\n") {
		return "the address, user and password are one line each"
	}
	if host != "" && user == "" {
		user = "admin"
	}
	n, err := home.SetReolink(host, user, r.PostFormValue("pass"))
	if err != nil {
		return "could not set up the cameras: " + err.Error()
	}
	doorbell.Get().Poke() // its address and login are the recorder's
	slog.Info("setup page: Reolink cameras set", "cameras", n)
	return ""
}
