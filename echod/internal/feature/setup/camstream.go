package setup

import (
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/camstream"
)

func init() { saves["camstream"] = saveCamStream }

// camStreamSection is the login for the camera over RTSP (feature/camstream), on a device with a camera.
// The password is never shown: it is written, or left alone.
func camStreamSection(w http.ResponseWriter, token string) {
	if !camstream.Offered() {
		return
	}
	c := config.Get().CamStream
	user, _ := c.Login()
	state := "off"
	switch {
	case c.On && c.Pass != "":
		state = "on"
	case c.On:
		state = "on, but waiting for a password"
	}
	hint := fmt.Sprintf("at least %d characters", camstream.MinPass)
	if c.Pass != "" {
		hint = "set; leave empty to keep it"
	}
	fmt.Fprint(w, `<fieldset><legend>Camera streaming</legend><form method="post" action="/setup/save">`)
	hidden(w, token, "camstream", "privacy")
	fmt.Fprintf(w, `<p style="margin:0">Camera streaming is %s. Turn it on or off with the Camera streaming
	 switch in Home Assistant.</p>
	 <label for="csuser">User</label>
	 <input id="csuser" name="user" value="%s" placeholder="%s" autocomplete="off">
	 <label for="cspass">Password</label>
	 <input id="cspass" name="pass" type="password" value="" placeholder="%s" autocomplete="new-password">
	 <p class="note">With it on, a recorder (Frigate, go2rtc, an NVR) can record this device's camera at
	  rtsp://<em>this device's address</em>:%s%s, with this login, over TCP. It never runs without a password;
	  turning it off, a new login and the lens cover cut viewers off, and the mute button stops the
	  picture. A Show shows a green dot while it is watched. Use a long password of its own. The picture
	  is a series of JPEGs, not H.264: a recorder that wants H.264 re-encodes it.</p>
	 <p><button type="submit">Save</button></p></form></fieldset>`,
		html.EscapeString(state), html.EscapeString(user), config.DefaultCamStreamUser, hint, camstream.Port, camstream.Path)
}

func saveCamStream(r *http.Request) string {
	user := strings.TrimSpace(r.PostFormValue("user"))
	pass := r.PostFormValue("pass")
	if err := camstream.SetLogin(user, pass); err != nil {
		return err.Error()
	}
	slog.Info("setup page: camera streaming login set")
	return ""
}
