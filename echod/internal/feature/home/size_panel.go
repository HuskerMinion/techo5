//go:build panel

package home

// SetPanelSize is the screen's size once it has opened: on a TECHO5 Panel the canvas the app asked
// for, which photos, the now-playing picture, the rain map and camera frames are fitted to from then on.
func SetPanelSize(w, h int) {
	if w > 0 && h > 0 {
		Get().screenSized(w, h)
	}
}
