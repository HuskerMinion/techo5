package sendspin

import (
	"encoding/json"
	"strings"
	"testing"
)

// A server from before encryption still reads player.state and static_delay_ms; one on the encrypted
// wire gets the 1.0 fields, with the commands in client/state.
func TestClientStateFollowsTheWire(t *testing.T) {
	old, _ := json.Marshal((&session{}).stateFor(true, 5))
	for _, want := range []string{`"state":"synchronized"`, `"static_delay_ms":0`, `"available":true`} {
		if !strings.Contains(string(old), want) {
			t.Errorf("unencrypted state %s lacks %s", old, want)
		}
	}
	if strings.Contains(string(old), "supported_commands") {
		t.Errorf("unencrypted state %s carries supported_commands", old)
	}

	s := &session{encrypted: true}
	s.muted.Store(false)
	now, _ := json.Marshal(s.stateFor(false, 0))
	for _, gone := range []string{`"state"`, `static_delay_ms`} {
		if strings.Contains(string(now), gone) {
			t.Errorf("encrypted state %s carries %s", now, gone)
		}
	}
	for _, want := range []string{`"supported_commands":["volume","mute"]`, `"muted":false`, `"volume":0`, `"available":false`} {
		if !strings.Contains(string(now), want) {
			t.Errorf("encrypted state %s lacks %s", now, want)
		}
	}
}
