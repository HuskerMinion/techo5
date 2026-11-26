package serve

import (
	"log/slog"
	"sync"
	"time"
)

// The daemon's log is on the device's flash, and is rotated only when the daemon starts. Anything an
// add-on can make the server say, it can make it say as fast as it can connect or send, so nothing
// here is logged per connection or per message: each kind of line is said at most once a minute for
// each add-on, and the next time it is said it carries how many were left out in between.

// logEvery is how often one kind of line is said for one add-on.
var logEvery = time.Minute

// logLimit remembers, by add-on and kind of line, when it was last said and how many were left out.
type logLimit struct {
	mu   sync.Mutex
	last map[string]time.Time
	held map[string]int
}

// warn says a line, unless the same kind was said for the add-on within logEvery, in which case it
// is counted.
func (s *Server) warn(id, kind, msg string, args ...any) {
	now := s.Now()
	key := id + "\x00" + kind
	l := &s.logs
	l.mu.Lock()
	if l.last == nil {
		l.last, l.held = map[string]time.Time{}, map[string]int{}
	}
	if at, ok := l.last[key]; ok && now.Sub(at) < logEvery {
		l.held[key]++
		l.mu.Unlock()
		return
	}
	held := l.held[key]
	l.last[key], l.held[key] = now, 0
	l.mu.Unlock()
	args = append([]any{"addon", id}, args...)
	if held > 0 {
		args = append(args, "left_out_since_last", held)
	}
	slog.Warn(msg, args...)
}
