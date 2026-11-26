package addons

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// The real spawn: a program in its own process group, stopped with what it started when asked, and its
// output in the log it was given.
func TestSpawnStops(t *testing.T) {
	out := &syncWriter{}
	p, err := spawn(spec{Path: "/bin/sh", Args: []string{"-c", "echo up; sleep 30 & wait"}, Dir: t.TempDir(), Out: out})
	if err != nil {
		t.Skip("no /bin/sh here:", err)
	}
	// A real shell, so a busy machine running every package's tests at once may take a while to start
	// it: the wait for its first line is generous, and only the stop is timed.
	for end := time.Now().Add(30 * time.Second); !strings.Contains(out.String(), "up"); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(end) {
			p.Stop()
			t.Fatal("waited too long for its first line")
		}
	}
	start := time.Now()
	p.Stop()
	_ = p.Wait()
	if time.Since(start) > 3*time.Second {
		t.Error("Stop did not stop it promptly")
	}
}

type syncWriter struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncWriter) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}
