//go:build !panel

package tessera

// SetPanelSize does nothing off a TECHO5 Panel: every other screen's size is known from its board.
func SetPanelSize(w, h int) {}
