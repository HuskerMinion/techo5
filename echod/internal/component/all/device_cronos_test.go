//go:build !dot && !spot

package all

import (
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/component"
)

// notOnThisDevice is empty on the Echo Show 5: registered is its whole list.
var notOnThisDevice []string

// deviceSpecific is what the Show has beyond the shared list: AirPlay and Spotify Connect, which the
// Spot does not offer, and the video player (feature/video), which neither the Spot nor the Dot has.
var deviceSpecific = []string{"airplay", "spotify_connect", "video", "dlna_video", "video_state", "video_title", "video_error"}

// The Show plays videos from Home Assistant: the actions an automation calls are there.
func TestTheShowHasTheVideoActions(t *testing.T) {
	have := map[string]bool{}
	for _, a := range component.Default().Actions() {
		have[a.Name] = true
	}
	for _, name := range []string{"play_video", "stop_video", "pause_video", "resume_video"} {
		if !have[name] {
			t.Errorf("no %s action", name)
		}
	}
}
