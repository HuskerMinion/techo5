package notify

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/ring"
)

// fakeBell stands in for the bell a critical notification rings.
type fakeBell struct {
	mu     sync.Mutex
	steps  []int // each ring's step, in the order they started
	rises  []time.Duration
	labels []string // each ring's label
	stops  int
	ended  []func()
}

func (b *fakeBell) rings() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.steps)
}

func (b *fakeBell) stopped() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.stops
}

func bellFor(r *rig) *fakeBell {
	b := &fakeBell{}
	r.f.ring = func(label string, step int, rise time.Duration, ended func()) func() {
		b.mu.Lock()
		b.steps, b.labels, b.ended = append(b.steps, step), append(b.labels, label), append(b.ended, ended)
		b.rises = append(b.rises, rise)
		b.mu.Unlock()
		var once sync.Once
		return func() { once.Do(func() { b.mu.Lock(); b.stops++; b.mu.Unlock() }) }
	}
	return b
}

func TestButtonsAreReadAsTheActionsTakeThem(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []Button
	}{
		{"", nil},
		{" , \n", nil},
		{"Snooze=snooze, OK=ok", []Button{{"Snooze", "snooze"}, {"OK", "ok"}}},
		{"Open gate=script.open_gate\nIgnore", []Button{{"Open gate", "script.open_gate"}, {"Ignore", "Ignore"}}},
		{"  Yes = y ,No=n,Later=later", []Button{{"Yes", "y"}, {"No", "n"}, {"Later", "later"}}},
	} {
		got, err := parseButtons(c.in)
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if len(got) != len(c.want) {
			t.Errorf("%q: %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%q: button %d is %v, want %v", c.in, i, got[i], c.want[i])
			}
		}
	}
	for _, bad := range []string{
		"=id",
		"Label=",
		"A,B,C,D",
		strings.Repeat("x", maxButtonLabel+1) + "=x",
		"Label=" + strings.Repeat("x", maxButtonID+1),
		"Bell\x07=b",
	} {
		if _, err := parseButtons(bad); err == nil {
			t.Errorf("%q taken, want it refused", bad)
		}
	}
}

// A critical notification lights the screen at its own brightness while it rings, full when sent 0, and
// gives the screen back once it is quieted.
func TestACriticalNotificationLightsTheScreenWhileItRings(t *testing.T) {
	for _, c := range []struct{ in, want int }{{0, 100}, {-4, 100}, {40, 40}, {300, 100}} {
		r := newRig(t, true)
		bellFor(r)
		if _, ok := r.f.CriticalLight(); ok {
			t.Fatal("a light with nothing up")
		}
		_ = r.f.NotifyCritical("Smoke in the hall", "Smoke", "", "", 0, "", "", 0, 0, c.in, 0)
		if got, ok := r.f.CriticalLight(); !ok || got.Percent != c.want || got.Since.IsZero() {
			t.Errorf("brightness %d lights at %+v, want %d", c.in, got, c.want)
		}
		n, _ := r.f.Card()
		r.f.Tap(n.ID)
		if got, ok := r.f.CriticalLight(); ok {
			t.Errorf("quieted, still lights at %+v", got)
		}
	}
}

// A critical notification's rises are their seconds, held to the length of a whole ring; none for 0.
func TestACriticalRiseIsHeldToTheRing(t *testing.T) {
	r := newRig(t, true)
	b := bellFor(r)
	for _, rise := range []int{0, -5, 20, 100000} {
		_ = r.f.NotifyCritical("Smoke in the hall", "Smoke", "", "", 0, "", "", 30, rise, 0, 0)
	}
	want := []time.Duration{0, 0, 20 * time.Second, ring.RingFor}
	for i, w := range want {
		if b.rises[i] != w {
			t.Errorf("rise %d: %v, want %v", i, b.rises[i], w)
		}
	}
	_ = r.f.NotifyCritical("Smoke in the hall", "Smoke", "", "", 0, "", "", 30, 0, 50, 12)
	if l, _ := r.f.CriticalLight(); l.Percent != 50 || l.Rise != 12*time.Second {
		t.Errorf("light %+v, want 50%% over 12s", l)
	}
}

