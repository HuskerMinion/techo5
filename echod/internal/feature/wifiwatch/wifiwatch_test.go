package wifiwatch

import (
	"context"
	"strings"
	"testing"
	"time"
)

// world is what the watcher sees, set by the test.
type world struct {
	now        time.Time
	ha, joined bool
	gateway    bool
	reassocs   int
	said       []string

	deaf     bool   // the radio hears nothing sent to a group
	noCounts bool   // the IP counters cannot be read
	in, out  uint64 // the group counters
}

// groupPackets is the network between two looks: the device sends to groups itself and hears its own
// copies, and unless it is deaf it hears the rest of the network too.
func (wd *world) groupPackets() (uint64, uint64, bool) {
	wd.out += 5
	wd.in += 5
	if !wd.deaf {
		wd.in += 3
	}
	return wd.in, wd.out, !wd.noCounts
}

func (wd *world) watch() *Watch {
	return &Watch{
		now:          func() time.Time { return wd.now },
		haConnected:  func() bool { return wd.ha },
		groupPackets: wd.groupPackets,
		joined:       func(context.Context) bool { return wd.joined },
		gatewayUp:    func(context.Context) bool { return wd.gateway },
		reassociate:  func(context.Context) error { wd.reassocs++; return nil },
		evidence:     func(context.Context) []string { return []string{"supplicant: wpa_state=COMPLETED"} },
		say: func(msg string, args ...any) {
			for i := 0; i+1 < len(args); i += 2 {
				if args[i] == "when" {
					msg += " " + args[i+1].(string)
				}
			}
			wd.said = append(wd.said, msg)
		},
		wait: firstWait,
	}
}

// count is how many times the watcher said something starting with prefix.
func (wd *world) count(prefix string) int {
	n := 0
	for _, s := range wd.said {
		if strings.HasPrefix(s, prefix) {
			n++
		}
	}
	return n
}

// What came of a reassociation is said once, either way, so a diagnostics download can tell a rekey
// that the watcher cured from one it did not.
func TestItSaysWhetherTheReassociationWorked(t *testing.T) {
	wd := start()
	w := wd.watch()
	wd.run(w, time.Minute)
	wd.ha = false
	wd.run(w, 4*time.Minute) // reassociates at 17:04
	if wd.count("wifi: evidence before") != 1 {
		t.Fatalf("no evidence recorded before reassociating: %q", wd.said)
	}
	wd.ha = true
	wd.run(w, 2*time.Minute)
	if wd.count("wifi: recovered") != 1 {
		t.Errorf("recovery said %d times, want once: %q", wd.count("wifi: recovered"), wd.said)
	}

	wd = start()
	w = wd.watch()
	wd.run(w, time.Minute)
	wd.ha = false
	wd.run(w, 10*time.Minute)
	if wd.count("wifi: still unreachable") != 1 || wd.count("wifi: evidence after") != 1 {
		t.Errorf("a reassociation that did not help was not said once with its evidence: %q", wd.said)
	}
	if wd.count("wifi: recovered") != 0 {
		t.Errorf("said it recovered when it did not: %q", wd.said)
	}
}

// run looks every 30 seconds for d.
func (wd *world) run(w *Watch, d time.Duration) {
	for end := wd.now.Add(d); wd.now.Before(end); wd.now = wd.now.Add(every) {
		w.look(context.Background())
	}
}

func start() *world {
	return &world{now: time.Date(2026, 9, 23, 17, 0, 0, 0, time.UTC), ha: true, joined: true, gateway: true}
}

func TestDeafToBroadcastsItReassociatesOnceAndBacksOff(t *testing.T) {
	wd := start()
	w := wd.watch()
	wd.run(w, time.Minute) // Home Assistant connected

	wd.ha = false // the rekey: joined, gateway answers, Home Assistant cannot reach us
	wd.run(w, 2*time.Minute)
	if wd.reassocs != 0 {
		t.Fatalf("reassociated %d times inside the grace a Home Assistant restart gets", wd.reassocs)
	}
	wd.run(w, 2*time.Minute)
	if wd.reassocs != 1 {
		t.Fatalf("reassociated %d times once Home Assistant had been gone long enough, want 1", wd.reassocs)
	}
	wd.run(w, 9*time.Minute)
	if wd.reassocs != 1 {
		t.Fatalf("reassociated again inside the first wait: %d", wd.reassocs)
	}
	// Home Assistant went at 17:01, so the first try was at 17:04, the second is due at 17:14 and the
	// third, the wait doubled, at 17:34.
	wd.run(w, 2*time.Minute) // to 17:16
	if wd.reassocs != 2 {
		t.Fatalf("no second try after the first wait: %d", wd.reassocs)
	}
	wd.run(w, 17*time.Minute) // to 17:33
	if wd.reassocs != 2 {
		t.Fatalf("the wait did not double: %d", wd.reassocs)
	}
	wd.run(w, 2*time.Minute) // past 17:34
	if wd.reassocs != 3 {
		t.Fatalf("no third try once the doubled wait was up: %d", wd.reassocs)
	}

	// Home Assistant back: the next loss starts from the beginning.
	wd.ha = true
	wd.run(w, time.Minute)
	if w.wait != firstWait || !w.lostAt.IsZero() {
		t.Errorf("coming back did not reset the watcher: wait %v, lost at %v", w.wait, w.lostAt)
	}
}

