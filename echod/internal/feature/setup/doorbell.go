package setup

import (
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/doorbell"
)

// doorbellSection picks which camera on the Reolink recorder is a doorbell (feature/doorbell), and
// says whether the device is hearing it.
func doorbellSection(w http.ResponseWriter, token string) {
	c := config.Get()
	r := c.Home.Reolink
	fieldsetOpen(w, "doorbell", "Doorbell")
	if r.Base == "" {
		fmt.Fprint(w, `<p class="note" style="margin:0">Set up a Reolink Home Hub or NVR above first: a press of a
		 Reolink doorbell reaches the device through it.</p></fieldset>`)
		return
	}
	fmt.Fprint(w, `<form method="post" action="/setup/save">`)
	hidden(w, token, "doorbell")
	if c.Doorbell.Camera != "" {
		s := doorbell.Get().Status()
		state := "Starting."
		switch {
		case s.Camera != c.Doorbell.Camera: // just saved: the doorbell has not caught up yet
		case s.Listening:
			state = "Listening for the button."
		case s.Problem != "":
			state = "Not hearing the doorbell: " + html.EscapeString(s.Problem) + "."
		}
		if !s.LastPress.IsZero() {
			state += " Last pressed " + s.LastPress.Local().Format("Jan 2, 3:04 PM") + "."
		}
		fmt.Fprintf(w, `<p style="margin:0">%s</p>`, state)
	}
	fmt.Fprint(w, `<label for="dbcam">Doorbell camera</label><select id="dbcam" name="camera"><option value="">None</option>`)
	for _, cam := range r.Cameras {
		sel := ""
		if cam.Entity == c.Doorbell.Camera {
			sel = " selected"
		}
		fmt.Fprintf(w, `<option value="%s"%s>%s</option>`, html.EscapeString(cam.Entity), sel, html.EscapeString(cam.Name))
	}
	does := "rings the device and shows the doorbell's camera for a minute"
	if !hasScreen {
		does = "rings the device" // a Dot has no screen to show the camera on
	}
	fmt.Fprintf(w, `</select>
	 <label for="dbport">The recorder's ONVIF port</label>
	 <input id="dbport" name="port" value="%d" inputmode="numeric" autocomplete="off">
	 <p class="note">A press %s, at any hour. Turn
	  on ONVIF for the Home Hub or NVR in the Reolink app (Settings > Network > Advanced > Server
	  Settings), where its port is shown too. The doorbell itself has to be on the Hub or NVR: on its own, a
	  Reolink battery doorbell does not report its button.</p>
	 <p><button type="submit">Save</button></p></form>`, c.Doorbell.ONVIFPort(), does)
	fmt.Fprint(w, `<form method="post" action="/setup/save">`)
	hidden(w, token, "doorbell-chime")
	fmt.Fprint(w, `<p><button type="submit">Play the chime</button></p></form></fieldset>`)
}

// chimeDoorbell plays the chime, for hearing it from the page.
func chimeDoorbell() {
	doorbell.Get().Chime()
	slog.Info("setup page: doorbell chime played")
}

func saveDoorbell(r *http.Request) string {
	camera := strings.TrimSpace(r.PostFormValue("camera"))
	port := config.DefaultONVIFPort
	if p := strings.TrimSpace(r.PostFormValue("port")); p != "" && camera != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return "the ONVIF port is a number from 1 to 65535"
		}
		port = n
	}
	if camera != "" {
		listed := false
		for _, cam := range config.Get().Home.Reolink.Cameras {
			listed = listed || cam.Entity == camera
		}
		if !listed {
			return "that camera is not on the Reolink recorder"
		}
	}
	if err := config.Set().Doorbell().Save(camera, port); err != nil {
		return "could not save the doorbell: " + err.Error()
	}
	doorbell.Get().Poke()
	slog.Info("setup page: doorbell set", "camera", camera, "port", port)
	return ""
}
