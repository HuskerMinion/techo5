//go:build !dot && !spot && !panel

package speaker

import "testing"

// The ThinkSmart View's front end reaches the amplifier only through its route, which is written
// before the stream opens and again with the amplifier's volume at its 0 dB top.
func TestTheThinkSmartViewRoutesBeforeItOpens(t *testing.T) {
	if len(thinkSmartRoutes) != 1 || thinkSmartRoutes[0].name != "QUAT_MI2S_RX Audio Mixer MultiMedia1" || thinkSmartRoutes[0].level != 1 {
		t.Errorf("routes = %+v", thinkSmartRoutes)
	}
	last := thinkSmartInit[len(thinkSmartInit)-1]
	if len(thinkSmartInit) != 2 || thinkSmartInit[0].name != thinkSmartRoutes[0].name || last.name != "Speaker Amp Master Playback Volume" || last.level != 85 {
		t.Errorf("init = %+v", thinkSmartInit)
	}
	if len(thinkSmartRoutes) != 1 {
		t.Errorf("building the init changed the routes: %+v", thinkSmartRoutes)
	}
}
