package detect

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/wakeword"
)

// The board keeps what the words have been scoring, for the setup page's view of them (Scores). It holds
// numbers, never audio.
//
// The events are always kept, the last boardEvents of them: a detection, a near miss, a wake the device
// was unsure of, a word ready or dropped. They are rare, and the log has each one at the same line anyway,
// so a page opened after the word was said can still show what happened. The peaks from moment to moment
// are kept only while a view has asked for them in the last watchFor, and forgotten at the first score
// after that, so with nobody watching, a score costs two atomic loads and nothing is kept.
//
// It is fed with the engine's lock held, and only copies: it never calls back into the engine. Its own
// lock is taken after the engine's, never before, and Scores never holds the two at once.
const (
	// bucketMs is how much time one peak stands for. Fifty scores a second are too many to send to a
	// page and too many to read; the highest of each quarter second is what a person watching sees.
	bucketMs = 250
	// boardBuckets is thirty seconds of them, as far back as a view's trace goes.
	boardBuckets = 120
	boardEvents  = 64
	// watchFor is how long a view's read keeps the peaks being kept. It polls well inside it.
	watchFor = 10 * time.Second
)

type board struct {
	// watchedUntil is when the latest view's read stops keeping the peaks, in Unix nanoseconds, and
	// kept is whether any peaks are held, so the first score after a watch lapses forgets them.
	watchedUntil atomic.Int64
	kept         atomic.Bool

	mu     sync.Mutex
	seq    uint64
	events [boardEvents]wakeword.ScoreEvent
	// traces is each slot's peaks, made the first time the slot scores while watched: the engine has a
	// slot for every index up to the command words', and only a few of them are ever loaded.
	traces map[int]*trace
}

// trace is a slot's peaks, one entry per bucket, reused as the buckets come round again.
type trace [boardBuckets]struct {
	at       int64 // the bucket this entry holds, so one left from a lap ago is not read as this one
	best     float32
	settling bool
}

// bucketOf is the bucket a moment falls in.
func bucketOf(t time.Time) int64 { return t.UnixMilli() / bucketMs }

// watch keeps the peaks being kept for watchFor from now.
func (b *board) watch(now time.Time) { b.watchedUntil.Store(now.Add(watchFor).UnixNano()) }

// heard is a fresh score of slot n. settling is the slot's detections being held back as it came. Held
// with the engine's lock.
func (b *board) heard(n int, score float64, now time.Time, settling bool) {
	if now.UnixNano() >= b.watchedUntil.Load() {
		if b.kept.Load() {
			b.mu.Lock()
			b.traces = nil
			b.kept.Store(false)
			b.mu.Unlock()
		}
		return
	}
	at := bucketOf(now)

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.traces == nil {
		b.traces = map[int]*trace{}
	}
	t := b.traces[n]
	if t == nil {
		t = new(trace)
		b.traces[n] = t
	}
	b.kept.Store(true)
	e := &t[at%boardBuckets]
	if e.at != at {
		e.at, e.best, e.settling = at, float32(score), settling
		return
	}
	e.best = max(e.best, float32(score))
	e.settling = e.settling || settling
}

// forget drops a slot's peaks as another word goes into it, or none does, so the new word's row does not
// show what the old one scored. Held with the engine's lock.
func (b *board) forget(n int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.traces, n)
}

// forgetAll drops every slot's peaks, as the engine empties its slots. Held with the engine's lock.
func (b *board) forgetAll() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.traces = nil
	b.kept.Store(false)
}

// event is one of the log's lines about a word. Held with the engine's lock.
func (b *board) event(ev wakeword.ScoreEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seq++
	ev.Seq = b.seq
	b.events[b.seq%boardEvents] = ev
}

// read is a copy of what the board holds for a view: the events after one already seen, and the peaks
// of the given slots from a bucket on.
type read struct {
	events        []wakeword.ScoreEvent
	best          map[int][]float32
	settling      map[int][]bool
	first, newest int64
	lost          bool
}

func (b *board) read(after uint64, from int64, now time.Time, slots []int) read {
	r := read{newest: bucketOf(now), best: map[int][]float32{}, settling: map[int][]bool{}}
	oldest := r.newest - boardBuckets + 1
	switch {
	case from <= 0:
		r.first = oldest
	case from < oldest:
		r.first, r.lost = oldest, true
	case from > r.newest:
		r.first = r.newest + 1
	default:
		r.first = from
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.seq > 0 {
		first := uint64(1)
		if b.seq > boardEvents {
			first = b.seq - boardEvents + 1
		}
		if after > 0 && after+1 < first {
			r.lost = true
		}
		for s := max(after+1, first); s <= b.seq; s++ {
			r.events = append(r.events, b.events[s%boardEvents])
		}
	}

	for _, n := range slots {
		best := make([]float32, r.newest-r.first+1)
		settling := make([]bool, len(best))
		if t := b.traces[n]; t != nil {
			for k := r.first; k <= r.newest; k++ {
				if e := t[k%boardBuckets]; e.at == k {
					best[k-r.first], settling[k-r.first] = e.best, e.settling
				}
			}
		}
		r.best[n], r.settling[n] = best, settling
	}
	return r
}
