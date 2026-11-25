package maintenance

import (
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// urgentGap is the least time between two flushes asked for by warnings. A warning on its own
// reaches flash as soon as it is written; one that repeats every second, as a lost connection's can,
// is flushed at most every five seconds, which is no more than the global sync this replaced did.
const urgentGap = 5 * time.Second

// logFile is the daemon's log as the flusher sees it: how long it is, and a way to push it to flash.
type logFile interface {
	size() int64
	sync()
}

// logFlush decides when the daemon's log is pushed out to flash. See Maintenance's Run.
type logFlush struct {
	log logFile // nil when the log is not a file, and there is nothing to flush

	synced  time.Time // when it was last flushed for a warning
	flushed int64     // its size when it was last flushed
}

func newLogFlush(log logFile) *logFlush {
	l := &logFlush{log: log}
	if log != nil {
		// What is already in it was written before this daemon started, by the script that started
		// it or by the daemon before it, which flushed its own.
		l.flushed = log.size()
	}
	return l
}

// flush pushes the log out if it has grown, or shrunk (the Dot's techo5-logs empties a log in
// place), since the last time. A sync with nothing written is cheap, but not free on every board.
func (l *logFlush) flush() {
	if l.log == nil {
		return
	}
	if n := l.log.size(); n != l.flushed {
		l.log.sync()
		l.flushed = n
	}
}

// warned flushes for a warning or an error, at once unless the last one was under urgentGap ago. It
// returns how long to wait before asking again, or zero when it has flushed.
func (l *logFlush) warned(now time.Time) time.Duration {
	if wait := urgentGap - now.Sub(l.synced); !l.synced.IsZero() && wait > 0 {
		return wait
	}
	l.flush()
	l.synced = now
	return 0
}

// stdLog is the daemon's standard output and error, which techo5-run sends to its log file, as long
// as they are regular files: under Fire OS's init, or in the Panel's app, they are /dev/null or a
// pipe, and there is nothing to flush. Both are kept if they are different files.
type stdLog []*os.File

func openStdLog() logFile {
	var out stdLog
	var seen []os.FileInfo
	for _, f := range []*os.File{os.Stderr, os.Stdout} {
		fi, err := f.Stat()
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		dup := false
		for _, s := range seen {
			dup = dup || os.SameFile(s, fi)
		}
		if !dup {
			seen = append(seen, fi)
			out = append(out, f)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (s stdLog) size() int64 {
	var n int64
	for _, f := range s {
		if fi, err := f.Stat(); err == nil {
			n += fi.Size()
		}
	}
	return n
}

// sync is fdatasync: the file's data and its length, without the times that change on every write.
func (s stdLog) sync() {
	for _, f := range s {
		_ = unix.Fdatasync(int(f.Fd()))
	}
}
