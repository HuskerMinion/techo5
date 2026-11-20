//go:build dot

package firstrun

// A Dot, an Echo 2nd gen and a Dot 3rd gen have no screen: their first run is the setup page's, and
// one installed the usual way and added to Home Assistant is set up (doneByHome).
// Improv has been tried on the Dot 2nd gen and the Dot 3rd gen, so each can be reset for someone else.
// Nor do they have a screen language or a clock to show, so their first run has no language step.
func init() {
	screenless = true
	steps = []string{Hello, Wifi, Zone, Name, Home, Updates, Sound, Done}
}