func TestItLeavesAloneWhatAReassociationDoesNotCure(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		everHA, joined, gwUp bool
	}{
		{"a device Home Assistant never connected to", false, true, true},
		{"a supplicant that is not joined", true, false, true},
		{"a gateway that does not answer (the network is down)", true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wd := start()
			wd.ha, wd.joined, wd.gateway = tc.everHA, tc.joined, tc.gwUp
			w := wd.watch()
			wd.run(w, time.Minute)
			wd.ha = false
			wd.run(w, time.Hour)
			if wd.reassocs != 0 {
				t.Errorf("reassociated %d times", wd.reassocs)
			}
		})
	}
}

// A device with a direct brain never has Home Assistant connected, so it goes by group traffic: when
// that stops while the device stays joined and the gateway answers, it is the rekey, and it is cured
// the same way, with the same grace and the same backoff.
func TestWithoutHomeAssistantItGoesByTrafficSentToEveryone(t *testing.T) {
	wd := start()
	wd.ha = false
	w := wd.watch()
	wd.run(w, time.Minute) // the network is heard, last at 17:00:30

	wd.deaf = true // from 17:01: only its own copies come back
	wd.run(w, 4*time.Minute)
	if wd.reassocs != 0 {
		t.Fatalf("reassociated %d times before the network had been quiet for quiet and then gone", wd.reassocs)
	}
	wd.run(w, 2*time.Minute) // quiet from 17:02:30, gone at 17:05:30
	if wd.reassocs != 1 {
		t.Fatalf("reassociated %d times once the network had been quiet long enough, want 1", wd.reassocs)
	}
	if wd.count("wifi: traffic sent to everyone has been gone") != 1 {
		t.Errorf("the log does not say what it went by: %q", wd.said)
	}
	wd.run(w, 8*time.Minute) // to 17:15; the second try is due at 17:15:30
	if wd.reassocs != 1 {
		t.Fatalf("reassociated again inside the first wait: %d", wd.reassocs)
	}

	wd.deaf = false
	wd.run(w, time.Minute)
	if wd.count("wifi: recovered: traffic sent to everyone") != 0 {
		t.Errorf("said it recovered long after the settle time: %q", wd.said)
	}
	if !w.lostAt.IsZero() || w.wait != firstWait {
		t.Errorf("hearing the network again did not reset the watcher: wait %v, lost at %v", w.wait, w.lostAt)
	}
}

// Back within the settle time after a reassociation is said as a recovery.
func TestWithoutHomeAssistantItSaysTheReassociationWorked(t *testing.T) {
	wd := start()
	wd.ha = false
	w := wd.watch()
	wd.run(w, time.Minute)
	wd.deaf = true
	wd.run(w, 5*time.Minute) // reassociates at 17:05:30
	if wd.reassocs != 1 {
		t.Fatalf("reassociated %d times, want 1", wd.reassocs)
	}
	wd.deaf = false
	wd.run(w, time.Minute)
	if wd.count("wifi: recovered: traffic sent to everyone is back") != 1 {
		t.Errorf("recovery not said once: %q", wd.said)
	}
}

func TestWithoutHomeAssistantItLeavesAloneWhatItCannotTell(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*world)
	}{
		{"a network that is never heard this boot", func(wd *world) { wd.deaf = true }},
		{"counters that cannot be read", func(wd *world) { wd.noCounts = true }},
		{"a gateway that does not answer (the network is down)", func(wd *world) { wd.gateway = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wd := start()
			wd.ha = false
			tc.set(wd)
			w := wd.watch()
			wd.run(w, time.Minute)
			wd.deaf = true
			wd.run(w, time.Hour)
			if wd.reassocs != 0 {
				t.Errorf("reassociated %d times", wd.reassocs)
			}
		})
	}
}

// Once Home Assistant has connected it is the sign, as before: group traffic still arriving does not
// hide that Home Assistant can no longer reach the device.
func TestHomeAssistantStaysTheSignOnceItHasConnected(t *testing.T) {
	wd := start()
	w := wd.watch()
	wd.run(w, time.Minute)
	wd.ha = false
	wd.run(w, 5*time.Minute)
	if wd.reassocs != 1 {
		t.Fatalf("reassociated %d times with Home Assistant gone and the network still heard, want 1", wd.reassocs)
	}
	if wd.count("wifi: Home Assistant has been gone") != 1 {
		t.Errorf("the log does not name Home Assistant: %q", wd.said)
	}
}
