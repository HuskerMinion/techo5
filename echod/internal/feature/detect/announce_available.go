//go:build !dot

package detect

// announceWordAvailable is whether this device can afford to listen for the announce word.
//
// The model is an openWakeWord one, and loading any of those starts the shared front end: a mel
// transform and a speech embedding on every step, whatever else is loaded. A Show or a Spot runs
// that already and has the headroom for it.
const announceWordAvailable = true
