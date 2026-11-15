package setup

import (
	"context"
	"crypto/sha256"
	"strings"
	"sync"
	"time"
)

// Two sections say what a server of the owner's says: Music Assistant whether this device is a player
// in it and which services it has, Immich which albums it has. They used to ask as the tab was drawn,
// for up to eight seconds between them, on every draw of the tab, and a server that was down held the
// whole page up each time. They ask through a lookup now: the answer is kept for a short while, asked
// for once however many draws want it, and a draw waits for it only briefly. One not back by then is
// said to be on its way, and the next draw has it.

// lookupFresh is how long an answer is used before it is asked for again. Short: what it says, a player
// connected or not, can change, and a draw a minute later should see that.
const lookupFresh = 30 * time.Second

// lookupWait is how long a draw waits for an answer it does not have yet. A server on the same network
// answers well within it; one that does not is not waited for. A variable for the tests.
var lookupWait = time.Second

// lookup is one server's answer, kept. T is what a section makes of it.
type lookup[T any] struct {
	ask     time.Duration // the most one asking may take: the client's own bound
	mu      sync.Mutex
	key     [32]byte // the server asked, and how: what the kept answer is for
	at      time.Time
	val     T
	asking  chan struct{} // closed when the asking under way ends; nil when none is
	askedOf [32]byte      // what that asking is for
}

// lookupKey is what an answer is for: the server and its secret, as one value. Hashed, so that the key
// kept beside an answer is not a second copy of the secret.
func lookupKey(parts ...string) [32]byte {
	return sha256.Sum256([]byte(strings.Join(parts, "\x00")))
}

// get is the answer for key: the kept one while it is fresh, or a new one asked for with fetch and
// waited for no longer than lookupWait. ok is false when there is none yet; a kept one for the same key
// that is no longer fresh is used meanwhile. ctx ends the wait early, never the asking: an answer that
// arrives after the page was sent is kept for the next draw.
func (l *lookup[T]) get(ctx context.Context, key [32]byte, fetch func(context.Context) T) (val T, ok bool) {
	l.mu.Lock()
	if l.key == key && !l.at.IsZero() && time.Since(l.at) < lookupFresh {
		val = l.val
		l.mu.Unlock()
		return val, true
	}
	done := l.asking
	if done == nil || l.askedOf != key {
		done = make(chan struct{})
		l.asking, l.askedOf = done, key
		go l.fetch(key, done, fetch)
	}
	l.mu.Unlock()

	t := time.NewTimer(lookupWait)
	defer t.Stop()
	select {
	case <-done:
	case <-t.C:
	case <-ctx.Done():
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.key == key && !l.at.IsZero() {
		return l.val, true
	}
	return val, false
}

// fetch asks, bounded by the lookup's own time, and keeps the answer.
func (l *lookup[T]) fetch(key [32]byte, done chan struct{}, fetch func(context.Context) T) {
	defer close(done)
	ctx, cancel := context.WithTimeout(context.Background(), l.ask)
	defer cancel()
	v := fetch(ctx)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.asking == done {
		l.asking = nil
	}
	if l.askedOf == key { // not since asked of some other server
		l.key, l.at, l.val = key, time.Now(), v
	}
}

// forget drops the kept answer, after a save that may change it.
func (l *lookup[T]) forget() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.at = time.Time{}
	l.askedOf = [32]byte{}
}
