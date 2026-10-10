package notify

import (
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/component"
)

// sink is a sender that records what it is given, and can be held so a send waits the way one to a dead
// Home Assistant connection does.
type sink struct {
	mu   sync.Mutex
	got  []string
	hold chan struct{} // closed to let held sends through; nil sends at once
}

func (s *sink) send(e component.Event) {
	if s.hold != nil {
		<-s.hold
	}
	s.mu.Lock()
	s.got = append(s.got, e.Data["event"]+":"+e.Data["id"])
	s.mu.Unlock()
}

func (s *sink) sent() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.got...)
}

func event(what, id string) component.Event {
	return component.Event{Name: Event, Data: map[string]string{"event": what, "id": id}}
}

// Posting never waits on the send, however long the send takes.
func TestPostingDoesNotWaitOnTheSend(t *testing.T) {
	s := &sink{hold: make(chan struct{})}
	defer close(s.hold)
	o := newOutbox(s.send, 64)
	done := make(chan struct{})
	go func() {
		for i := 0; i < 10; i++ {
			o.post(event("shown", strconv.Itoa(i)))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("posting waited on a send that is held")
	}
}

// Events go out one at a time, in the order they were posted.
func TestEventsGoOutInOrder(t *testing.T) {
	s := &sink{}
	o := newOutbox(s.send, 64)
	var want []string
	for i := 0; i < 20; i++ {
		what := []string{"shown", "replaced", "dismissed", "expired"}[i%4]
		o.post(event(what, strconv.Itoa(i)))
		want = append(want, what+":"+strconv.Itoa(i))
	}
	waitFor(t, "every event", func() bool { return len(s.sent()) == len(want) })
	if got := s.sent(); !same(got, want) {
		t.Errorf("sent %v, want %v", got, want)
	}
}

// While a send is held, the outbox keeps at most its limit, dropping the oldest, so a Home Assistant
// that stays away can't grow it without bound.
func TestAFullOutboxDropsTheOldest(t *testing.T) {
	s := &sink{hold: make(chan struct{})}
	o := newOutbox(s.send, 3)
	o.post(event("shown", "0")) // taken by the sender, which then waits in the send
	waitFor(t, "the first event to be taken", func() bool { return o.queued() == 0 })
	for i := 1; i <= 5; i++ {
		o.post(event("shown", strconv.Itoa(i)))
	}
	if n := o.queued(); n != 3 {
		t.Fatalf("%d queued, want the limit of 3", n)
	}
	close(s.hold)
	waitFor(t, "the queue to drain", func() bool { return len(s.sent()) == 4 })
	if got, want := s.sent(), []string{"shown:0", "shown:3", "shown:4", "shown:5"}; !same(got, want) {
		t.Errorf("sent %v, want %v (the oldest dropped)", got, want)
	}
}

// With the real outbox behind it, a notification comes up, redraws and can be replaced while the send
// to Home Assistant is held, and the events still go out in order once it is let through.
func TestANotificationDoesNotWaitOnHomeAssistant(t *testing.T) {
	r := newRig(t, true)
	s := &sink{hold: make(chan struct{})}
	r.f.fire = newOutbox(s.send, 64).post
	var redraws atomic.Int32
	stop := r.f.Changed.Listen(func(struct{}) { redraws.Add(1) })
	defer stop()

	done := make(chan struct{})
	go func() {
		_ = r.f.Notify("first", "", "", "", "", "", 0)
		_ = r.f.Notify("second", "", "", "", "", "", 0)
		r.f.tick(r.now.Add(13 * time.Hour))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("notifying waited on Home Assistant")
	}
	if n := redraws.Load(); n != 3 {
		t.Errorf("%d redraws while Home Assistant was held, want 3 (two shown, one expired)", n)
	}
	close(s.hold)
	waitFor(t, "the events", func() bool { return len(s.sent()) == 4 })
	if got, want := s.sent(), []string{"shown:1", "replaced:1", "shown:2", "expired:2"}; !same(got, want) {
		t.Errorf("sent %v, want %v", got, want)
	}
}

// The feature as built for the device sends its events through an outbox: with whatever is listening
// on component.Fire held (as the API server is, writing to a dead connection), Notify still returns.
func TestTheFeatureSendsThroughAnOutbox(t *testing.T) {
	r := newRig(t, true)
	f := newFeature(r.pics)
	f.now, f.chime, f.say, f.hushed = r.f.now, func() {}, func(string) {}, func() bool { return true }
	hold := make(chan struct{})
	var got atomic.Int32
	stop := component.Fire.Listen(func(e component.Event) {
		if e.Name == Event {
			<-hold
			got.Add(1)
		}
	})
	defer stop()
	done := make(chan struct{})
	go func() {
		_ = f.Notify("one", "", "", "", "", "", 0)
		_ = f.Notify("two", "", "", "", "", "", 0)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Notify waited on the event being sent")
	}
	close(hold)
	waitFor(t, "the events to be sent", func() bool { return got.Load() == 3 })
}

// A send that panics loses that event alone; the ones after it still go out.
func TestAPanickingSendLosesOnlyItsEvent(t *testing.T) {
	s := &sink{}
	o := newOutbox(func(e component.Event) {
		if e.Data["id"] == "2" {
			panic("a bad send")
		}
		s.send(e)
	}, 64)
	for i := 1; i <= 3; i++ {
		o.post(event("shown", strconv.Itoa(i)))
	}
	waitFor(t, "the events after the bad one", func() bool { return len(s.sent()) == 2 })
	if got, want := s.sent(), []string{"shown:1", "shown:3"}; !same(got, want) {
		t.Errorf("sent %v, want %v", got, want)
	}
}
