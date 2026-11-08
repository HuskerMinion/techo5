//go:build dot

package firstrun

// A Dot, an Echo 2nd gen and a Dot 3rd gen have no screen: their first run is the setup page's, and
// one installed the usual way and added to Home Assistant is set up (doneByHome).
// Improv has been tried on the Dot 2nd gen and the Dot 3rd gen, so each can be reset for someone else.
func init() { screenless = true }
