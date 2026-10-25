package component

import "github.com/HuskerMinion/techo5/echod/internal/lib/hook"

// Call is a Home Assistant action the device wants run — a script, a service — with its data. Home
// Assistant only honors these when "Allow the device to perform Home Assistant actions" is on for
// the ESPHome entry, which the radio page needs.
//
// A hook for the same reason as Fire: the server imports the features, so they cannot import it.
type Call struct {
	Service string
	Data    map[string]string

	// Templates are fields Home Assistant renders before the call, for a value that must not arrive as
	// text: every field in Data is a string, and an action that validates a bool strictly refuses
	// "false" where "{{ false }}" renders to the real thing.
	Templates map[string]string

	// Unheard, when set, is called on the caller's goroutine, before Emit returns, when the call went to
	// nobody: no Home Assistant was connected to the device to take it. Nothing calls it where nothing
	// serves the device's API at all, since nothing is known then.
	Unheard func()
}

var CallService hook.Hook[Call]
