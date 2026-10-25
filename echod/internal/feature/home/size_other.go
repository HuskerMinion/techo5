//go:build !panel

package home

// SetPanelSize does nothing off a TECHO5 Panel: every other screen's size is known from its board,
// and the panel_*.go files have it.
func SetPanelSize(w, h int) {}
