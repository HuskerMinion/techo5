//go:build dot

package firstrun

// A Dot, an Echo 2nd gen and a Dot 3rd gen have no screen: their first run is the setup page's, and
// one installed the usual way and added to Home Assistant is set up (doneByHome).
func init() { screenless = true }
