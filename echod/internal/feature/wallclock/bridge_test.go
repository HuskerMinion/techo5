package wallclock

import (
	"log/slog"
	"testing"
	"time"
)

// running is a timer as the device holds one.
func running(id, name string, ends time.Time) Timer {
	return Timer{ID: id, Name: name, Due: ends}
}

type call struct {
	name  string
	token string
}

type recorder struct {
	calls []call
	err   error
}

func (r *recorder) record(name, token string) error {
	r.calls = append(r.calls, call{name, token})
	return r.err
}

func (r *recorder) Timeinfo(at time.Time) error { return r.record("timeinfo", "") }
func (r *recorder) ClearIndicator() error       { return r.record("clear-indicator", "") }
func (r *recorder) TimersActive() error         { return r.record("timers-active", "") }
func (r *recorder) TimersCleared() error        { return r.record("timers-cleared", "") }
func (r *recorder) SetAlert(token string, end time.Time) error {
	return r.record("set "+FormatTime(end, time.UTC), token)
}
func (r *recorder) DeleteAlert(token string) error { return r.record("delete", token) }

func (r *recorder) reset() { r.calls = nil }

func (r *recorder) expect(t *testing.T, want ...call) {
	t.Helper()
	if len(r.calls) != len(want) {
		t.Fatalf("calls = %v, want %v", r.calls, want)
	}
	for i := range want {
		if r.calls[i] != want[i] {
			t.Fatalf("calls = %v, want %v", r.calls, want)
		}
	}
}

var start = time.Date(2026, 11, 2, 21, 0, 0, 0, time.UTC)

func newTestBridge(out Output) *Bridge {
	return New(Config{
		Timezone:         time.UTC,
		TimeinfoInterval: 30 * time.Minute,
		PulseClearDelay:  11 * time.Second,
		Log:              slog.New(slog.DiscardHandler),
	}, out)
}

func TestConnectPrimesAndSendsEveryAlert(t *testing.T) {
	timers := []Timer{
		running("a", "pasta", start.Add(2*time.Minute)),
		running("b", "eggs", start.Add(5*time.Minute)),
	}
	out := &recorder{}
	b := newTestBridge(out)

	if err := b.OnConnect(start); err != nil {
		t.Fatal(err)
	}
	if err := b.Step(timers, start); err != nil {
		t.Fatal(err)
	}
	out.expect(t,
		call{"timeinfo", ""},
		call{"clear-indicator", ""},
		call{"set 2026-11-02T21:02:00+00:00", Token("a")},
		call{"set 2026-11-02T21:05:00+00:00", Token("b")},
	)
}

