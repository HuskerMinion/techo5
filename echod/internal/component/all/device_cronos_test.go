//go:build !dot && !spot

package all

import (
	"slices"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/feature/video"
)

// notOnThisDevice is what the Echo Show 5 does not put up: it has no LED ring, so nothing that sets
// what a ring shows (led.HasRing). A panel, built on the Show's, differs a little (notOnPanel,
// onlyOnPanel).
var notOnThisDevice = append(append([]string(nil), ringOnly...), notOnPanel...)

// deviceSpecific is what the Show has beyond the shared list: AirPlay and Spotify Connect, which the
// Spot does not offer, and the video player (feature/video), which neither the Spot nor the Dot has.
// A panel's list goes without what notOnPanel names and has onlyOnPanel's besides.
var deviceSpecific = append(slices.DeleteFunc([]string{"airplay", "spotify_connect", "video", "dlna_video", "video_state", "video_title", "video_error", "clock_hold_gesture"},
	func(id string) bool { return slices.Contains(notOnPanel, id) }), onlyOnPanel...)

// The Show plays videos from Home Assistant: the actions an automation calls are there. A panel has no
// video player (video.Here), and no actions for one.
func TestTheShowHasTheVideoActions(t *testing.T) {
	have := map[string]bool{}
	for _, a := range component.Default().Actions() {
		have[a.Name] = true
	}
	for _, name := range []string{"play_video", "stop_video", "pause_video", "resume_video"} {
		if have[name] != video.Here {
			t.Errorf("%s action: %v, want %v", name, have[name], video.Here)
		}
	}
}

// actionsNotOnThisDevice: the Show offers every action the shared code has.
var actionsNotOnThisDevice = map[string]string{}
