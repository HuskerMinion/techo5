package setup

import (
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/camera"
)

func init() { saves["cameralist"] = saveCameraList }

// cameraListSection is the camera list as the drawer, the grid and "show the …" have it: whether the
// device's own camera is on it, and its order, as a camera's name a line.
func cameraListSection(w http.ResponseWriter, token string) {
	h := config.Get().Home
	cams := home.Get().Cameras()
	if len(cams) == 0 && !camera.Available() {
		return
	}
	fmt.Fprint(w, `<fieldset><legend>Camera list</legend><form method="post" action="/setup/save">`)
	hidden(w, token, "cameralist", "connections")
	if camera.Available() {
		fmt.Fprintf(w, `<p><label><input type="checkbox" name="own" value="1"%s> List this device's own camera</label></p>`,
			checkedIf(!h.OwnCameraLeftOut))
	}
	var lines []string
	for _, c := range cams {
		lines = append(lines, c.Name)
	}
	text := strings.Join(lines, "\n")
	fmt.Fprintf(w, `<label for="camorder">Order</label>
	 <textarea id="camorder" name="order" rows="%d" maxlength="4000">%s</textarea>
	 <input type="hidden" name="was_order" value="%s">
	 <p class="note">The cameras as the drawer and the grid list them, one a line, the first at the top.
	  Move the lines to change the order. A camera left out of the box goes after the others, and an
	  empty box puts them back in the usual order. Left off the list, the device's own camera is not in
	  the drawer or the grid, and "show" does not find it; an automation can still put it up.</p>
	 <p><button type="submit">Save</button></p></form></fieldset>`,
		max(3, min(len(lines), 10)), html.EscapeString(text), html.EscapeString(text))
}

func saveCameraList(r *http.Request) string {
	h := config.Get().Home
	leftOut := h.OwnCameraLeftOut
	if camera.Available() {
		leftOut = r.PostFormValue("own") == ""
	}
	order := h.CameraOrder
	if text := r.PostFormValue("order"); norm(text) != norm(r.PostFormValue("was_order")) {
		var problem string
		if order, problem = cameraOrder(home.Get().Cameras(), text); problem != "" {
			return problem
		}
	}
	if leftOut == h.OwnCameraLeftOut && strings.Join(order, "\n") == strings.Join(h.CameraOrder, "\n") {
		return "" // nothing changed
	}
	if err := home.SetCameraList(leftOut, order); err != nil {
		return "could not save the camera list: " + err.Error()
	}
	slog.Info("setup page: camera list set", "own_left_out", leftOut, "ordered", len(order))
	return ""
}

// norm is the order box's text with its line ends and blank lines evened out, to tell a change.
func norm(s string) string {
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(s, "\r", "\n"), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// cameraOrder reads the order box, a camera's name a line, into the cameras' entities. Names match in
// any case; two cameras of the same name take the lines in their list order. Empty is the usual order.
func cameraOrder(cams []config.Camera, text string) ([]string, string) {
	used := make([]bool, len(cams))
	var order []string
	for _, l := range strings.Split(norm(text), "\n") {
		if l == "" {
			continue
		}
		found := false
		for i, c := range cams {
			if !used[i] && strings.EqualFold(c.Name, l) {
				order, used[i], found = append(order, c.Entity), true, true
				break
			}
		}
		if !found {
			return nil, fmt.Sprintf("no camera on the list is called %q", l)
		}
	}
	return order, ""
}
