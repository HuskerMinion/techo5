//go:build linux && (dot || spot)

package btaudio

// isAthena: the Dot and Spot builds never run on the 2nd gen Show 8.
func isAthena() bool { return false }
