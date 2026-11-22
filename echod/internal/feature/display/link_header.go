//go:build !dot && !spot

package display

// headerLine is what the pill at the top of the page says: the microphone off, what the device has lost,
// or both. Empty is no pill.
func headerLine(s scene) string {
	var line string
	if s.muted {
		line = tr("microphone_off")
	}
	var lost string
	switch s.linkDown {
	case linkNoWifi:
		lost = tr("no_wifi")
	case linkNoHome:
		lost = tr("no_home_assistant")
	}
	switch {
	case line != "" && lost != "":
		return line + "  ·  " + lost
	case lost != "":
		return lost
	}
	return line
}