// A notification with buttons chimes at its own volume, at its usual level when sent 0, and not at all
// at night, whatever its volume.
func TestNotifyButtonsChimesAtItsOwnVolume(t *testing.T) {
	r := newRig(t, true)
	for _, c := range []struct{ volume, step int }{{0, 0}, {10, volumeStep(10)}, {100, config.VolumeSteps}} {
		n := r.chimes.Load()
		if err := r.f.NotifyButtons("Washer done", "", "", "", "", "", 0, "", "", c.volume); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "the chime", func() bool { return r.chimes.Load() == n+1 })
		if got := int(r.chimeStep.Load()); got != c.step {
			t.Errorf("volume %d chimed at step %d, want %d", c.volume, got, c.step)
		}
	}
	r.hushed.Store(true)
	n := r.chimes.Load()
	_ = r.f.NotifyButtons("Washer done", "", "", "", "", "", 0, "", "", 100)
	time.Sleep(20 * time.Millisecond)
	if r.chimes.Load() != n {
		t.Error("chimed at night")
	}
}

func TestACriticalVolumeIsInTheSpeakersSteps(t *testing.T) {
	for _, c := range []struct{ in, want int }{
		{-5, 0}, {0, 0}, {1, 1}, {50, config.VolumeSteps / 2}, {100, config.VolumeSteps}, {250, config.VolumeSteps},
	} {
		if got := volumeStep(c.in); got != c.want {
			t.Errorf("volume %d is step %d, want %d", c.in, got, c.want)
		}
	}
}

// A button tap tells Home Assistant which button, and that is how the notification ends: nothing more
// is reported of it. A tap on the card off the buttons puts it away, as on any card.
func TestAButtonTapEndsTheNotificationWithItsAction(t *testing.T) {
	r := newRig(t, true)
	if err := r.f.NotifyButtons("Someone is at the door", "Door", "", "", "", "door", 0, "", "Open=open, Ignore=ignore", 0); err != nil {
		t.Fatal(err)
	}
	n, up := r.f.Card()
	if !up || len(n.Buttons) != 2 || n.Critical {
		t.Fatalf("card %+v, up %v", n, up)
	}
	if r.f.Press("no such id", 0) || r.f.Press(n.ID, 2) {
		t.Error("a press on what is not there was taken")
	}
	if !r.f.Press(n.ID, 1) {
		t.Fatal("the press was not taken")
	}
	if _, up := r.f.Card(); up {
		t.Error("the card stayed up")
	}
	if got := r.fired(); !same(got, []string{"shown", "action"}) {
		t.Errorf("events %v", got)
	}
	if e := r.last(); e.Data["action"] != "ignore" || e.Data["tag"] != "door" {
		t.Errorf("action event %v", e.Data)
	}
	waitFor(t, "the chime", func() bool { return r.chimes.Load() == 1 })

	if err := r.f.NotifyButtons("Washer done", "", "", "off", "", "", 0, "", "OK", 0); err != nil {
		t.Fatal(err)
	}
	n, _ = r.f.Card()
	if !r.f.Tap(n.ID) {
		t.Error("a tap off the buttons did nothing")
	}
	if e := r.last(); e.Data["event"] != "dismissed" || e.Data["action"] != "" {
		t.Errorf("last event %v, want dismissed with no action", e.Data)
	}
	if err := r.f.NotifyButtons("x", "", "", "", "", "", 0, "", "A,B,C,D", 0); err == nil {
		t.Error("four buttons taken")
	}
}

