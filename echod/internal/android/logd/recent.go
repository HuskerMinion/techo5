package logd

import (
	"strings"
	"sync"
)

// A TECHO5 Panel's log goes to logcat and nowhere else: nothing on a tablet starts the daemon with its
// output in a file, as an Echo's start script does. So the handler keeps the last of it here, for the
// diagnostics bundle (diag/bundle.go), which would otherwise have no log to put in. Bounded both ways:
// by lines, and by bytes, since one record can be a whole panic's stack.
const (
	recentLines = 2000
	recentBytes = 512 << 10

	// recentLineMax is the most of one line kept: a stack is still mostly there, and one line cannot
	// push the rest out.
	recentLineMax = 16 << 10
)

// recentLog is the last of the log, oldest first, as the lines read in a log file: the level's letter,
// then the line.
type recentLog struct {
	mu    sync.Mutex
	lines []string
	bytes int
}

var recent recentLog

func (r *recentLog) add(line string) {
	if len(line) > recentLineMax {
		line = strings.ToValidUTF8(line[:recentLineMax], "") + " …"
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, line)
	r.bytes += len(line)
	for len(r.lines) > recentLines || r.bytes > recentBytes {
		r.bytes -= len(r.lines[0])
		r.lines[0] = ""
		r.lines = r.lines[1:]
	}
}

// Recent is the last of the daemon's own log, oldest first, formatted as a log file has it. Only a
// panel keeps it (keepRecent); anywhere else it is empty, and the log is in its file.
func Recent() []string {
	recent.mu.Lock()
	defer recent.mu.Unlock()
	return append([]string(nil), recent.lines...)
}
