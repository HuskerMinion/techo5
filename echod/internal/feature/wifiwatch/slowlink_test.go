package wifiwatch

import (
	"context"
	"strings"
	"testing"
	"time"
)

// slowWorld is what the slow-link watcher sees, set by the test. Looks run at once, on the test's
// own goroutine.
type slowWorld struct {
	now                time.Time
	wifi, up, gateway  bool
	reassocs, evidence int
	said               []string
}

func (sw *slowWorld) link() *slowLink {
	return &slowLink{
		available:   func() bool { return sw.wifi },
		now:         func() time.Time { return sw.now },
		joined:      func(context.Context) bool { return sw.up },
		gatewayUp:   func(context.Context) bool { return sw.gateway },
		reassociate: func(context.Context) error { sw.reassocs++; return nil },
		evidence:    func(context.Context) []string { sw.evidence++; return []string{"supplicant: wpa_state=COMPLETED"} },
		say:         func(msg string, _ ...any) { sw.said = append(sw.said, msg) },
		start:       func(_ string, f func()) { f() },
	}
}

func healthy() *slowWorld {
	return &slowWorld{now: time.Date(2026, 10, 6, 3, 0, 0, 0, time.UTC), wifi: true, up: true, gateway: true}
}

// A crawl on a link that is up is reassociated once, and not again for slowEvery however often the
// download says it is still slow.
func TestASlowLinkIsReassociatedOnceInAWhile(t *testing.T) {
	sw := healthy()
	s := sw.link()

	s.report(20_000)
	if sw.reassocs != 1 {
		t.Fatalf("reassociated %d times for a crawling download, want 1", sw.reassocs)
	}
	if sw.evidence != 1 || !strings.Contains(strings.Join(sw.said, "\n"), "crawling") {
		t.Errorf("the reassociation was not logged with its evidence: %q", sw.said)
	}

	for range 10 {
		sw.now = sw.now.Add(10 * time.Minute)
		s.report(0)
	}
	if sw.reassocs != 1 {
		t.Fatalf("reassociated %d times inside slowEvery", sw.reassocs)
	}

	sw.now = sw.now.Add(slowEvery)
	s.report(15_000)
	if sw.reassocs != 2 {
		t.Errorf("reassociated %d times once slowEvery had passed, want 2", sw.reassocs)
	}
}

func TestASlowLinkThatIsNotUpIsLeftAlone(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*slowWorld)
	}{
		{"no supplicant on this device", func(sw *slowWorld) { sw.wifi = false }},
		{"not joined", func(sw *slowWorld) { sw.up = false }},
		{"a gateway that does not answer", func(sw *slowWorld) { sw.gateway = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sw := healthy()
			tc.set(sw)
			s := sw.link()
			s.report(0)
			if sw.reassocs != 0 {
				t.Errorf("reassociated %d times", sw.reassocs)
			}
			// A look that did nothing does not use up the next few hours.
			sw.wifi, sw.up, sw.gateway = true, true, true
			sw.now = sw.now.Add(time.Minute)
			s.report(0)
			if sw.reassocs != 1 {
				t.Errorf("reassociated %d times once the link was up, want 1", sw.reassocs)
			}
		})
	}
}

// A report while a look is still running is not a second look.
func TestOneLookAtATime(t *testing.T) {
	sw := healthy()
	s := sw.link()
	var pending []func()
	s.start = func(_ string, f func()) { pending = append(pending, f) }

	s.report(0)
	s.report(0)
	if len(pending) != 1 {
		t.Fatalf("%d looks started, want 1", len(pending))
	}
	pending[0]()
	if sw.reassocs != 1 {
		t.Errorf("reassociated %d times, want 1", sw.reassocs)
	}
}
