//go:build !panel_debug

package loopback

// Dump is the echo path recorder, left out of this build: only a debug build records (dump.go, the
// panel_debug tag). On is always false here, whatever is at FlagPath, so a release build never writes
// what the tablet hears or plays to a file.
type Dump struct {
	FlagPath string
}

// On is false: this build has no recorder.
func (*Dump) On() bool { return false }
