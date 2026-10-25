package setup

import (
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"unicode"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/camera"
)

func init() { saves["camerabuttons"] = saveCameraButtons }

// cameraButtonsSection is the buttons on the cameras' views that run a Home Assistant action, a camera a
// line: its entity, a colon, and its buttons as home_camera_buttons takes them.
func cameraButtonsSection(w http.ResponseWriter, token string) {
	cams := home.Get().Cameras()
	if len(cams) == 0 && !camera.Available() {
		return
	}
	text := cameraButtonsText(cams, config.Get().Home.CameraButtons)
	var names []string
	for _, c := range cams {
		names = append(names, html.EscapeString(c.Name)+" <code>"+html.EscapeString(c.Entity)+"</code>")
	}
	fmt.Fprint(w, `<fieldset><legend>Camera buttons</legend><form method="post" action="/setup/save">`)
	hidden(w, token, "camerabuttons", "connections")
	fmt.Fprintf(w, `<label for="cambuttons">Buttons</label>
	 <textarea id="cambuttons" name="buttons" rows="%d" maxlength="4000" placeholder="camera.front_door: Open gate=script.open_gate, Unlock=lock.unlock:lock.front_door">%s</textarea>
	 <input type="hidden" name="was_buttons" value="%s">
	 <p class="note">Buttons on a camera's view that run a Home Assistant action, such as opening a gate. One
	  camera a line: the camera's entity, a colon, then up to %d buttons split by commas, each
	  <code>Label=domain.service</code>, or <code>Label=domain.service:domain.object</code> for an action
	  on an entity. Every button asks for a second tap before it runs. Home Assistant runs them only with
	  "Allow the device to perform Home Assistant actions" on in this device's ESPHome options. An empty
	  box takes every button off.</p>`,
		max(3, min(strings.Count(text, "\n")+2, 10)), html.EscapeString(text), html.EscapeString(text), home.CameraButtonsMax)
	if len(names) > 0 {
		fmt.Fprintf(w, `<p class="note">The cameras: %s.</p>`, strings.Join(names, ", "))
	}
	fmt.Fprint(w, `<p><button type="submit">Save</button></p></form></fieldset>`)
}

// cameraButtonsText is every camera's buttons as the box shows them: the cameras in the list's order,
// then any the list does not have now, by entity.
func cameraButtonsText(cams []config.Camera, all map[string][]config.CameraButton) string {
	var order []string
	for _, c := range cams {
		if len(all[c.Entity]) > 0 && !slices.Contains(order, c.Entity) {
			order = append(order, c.Entity)
		}
	}
	var rest []string
	for entity, buttons := range all {
		if len(buttons) > 0 && !slices.Contains(order, entity) {
			rest = append(rest, entity)
		}
	}
	slices.Sort(rest)
	var lines []string
	for _, entity := range append(order, rest...) {
		lines = append(lines, entity+": "+home.FormatCameraButtons(all[entity]))
	}
	return strings.Join(lines, "\n")
}

func saveCameraButtons(r *http.Request) string {
	text := r.PostFormValue("buttons")
	if norm(text) == norm(r.PostFormValue("was_buttons")) {
		return "" // nothing changed
	}
	all, problem := parseCameraButtonsText(text)
	if problem != "" {
		return problem
	}
	if err := home.SetAllCameraButtons(all); err != nil {
		return "could not save the camera buttons: " + err.Error()
	}
	slog.Info("setup page: camera buttons set", "cameras", len(all))
	return ""
}

// parseCameraButtonsText reads the box: a camera a line, "entity: Label=domain.service, …". A camera's
// entity can have a colon of its own ("go2rtc:porch"), so it ends at the first space, and that word ends
// with the colon. A camera given twice is refused, and so is any bad button, by its line.
func parseCameraButtonsText(text string) (map[string][]config.CameraButton, string) {
	all := map[string][]config.CameraButton{}
	for _, l := range strings.Split(norm(text), "\n") {
		if l == "" {
			continue
		}
		word, rest := l, ""
		if i := strings.IndexFunc(l, unicode.IsSpace); i >= 0 {
			word, rest = l[:i], l[i:]
		}
		entity, ok := strings.CutSuffix(word, ":")
		if !ok || entity == "" {
			return nil, fmt.Sprintf("%q: start the line with the camera's entity and a colon, as camera.front_door: Open gate=script.open_gate", l)
		}
		if _, twice := all[entity]; twice {
			return nil, fmt.Sprintf("%s is on two lines; put its buttons on one", entity)
		}
		buttons, err := home.ParseCameraButtons(rest)
		if err != nil {
			return nil, entity + ": " + err.Error()
		}
		all[entity] = buttons
	}
	return all, ""
}
