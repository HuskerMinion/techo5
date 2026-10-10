package notify

import (
	"log/slog"
	"runtime/debug"
	"sync"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// An event for Home Assistant goes out to every connection, and a write to one that has died waits for
// the connection's write timeout, twenty seconds. Notifications post their events here and carry on,
// and one goroutine sends them, so a notification comes up, chimes and expires on time whatever Home
// Assistant is doing.
//
// Unlike a sensor, an event can't be folded into the latest one: an automation waits for shown, then
// dismissed, expired or replaced, so every one goes out, in order.

// outboxSize is how many events wait for a Home Assistant that has stopped reading. Past it the oldest
// is dropped: by then it is minutes stale, and memory on the device is not to be spent on a backlog.
const outboxSize = 64

type outbox struct {
	send func(component.Event)
	size int

	mu      sync.Mutex
	queue   []component.Event
	wake    chan struct{}
	started sync.Once
}

func newOutbox(send func(component.Event), size int) *outbox {
	return &outbox{send: send, size: size, wake: make(chan struct{}, 1)}
}

// post queues an event for sending and returns at once.
func (o *outbox) post(e component.Event) {
	o.started.Do(func() { safe.Go("notification events", o.run) })
	o.mu.Lock()
	if len(o.queue) >= o.size {
		slog.Warn("notification: Home Assistant isn't taking events; dropping the oldest",
			"event", o.queue[0].Data["event"], "id", o.queue[0].Data["id"])
		o.queue = o.queue[1:]
	}
	o.queue = append(o.queue, e)
	o.mu.Unlock()
	select {
	case o.wake <- struct{}{}:
	default:
	}
}

// queued is how many events are waiting to be sent.
func (o *outbox) queued() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.queue)
}

// run is the only thing that sends, oldest first, with no lock held while it does.
func (o *outbox) run() {
	for range o.wake {
		for {
			o.mu.Lock()
			if len(o.queue) == 0 {
				o.mu.Unlock()
				break
			}
			e := o.queue[0]
			o.queue = o.queue[1:]
			o.mu.Unlock()
			o.sendOne(e)
		}
	}
}

// sendOne sends one event. A panic in a send costs that event only: the sender goes on with the next,
// where letting it die would leave every later notification's events queued and dropped.
func (o *outbox) sendOne(e component.Event) {
	defer func() {
		if p := recover(); p != nil {
			slog.Error("notification: sending an event panicked", "event", e.Data["event"], "id", e.Data["id"],
				"panic", p, "stack", string(debug.Stack()))
		}
	}()
	o.send(e)
}
