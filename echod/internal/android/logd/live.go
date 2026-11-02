package logd

import (
	"log/slog"
	"strings"
	"sync"
)

// The live log: the last of it in memory, and anything that wants to follow it from now on. The setup
// page's log view reads it. It is separate from Lines (one reader, taken by the API) and from Recent
// (kept on a panel only): it is kept everywhere, small, and any number of readers may follow it, each
// with its own buffer that loses the oldest rather than ever making logging wait.
const (
	liveLines   = 500
	liveBytes   = 128 << 10
	liveLineMax = 4 << 10

	// followDepth is how far one follower may fall behind before it loses lines.
	followDepth = 64
)

// Entry is one line of the live log. Seq counts up from 1, so a reader can tell a gap from quiet.
type Entry struct {
	Seq   uint64
	Level slog.Level
	Text  string
}

type liveLog struct {
	mu      sync.Mutex
	entries []Entry
	bytes   int
	seq     uint64
	subs    map[*Follower]struct{}
}

var live = &liveLog{subs: map[*Follower]struct{}{}}

// Follower receives the log from the moment it was made. C never blocks the logger: when it is full the
// oldest waiting entry is thrown away, and Lost says how many.
type Follower struct {
	C    chan Entry
	lost uint64
	log  *liveLog
}

func (l *liveLog) add(level slog.Level, text string) {
	if len(text) > liveLineMax {
		text = strings.ToValidUTF8(text[:liveLineMax], "") + " …"
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq++
	e := Entry{Seq: l.seq, Level: level, Text: text}
	l.entries = append(l.entries, e)
	l.bytes += len(text)
	for len(l.entries) > liveLines || l.bytes > liveBytes {
		l.bytes -= len(l.entries[0].Text)
		l.entries[0] = Entry{}
		l.entries = l.entries[1:]
	}
	for f := range l.subs {
		select {
		case f.C <- e:
			continue
		default:
		}
		// Full: make room by dropping the oldest, never by waiting. Only this goroutine sends under mu,
		// so the room is still there.
		select {
		case <-f.C:
			f.lost++
		default:
		}
		select {
		case f.C <- e:
		default:
			f.lost++
		}
	}
}

// Live is the last n lines of the log (all of those kept for n <= 0), oldest first, and a follower for
// everything after them, made under one lock so no line is in both or neither. Close the follower.
func Live(n int) ([]Entry, *Follower) {
	live.mu.Lock()
	defer live.mu.Unlock()
	f := &Follower{C: make(chan Entry, followDepth), log: live}
	live.subs[f] = struct{}{}
	return live.tail(n), f
}

// Recently is the last n lines, oldest first, without following.
func Recently(n int) []Entry {
	live.mu.Lock()
	defer live.mu.Unlock()
	return live.tail(n)
}

func (l *liveLog) tail(n int) []Entry {
	e := l.entries
	if n > 0 && len(e) > n {
		e = e[len(e)-n:]
	}
	return append([]Entry(nil), e...)
}

// Lost is how many entries this follower missed since it last asked, for saying so.
func (f *Follower) Lost() uint64 {
	f.log.mu.Lock()
	defer f.log.mu.Unlock()
	n := f.lost
	f.lost = 0
	return n
}

func (f *Follower) Close() {
	f.log.mu.Lock()
	delete(f.log.subs, f)
	f.log.mu.Unlock()
}
