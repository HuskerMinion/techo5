package voice

import (
	"context"
	"sync"
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"
)

// countingBackend keeps the frames sent, from whichever goroutine sends them.
type countingBackend struct {
	ha
	mu  sync.Mutex
	got [][]byte
}

func (b *countingBackend) Audio(frame []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.got = append(b.got, frame)
	return nil
}

func (b *countingBackend) last() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.got[len(b.got)-1]
}

// A turn's history waits for room in a full queue rather than being dropped as a frame is: it is the
// start of the request (#477). One that never finds room is counted as dropped.
func TestATurnsHistoryWaitsForRoom(t *testing.T) {
	c := newConversation(&esphome.VoiceSatellite{})
	be := &countingBackend{}
	for range outDepth {
		c.sendAudio(be, []byte{1, 2})
	}
	c.sendAudio(be, []byte{3, 4})
	if c.audioDropped.Load() != 1 {
		t.Fatalf("dropped %d frames on a full queue, want 1", c.audioDropped.Load())
	}

	go func() {
		time.Sleep(historyWait / 5)
		_ = (<-c.out)()
	}()
	c.sendHistory(context.Background(), be, []byte{5, 6, 7, 8})
	if c.audioDropped.Load() != 1 {
		t.Fatal("the history was dropped although room came within historyWait")
	}
	for len(c.out) > 0 {
		_ = (<-c.out)()
	}
	if last := be.last(); len(last) != 4 || last[0] != 5 {
		t.Fatalf("the history was not the last thing sent: %v", last)
	}

	for range outDepth {
		c.sendAudio(be, []byte{1, 2})
	}
	c.sendHistory(context.Background(), be, []byte{5, 6})
	if c.audioDropped.Load() != 2 {
		t.Fatalf("dropped %d, want the history that found no room counted", c.audioDropped.Load())
	}
}
