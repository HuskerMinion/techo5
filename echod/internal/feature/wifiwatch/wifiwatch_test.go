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
}

func (wd *world) watch() *Watch {
	return &Watch{
		now:         func() time.Time { return wd.now },
		haConnected: func() bool { return wd.ha },
		joined:      func(context.Context) bool { return wd.joined },
		gatewayUp:   func(context.Context) bool { return wd.gateway },
		reassociate: func(context.Context) error { wd.reassocs++; return nil },
		evidence:    func(context.Context) []string { return []string{"supplicant: wpa_state=COMPLETED"} },
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
