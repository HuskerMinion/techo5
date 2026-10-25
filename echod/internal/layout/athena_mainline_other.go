//go:build dot || spot || panel

package layout

// AthenaMainline is never true on a Dot, a Spot or a panel.
func AthenaMainline() bool { return false }

// Skylight is never true on a Dot, a Spot or a panel: the Skylight Calendar
// runs the Show build.
func Skylight() bool { return false }

// HasMicrophone: every Dot and Spot has its microphones, and a panel gets
// its microphone from the app.
func HasMicrophone() bool { return true }

// idmeFallback: the Dot and the Spot always have /proc/idme, and a panel has no idme at all.
func idmeFallback(string) (string, bool) { return "", false }

// CronosMainline: never here.
func CronosMainline() bool { return false }
