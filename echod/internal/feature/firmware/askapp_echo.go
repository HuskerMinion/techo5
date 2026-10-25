//go:build !panel

package firmware

// askApp is for panels: an Echo with no release stream to follow has nobody else to ask.
func askApp(bool) {}
