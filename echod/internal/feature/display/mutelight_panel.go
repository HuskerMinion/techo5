//go:build panel

package display

// muteLightSaysIt: a tablet has no mute light, so the header always says it (mutelight.go).
var muteLightSaysIt = func() bool { return false }
