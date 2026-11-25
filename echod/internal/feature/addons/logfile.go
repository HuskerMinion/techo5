package addons

import (
	"bytes"
	"io"
	"os"
	"strings"
	"sync"
)

// An add-on's log: what it writes on its standard output and error, kept in its folder, capped and
// rotated while it runs. The eMMC audit's lesson holds here as for the daemon's own log: a program that
// logs a line a second writes the flash for as long as it runs, so the file is bounded (maxLog, and one
// older file beside it), and nothing is synced per line. The kernel writes it back when it would anyway;
// a power cut loses the last few seconds of an add-on's log, which is a fair price.

// maxLog is how large the log grows before it is moved aside to log.1, which it replaces.
var maxLog int64 = 256 << 10

type logFile struct {
	path string

	mu   sync.Mutex
	f    *os.File
	size int64
}

func openLog(path string) (*logFile, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	return &logFile{path: path, f: f, size: st.Size()}, nil
}

// Write appends, and moves the file aside first once it has reached maxLog. A write is never split
// across the two: a line stays whole in one file or the other.
func (l *logFile) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return len(p), nil
	}
	if l.size > 0 && l.size+int64(len(p)) > maxLog {
		l.f.Close()
		_ = os.Rename(l.path, l.path+".1")
		f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
		if err != nil {
			l.f = nil
			return len(p), nil // the add-on is not stopped for a log that cannot be written
		}
		l.f, l.size = f, 0
	}
	n, err := l.f.Write(p)
	l.size += int64(n)
	if err != nil {
		return len(p), nil
	}
	return n, nil
}

// Note is a line of the supervisor's own in the log, so the log says when it was started and stopped.
func (l *logFile) Note(line string) { _, _ = l.Write([]byte("[techo5] " + line + "\n")) }

func (l *logFile) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}

// tailLog is the last n lines of an add-on's log, reaching into log.1 when the current file is short.
func tailLog(path string, n int) []string {
	read := func(p string) []string {
		f, err := os.Open(p)
		if err != nil {
			return nil
		}
		defer f.Close()
		// The end of the file is all that is wanted: a few lines are a few kilobytes.
		const back = 32 << 10
		if st, err := f.Stat(); err == nil && st.Size() > back {
			_, _ = f.Seek(-back, io.SeekEnd)
		}
		b, _ := io.ReadAll(f)
		b = bytes.TrimRight(b, "\n")
		if len(b) == 0 {
			return nil
		}
		return strings.Split(string(b), "\n")
	}
	lines := read(path)
	if len(lines) < n {
		lines = append(read(path+".1"), lines...)
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}
