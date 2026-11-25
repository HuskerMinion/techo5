//go:build panel

package setup

import (
	"fmt"
	"net/http"
)

// addonsSection is a line instead of the Add-ons card on a TECHO5 Panel: Android does not let an app
// that targets Android 10 or later run a program it downloaded, so add-ons cannot run there
// (feature/addons, here_panel.go).
func addonsSection(w http.ResponseWriter, _ string) {
	sectionOpen(w, "addons", "Add-ons", "Not on TECHO5 Panel")
	fmt.Fprint(w, `<p class="note" style="margin:0">Add-ons are not part of TECHO5 Panel: Android does not let an
	 app run programs it downloads. They run on the Echo Show, Spot and Dot builds.</p>`)
	sectionClose(w)
}
