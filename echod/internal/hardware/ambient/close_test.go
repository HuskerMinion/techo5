//go:build !dot

package ambient

import (
	"context"
	"sync"
	"testing"
	"time"
)

// watchedPoller is a sensor that says whether it was read after it was closed.
type watchedPoller struct {
	mu       sync.Mutex
	reads    int
	closed   bool
	lateRead bool
}

func (w *watchedPoller) sample() (float64, bool, error) {
	w.mu.Lock()
	w.reads++
	if w.closed {
		w.lateRead = true
	}
	w.mu.Unlock()
	time.Sleep(20 * time.Millisecond) // a reading takes a while on the bus
	w.mu.Lock()
	if w.closed {
		w.lateRead = true
	}
	w.mu.Unlock()
	return 10, true, nil
}

func (w *watchedPoller) close() error {
	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()
	return nil
}

// Close while Run polls stops the poll first: no reading is under way, or starts, once the sensor
// (the Skylight's I2C bus) is closed.
func TestCloseStopsThePollFirst(t *testing.T) {
	w := &watchedPoller{}
	s := &Sensor{poll: w}
	done := make(chan error, 1)
	go func() { done <- s.Run(context.Background()) }()
	for {
		w.mu.Lock()
		n := w.reads
		w.mu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run ended with %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run went on after Close")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.lateRead {
		t.Error("the sensor was read after it was closed")
	}
}
