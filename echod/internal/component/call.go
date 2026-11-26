package component

import (
	"encoding/json"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hook"
)

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

	// Reply, when set, asks Home Assistant for what the action returned and is called with it, or with
	// why there is nothing: no Home Assistant new enough is connected, the action failed, or no answer
	// came. It is called once, on a goroutine of its own, and never before Emit returns. A call with a
	// Reply goes to one Home Assistant, not to every one connected: an action that creates something
	// must not create it twice.
	Reply func(response json.RawMessage, err error)

	// ResponseTemplate is rendered by Home Assistant over what the action returned, as response, and
	// is what Reply gets instead: a template that picks out an id spares the device the action's whole
	// answer. Without one, Reply is only told whether the action worked: an action that returns nothing
	// fails when it is asked for what it returned.
	ResponseTemplate string

	// MinVersion is the oldest Home Assistant, as "2026.12", that a call with a Reply goes to. Empty for
	// any. One older than this is not asked at all: an action it does not have would only fail there,
	// or raise a repair there about a device asking for actions.
	MinVersion string
}

var CallService hook.Hook[Call]
