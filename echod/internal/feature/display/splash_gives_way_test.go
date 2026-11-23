//go:build !dot && !spot

package display

import (
	"testing"
	"time"
)

// The splash ends with Home Assistant listening, and gives way without it: to a ring, to the Wi-Fi
// page, after a while with no Home Assistant access, and after longer with access to one that does
// not come (#695).
func TestTheSplashGivesWay(t *testing.T) {
	for _, c := range []struct {
		name                        string
		elapsed                     time.Duration
		here, ringing, wifi, access bool
		want                        bool
	}{
		{"listening, too soon", time.Second, true, false, false, true, false},
		{"listening", splashMin, true, false, false, true, true},
		{"a ring", time.Second, false, true, false, true, true},
		{"the Wi-Fi page", time.Second, false, false, true, true, true},
		{"no access, waiting", noHomeAssistantWait - time.Second, false, false, false, false, false},
		{"no access", noHomeAssistantWait, false, false, false, false, true},
		{"access, Home Assistant restarting", 2 * time.Minute, false, false, false, true, false},
		{"access, Home Assistant never came", unreachableWait, false, false, false, true, true},
	} {
		if got, why := splashGivesWay(c.elapsed, c.here, c.ringing, c.wifi, c.access); got != c.want {
			t.Errorf("%s: ends %v (%s), want %v", c.name, got, why, c.want)
		}
	}
}
