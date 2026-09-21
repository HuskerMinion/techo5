//go:build dot

package detect

// announceWordAvailable is whether this device can afford to listen for the announce word.
//
// Not on a Dot. Measured on one: its detector sat at 37% of the time with the two microWakeWord
// models it carries, and loading this took it to 86%, with the worst frame at 100 ms against a
// 20 ms budget. The openWakeWord front end cost 13.4 ms a frame against microWakeWord's 3.8 for
// both of its models together — it is not a close call, and a device that cannot keep up with its
// own microphone has stopped hearing the wake word it already had.
//
// The way in for a Dot is a microWakeWord model of the same phrase, which is the cheap kind and the
// kind its other words already are. That is a different training pipeline and has not been done.
const announceWordAvailable = false
