//go:build linux

package serve

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A FIFO named as an add-on's sound is refused at once: opened without blocking, it is no regular file.
func TestAFIFOIsNoSound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ding.wav")
	if err := syscall.Mkfifo(path, 0o644); err != nil {
		t.Skip("no FIFO here:", err)
	}
	done := make(chan error, 1)
	go func() { _, err := readWAV(path); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a FIFO was read as a sound")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reading a FIFO hung")
	}
}
