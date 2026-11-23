//go:build !dot && !spot

package display

import (
	"testing"
	"time"
)

// The splash ends with Home Assistant listening, and gives way without it: to a ring, to the Wi-Fi
// page, to a browser asking to be let in, to Home Assistant refusing the device's access (#706), after
// a while with no Home Assistant access, and after longer with access to one that does not come (#695).
func TestTheSplashGivesWay(t *testing.T) {
	for _, c := range []struct {
		name                                         string
		elapsed                                      time.Duration
		here, ringing, wifi, access, refused, asking bool
		want                                         bool
	}{
		{"listening, too soon", time.Second, true, false, false, true, false, false, false},
		{"listening", splashMin, true, false, false, true, false, false, true},
		{"a ring", time.Second, false, true, false, true, false, false, true},
		{"the Wi-Fi page", time.Second, false, false, true, true, false, false, true},
		{"no access, waiting", noHomeAssistantWait - time.Second, false, false, false, false, false, false, false},
		{"no access", noHomeAssistantWait, false, false, false, false, false, false, true},
		{"access, Home Assistant restarting", 2 * time.Minute, false, false, false, true, false, false, false},
		{"access, Home Assistant never came", unreachableWait, false, false, false, true, false, false, true},
		{"access refused", time.Second, false, false, false, true, true, false, true},
		{"a sign-in refused its renewal", time.Second, false, false, false, false, true, false, true},
		{"a browser asking", time.Second, false, false, false, true, false, true, true},
	} {
		if got, why := splashGivesWay(c.elapsed, c.here, c.ringing, c.wifi, c.access, c.refused, c.asking); got != c.want {
			t.Errorf("%s: ends %v (%s), want %v", c.name, got, why, c.want)
		}
	}
}
