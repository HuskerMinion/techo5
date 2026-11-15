package voice

import (
	"context"
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"
)

type countingBackend struct {
	ha
	got [][]byte
}

func (b *countingBackend) Audio(frame []byte) error { b.got = append(b.got, frame); return nil }

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
	if last := be.got[len(be.got)-1]; len(last) != 4 || last[0] != 5 {
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
