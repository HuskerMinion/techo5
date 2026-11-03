package bluetooth

import (
	"log/slog"
	"sync"

	"google.golang.org/protobuf/proto"
)

// subscriber is a Home Assistant connection as the takeover watch sees it.
type subscriber interface {
	Send(proto.Message) error
	ClientInfo() string
}

// takeover notices a second Home Assistant taking the advertisement subscription from one that is
// still connected. The library keeps a single subscriber (the last to subscribe), so a device added
// to two Home Assistant instances gives one of them every advertisement and the other none, with
// nothing on either side to say why. This says it once in the log.
//
// It watches the subscribe and unsubscribe messages on their way to the library, which is as close
// as the library lets echod get: it offers OnSubscribed with a bool only, no connection. The library
// does not expose a connection's remote address either, so the peers are named by what they said
// about themselves in HelloRequest and by the order they subscribed in.
type takeover struct {
	mu   sync.Mutex
	cur  subscriber
	seq  int // how many subscriptions have been taken, to tell connections with the same client info apart
	curN int
}

// note records a subscribe (want) or unsubscribe message from c, before the library handles it. free is
// the free-slots reply as it stands, sent to the previous subscriber to learn whether it is still there.
func (t *takeover) note(c subscriber, want bool, free func() proto.Message) {
	t.mu.Lock()
	prev, prevN := t.cur, t.curN
	switch {
	case want && prev == c:
		t.mu.Unlock()
		return
	case want:
		t.seq++
		t.cur, t.curN = c, t.seq
	case prev == c:
		t.cur = nil
	}
	n := t.seq
	t.mu.Unlock()

	if !want || prev == nil {
		return
	}
	// The library says nothing when a connection closes; a send to it fails. The free-slots reply, with
	// the slots as they are, only tells it again what it already knows.
	if prev.Send(free()) != nil {
		return
	}
	slog.Warn("ble: a second Home Assistant took over the Bluetooth proxy; the first still connected gets no advertisements now",
		"new", c.ClientInfo(), "new_subscription", n,
		"previous", prev.ClientInfo(), "previous_subscription", prevN)
}