// A critical notification rings at its own volume, night or not, and is given an OK button when sent
// none. A tap on the card quiets it and leaves it up; a button ends it, and its bell.
func TestACriticalNotificationRingsUntilAnswered(t *testing.T) {
	r := newRig(t, true)
	b := bellFor(r)
	r.hushed.Store(true) // night: no chime for a notification, but a critical one rings
	if err := r.f.NotifyCritical("Water under the sink", "Leak", "mdi:water-alert", "leak", 0, "", "", 40, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	n, up := r.f.Card()
	if !up || !n.Critical || len(n.Buttons) != 1 || n.Buttons[0].ID != OKID || n.Buttons[0].Label != "" {
		t.Fatalf("card %+v, up %v", n, up)
	}
	if b.rings() != 1 || b.steps[0] != volumeStep(40) || b.labels[0] != "Leak" || b.rises[0] != 0 {
		t.Fatalf("bell %+v", b.steps)
	}
	if !r.f.Ringing(n.ID) || !r.f.CriticalUp() {
		t.Error("not ringing")
	}
	if !r.f.Tap(n.ID) {
		t.Error("a tap on the card did not quiet it")
	}
	if b.stopped() != 1 || r.f.Ringing(n.ID) {
		t.Error("the bell rang on after a tap")
	}
	if _, up := r.f.Card(); !up {
		t.Fatal("a tap took the critical card away")
	}
	if r.f.Tap(n.ID) {
		t.Error("a second tap did something")
	}
	if !r.f.Press(n.ID, 0) {
		t.Fatal("OK not taken")
	}
	if got := r.fired(); !same(got, []string{"shown", "action"}) {
		t.Errorf("events %v", got)
	}
	if r.last().Data["action"] != OKID {
		t.Errorf("action %q", r.last().Data["action"])
	}
	time.Sleep(20 * time.Millisecond)
	if r.chimes.Load() != 0 {
		t.Errorf("chimed %d times; a critical notification rings instead", r.chimes.Load())
	}
}

// The stop word (silence) quiets a critical notification's bell; on a screen its card stays, and a
// Dot, with nothing to tap, ends it.
func TestTheStopWordQuietsACriticalNotification(t *testing.T) {
	r := newRig(t, true)
	b := bellFor(r)
	_ = r.f.NotifyCritical("Smoke in the hall", "Smoke", "", "", 0, "", "", 0, 0, 0, 0)
	if !r.f.silence() || b.stopped() != 1 {
		t.Fatal("not silenced")
	}
	if _, up := r.f.Card(); !up {
		t.Error("the card went with the bell")
	}
	if r.f.silence() {
		t.Error("silenced twice")
	}

	r = newRig(t, false)
	_ = bellFor(r)
	_ = r.f.NotifyCritical("Smoke in the hall", "Smoke", "", "", 0, "", "", 0, 0, 0, 0)
	if !r.f.silence() {
		t.Fatal("not silenced on a Dot")
	}
	if got := r.fired(); !same(got, []string{"shown", "dismissed"}) {
		t.Errorf("Dot events %v", got)
	}

	// On a Dot a bell that ends by itself (a button quieted it, and the offer ran out) ends it too.
	r = newRig(t, false)
	b = bellFor(r)
	_ = r.f.NotifyCritical("Smoke in the hall", "Smoke", "", "", 0, "", "", 0, 0, 0, 0)
	b.ended[0]()
	if got := r.fired(); !same(got, []string{"shown", "dismissed"}) {
		t.Errorf("Dot events after the bell ended %v", got)
	}
}

// A notification that comes while a critical one is up waits, and only the latest; it comes up, quietly,
// once the critical one goes. Another critical one takes the place at once.
func TestANotificationWaitsBehindACriticalOne(t *testing.T) {
	r := newRig(t, true)
	b := bellFor(r)
	_ = r.f.NotifyCritical("Water under the sink", "Leak", "", "leak", 0, "", "", 0, 0, 0, 0)
	crit, _ := r.f.Card()
	_ = r.f.Notify("Washer done", "Washer", "", "", "", "washer", 0)
	_ = r.f.Notify("Dryer done", "Dryer", "", "", "", "dryer", 0)
	r.f.Alert("Meeting in 5 min", "Stand-up", time.Minute, false) // the device's own waits too, unreported
	if n, _ := r.f.Card(); n.ID != crit.ID {
		t.Fatalf("the card is %+v, want the critical one", n)
	}
	if err := r.f.NotifyPicture("Doorbell", "", "/local/door.png", "", "", "", "", 0); err == nil {
		t.Error("a picture went over the critical card")
	}
	if got := r.fired(); !same(got, []string{"shown", "replaced", "replaced"}) {
		t.Errorf("events %v, want the critical one shown and the waiting ones replaced", got)
	}
	time.Sleep(20 * time.Millisecond)
	if r.chimes.Load() != 0 {
		t.Error("a waiting notification chimed")
	}

	// The device's own alert waits last; clearing by the dryer's tag finds nothing waiting with it.
	if r.f.Clear("dryer") {
		t.Error("cleared what was not waiting")
	}
	_ = r.f.Notify("Dryer done", "Dryer", "", "", "", "dryer", 0)
	r.f.Press(crit.ID, 0)
	if b.stopped() != 1 {
		t.Error("the bell rang on")
	}
	waitFor(t, "the waiting card", func() bool { n, up := r.f.Card(); return up && n.Title == "Dryer" })
	time.Sleep(20 * time.Millisecond)
	if r.chimes.Load() != 0 {
		t.Error("the waiting card chimed when it came up")
	}

	// A critical one whose time ran out before the ticker saw it is replaced by the next notification
	// at once, and what waited behind it is older news: replaced, not left waiting.
	r = newRig(t, true)
	b = bellFor(r)
	_ = r.f.NotifyCritical("Water under the sink", "Leak", "", "leak", 30, "", "", 0, 0, 0, 0)
	r.now = r.now.Add(10 * time.Second)
	_ = r.f.Notify("Washer done", "Washer", "", "", "", "washer", 0)
	r.now = r.now.Add(30 * time.Second)
	_ = r.f.Notify("Dryer done", "Dryer", "", "", "", "dryer", 0)
	if n, _ := r.f.Card(); n.Title != "Dryer" || b.stopped() != 1 {
		t.Errorf("card %q, bell stops %d", n.Title, b.stopped())
	}
	if got := r.fired(); !same(got, []string{"shown", "replaced", "replaced", "shown"}) {
		t.Errorf("events %v", got)
	}
	r.f.mu.Lock()
	waiting := r.f.waiting
	r.f.mu.Unlock()
	if waiting != nil {
		t.Errorf("%+v still waits", waiting.n)
	}

	// Critical over critical: the new one rings, the old one is replaced and its bell stops.
	r = newRig(t, true)
	b = bellFor(r)
	_ = r.f.NotifyCritical("one", "", "", "", 0, "", "", 0, 0, 0, 0)
	_ = r.f.NotifyCritical("two", "", "", "", 0, "", "", 0, 0, 0, 0)
	if n, _ := r.f.Card(); n.Message != "two" || b.rings() != 2 || b.stopped() != 1 {
		t.Errorf("card %q, rings %d, stops %d", n.Message, b.rings(), b.stopped())
	}
}

// Clearing by tag takes a critical notification down with its bell, and a waiting one with its tag.
func TestClearingACriticalNotificationStopsItsBell(t *testing.T) {
	r := newRig(t, true)
	b := bellFor(r)
	_ = r.f.NotifyCritical("Water under the sink", "Leak", "", "leak", 0, "", "", 0, 0, 0, 0)
	_ = r.f.Notify("Washer done", "Washer", "", "", "", "washer", 0)
	if !r.f.Clear("washer") {
		t.Error("the waiting notification was not cleared")
	}
	if !r.f.Clear("leak") || b.stopped() != 1 {
		t.Error("the critical one was not cleared, or its bell rang on")
	}
	if got := r.fired(); !same(got, []string{"shown", "cleared", "cleared"}) {
		t.Errorf("events %v", got)
	}
	time.Sleep(20 * time.Millisecond)
	if _, up := r.f.Card(); up {
		t.Error("something came up after both were cleared")
	}

	// Its own time running out ends it too, and its bell.
	r = newRig(t, true)
	b = bellFor(r)
	_ = r.f.NotifyCritical("Water under the sink", "Leak", "", "leak", 30, "", "", 0, 0, 0, 0)
	r.now = r.now.Add(31 * time.Second)
	r.f.tick(r.now)
	if b.stopped() != 1 || r.last().Data["event"] != "expired" {
		t.Errorf("stops %d, last %v", b.stopped(), r.last().Data)
	}
}

// A critical notification holds others back only while it rings. Quieted (the stop word, Stop, a button
// that leaves it up), it is a card like any other: a picture is shown over it rather than refused, and
// words replace it rather than waiting up to its twelve hours.
func TestAQuietedCriticalNotificationNoLongerHoldsOthersBack(t *testing.T) {
	r := newRig(t, true)
	_ = bellFor(r)
	_ = r.f.NotifyCritical("Water under the sink", "Leak", "", "leak", 0, "", "", 0, 0, 0, 0)
	if !r.f.silence() {
		t.Fatal("not silenced")
	}
	_ = r.f.Notify("Washer done", "Washer", "", "", "", "washer", 0)
	if n, _ := r.f.Card(); n.Title != "Washer" {
		t.Errorf("the card is %q, want the washer's: the quieted critical one held it back", n.Title)
	}
	if got := r.fired(); !same(got, []string{"shown", "replaced", "shown"}) {
		t.Errorf("events %v", got)
	}

	r = newRig(t, true)
	_ = bellFor(r)
	_ = r.f.NotifyCritical("Water under the sink", "Leak", "", "leak", 0, "", "", 0, 0, 0, 0)
	_ = r.f.silence()
	if err := r.f.NotifyPicture("Doorbell", "", "/local/door.png", "", "", "", "", 0); err != nil && strings.Contains(err.Error(), "critical") {
		t.Errorf("a picture was refused over a quieted critical card: %v", err)
	}
}

// A card that waited while a critical notification rang comes up once the bell is quieted, as one sent
// after the quieting does.
func TestAParkedCardComesUpWhenTheCriticalBellIsQuieted(t *testing.T) {
	r := newRig(t, true)
	_ = bellFor(r)
	_ = r.f.NotifyCritical("Water under the sink", "Leak", "", "leak", 0, "", "", 0, 0, 0, 0)
	_ = r.f.Notify("Washer done", "Washer", "", "", "", "washer", 0)
	if n, _ := r.f.Card(); n.Title != "Leak" {
		t.Fatalf("the card is %q while the bell rings", n.Title)
	}
	if !r.f.silence() {
		t.Fatal("not silenced")
	}
	waitFor(t, "the waiting card", func() bool { n, up := r.f.Card(); return up && n.Title == "Washer" })
}