// Two timers are two alerts, and the token of each stays the same from step to step: a token that
// changed would replay the clock's "set" animation forever.
func TestASecondTimerIsAnAlertOfItsOwn(t *testing.T) {
	timers := []Timer{running("a", "pasta", start.Add(2*time.Minute))}
	out := &recorder{}
	b := newTestBridge(out)
	if err := b.Step(timers, start); err != nil {
		t.Fatal(err)
	}
	out.reset()

	// Nothing changed: nothing is sent.
	if err := b.Step(timers, start.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	out.expect(t)

	timers = append(timers, running("b", "eggs", start.Add(5*time.Minute)))
	if err := b.Step(timers, start.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	out.expect(t, call{"set 2026-11-02T21:05:00+00:00", Token("b")})

	if Token("a") == Token("b") {
		t.Fatal("two timers share a token")
	}
}

// Cancelling one of two leaves the other where it was.
func TestCancellingOneLeavesTheOtherAlone(t *testing.T) {
	timers := []Timer{
		running("a", "pasta", start.Add(2*time.Minute)),
		running("b", "eggs", start.Add(5*time.Minute)),
	}
	out := &recorder{}
	b := newTestBridge(out)
	if err := b.Step(timers, start); err != nil {
		t.Fatal(err)
	}
	out.reset()

	if err := b.Step(timers[:1], start.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	out.expect(t, call{"delete", Token("b")})
	if _, ok := b.held[Token("a")]; !ok {
		t.Fatal("the timer still running was dropped")
	}
}

// A paused timer drops its marker without a sound; resuming puts it back at its new end.
func TestPauseAndResume(t *testing.T) {
	timers := []Timer{running("a", "pasta", start.Add(2*time.Minute))}
	out := &recorder{}
	b := newTestBridge(out)
	if err := b.Step(timers, start); err != nil {
		t.Fatal(err)
	}
	out.reset()

	// Paused: the timer is still held, but nothing is counting down, so no due time is given.
	if err := b.Step(nil, start.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	out.expect(t, call{"delete", Token("a")})
	out.reset()

	timers = []Timer{running("a", "pasta", start.Add(10*time.Minute))}
	if err := b.Step(timers, start.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	out.expect(t, call{"set 2026-11-02T21:10:00+00:00", Token("a")})
}

// Adding time moves the same alert to the new end.
func TestAddingTimeMovesTheAlert(t *testing.T) {
	timers := []Timer{running("a", "pasta", start.Add(2*time.Minute))}
	out := &recorder{}
	b := newTestBridge(out)
	if err := b.Step(timers, start); err != nil {
		t.Fatal(err)
	}
	out.reset()

	timers = []Timer{running("a", "pasta", start.Add(7*time.Minute))}
	if err := b.Step(timers, start.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	out.expect(t, call{"set 2026-11-02T21:07:00+00:00", Token("a")})
}

// A timer that reached its end rings once, and its alert is deleted after the pulse rather than
// during it.
func TestFinishingPulsesOnce(t *testing.T) {
	timers := []Timer{running("a", "pasta", start.Add(time.Minute))}
	out := &recorder{}
	b := newTestBridge(out)
	if err := b.Step(timers, start); err != nil {
		t.Fatal(err)
	}
	out.reset()

	if err := b.Step(nil, start.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	out.expect(t, call{"timers-active", ""})
	out.reset()

	// Still within the pulse: nothing more is sent.
	if err := b.Step(nil, start.Add(time.Minute+time.Second)); err != nil {
		t.Fatal(err)
	}
	out.expect(t)

	if err := b.Step(nil, start.Add(time.Minute+11*time.Second)); err != nil {
		t.Fatal(err)
	}
	out.expect(t, call{"delete", Token("a")}, call{"timers-cleared", ""})
}

// The documented hardware case: 5, 20 and 61 minutes running together are three alerts. The ring
// draws the soonest as a run and the others as markers.
func TestThreeTimersAreThreeAlerts(t *testing.T) {
	timers := []Timer{
		running("five", "eggs", start.Add(5*time.Minute)),
		running("twenty", "roast", start.Add(20*time.Minute)),
		running("long", "bread", start.Add(61*time.Minute)),
	}
	out := &recorder{}
	b := newTestBridge(out)
	if err := b.OnConnect(start); err != nil {
		t.Fatal(err)
	}
	out.reset()
	if err := b.Step(timers, start); err != nil {
		t.Fatal(err)
	}
	if len(out.calls) != 3 {
		t.Fatalf("sent %d alerts, want one per timer: %v", len(out.calls), out.calls)
	}
	if len(b.held) != 3 {
		t.Fatalf("holding %d alerts, want 3", len(b.held))
	}

	// The soonest ending takes its alert away and rings; the rest stay.
	out.reset()
	if err := b.Step(timers[1:], start.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	for _, c := range out.calls {
		if c.token == Token("twenty") || c.token == Token("long") {
			t.Fatalf("a running timer was disturbed: %v", out.calls)
		}
	}
	if len(b.held) != 2 {
		t.Fatalf("holding %d alerts, want 2", len(b.held))
	}
}

// The tokens are the 8-10 digit decimal form a real Echo sends.
func TestTokensAreTenDigits(t *testing.T) {
	for _, id := range []string{"01J8Z0", "local-175", "a"} {
		got := Token(id)
		if len(got) != 10 {
			t.Fatalf("Token(%q) = %q, want 10 digits", id, got)
		}
		for _, r := range got {
			if r < '0' || r > '9' {
				t.Fatalf("Token(%q) = %q, want digits only", id, got)
			}
		}
	}
}

// Two timers ending a few seconds apart each get their own burst. The clock stops pulsing on its own
// after about ten seconds, so a finish that arrives during an earlier pulse has to start a new one or
// it would be silent.
func TestASecondFinishDuringAPulseRingsAgain(t *testing.T) {
	first := start.Add(time.Minute)
	second := start.Add(time.Minute + 5*time.Second)

	timers := []Timer{
		running("a", "pasta", first),
		running("b", "sauce", second),
	}
	out := &recorder{}
	b := newTestBridge(out)
	if err := b.Step(timers, start); err != nil {
		t.Fatal(err)
	}
	out.reset()

	// The first ends; it rings.
	if err := b.Step(timers[1:], first); err != nil {
		t.Fatal(err)
	}
	out.expect(t, call{"timers-active", ""})
	out.reset()

	// Five seconds later the second ends, mid-pulse: it rings in its turn.
	if err := b.Step(nil, second); err != nil {
		t.Fatal(err)
	}
	out.expect(t, call{"timers-active", ""})
	out.reset()

	// The pulse still has its full time to run from the later finish.
	if err := b.Step(nil, second.Add(10*time.Second)); err != nil {
		t.Fatal(err)
	}
	out.expect(t)

	if err := b.Step(nil, second.Add(11*time.Second)); err != nil {
		t.Fatal(err)
	}
	if len(out.calls) != 3 {
		t.Fatalf("calls = %v, want two deletes and a clear", out.calls)
	}
}

// A timer that vanished long after it was due — the link was down — is not a fresh finish. Ringing
// for it would announce something that happened while nobody was listening.
func TestAStaleFinishDoesNotRing(t *testing.T) {
	timers := []Timer{running("a", "roast", start.Add(time.Minute))}
	out := &recorder{}
	b := newTestBridge(out)
	if err := b.Step(timers, start); err != nil {
		t.Fatal(err)
	}
	out.reset()

	if err := b.Step(nil, start.Add(time.Minute+10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	out.expect(t, call{"delete", Token("a")})
}

// A paused timer is gone from the clock's point of view: the timer package reports it with no due
// time, and the bridge must not hold an alert for it.
func TestPausedTimerIsNotAnAlert(t *testing.T) {
	out := &recorder{}
	b := newTestBridge(out)
	if err := b.Step([]Timer{{ID: "a", Name: "pasta"}}, start); err != nil {
		t.Fatal(err)
	}
	out.reset()
	if err := b.Step([]Timer{{ID: "a", Name: "pasta"}}, start.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	out.expect(t)
}
