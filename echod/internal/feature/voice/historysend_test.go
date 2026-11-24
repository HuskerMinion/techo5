package voice

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/mic"
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

// noiseMessage is the most one encrypted message to Home Assistant carries (go-esphome-device's
// maxNoisePayload); the wire refuses a larger one rather than splitting it.
const noiseMessage = 65515

// A wake held for the house's decision has the longest history a turn sends, mic.Held: three seconds,
// 96 KB. It goes up in pieces each well inside one encrypted message, all of it and in order, through
// a queue the size of the real one with Home Assistant reading as it fills (#728).
func TestALongHeldHistoryGoesInFramesUnderTheLimit(t *testing.T) {
	c := newConversation(&esphome.VoiceSatellite{})
	be := &countingBackend{}
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case fn := <-c.out:
				_ = fn()
			case <-ctx.Done():
				for len(c.out) > 0 {
					_ = (<-c.out)()
				}
				return
			}
		}
	}()

	history := make([]byte, 2*mic.Rate*int(mic.Held/time.Millisecond)/1000)
	for i := range history {
		history[i] = byte(i * 7)
	}
	c.sendHistory(context.Background(), be, history)
	stop()
	<-done

	if n := c.audioDropped.Load(); n != 0 {
		t.Fatalf("dropped %d pieces of the history", n)
	}
	be.mu.Lock()
	defer be.mu.Unlock()
	if len(be.got) < 2 {
		t.Fatalf("%d bytes of history went as %d message(s), want it split", len(history), len(be.got))
	}
	var joined []byte
	for i, m := range be.got {
		// Room for the message's own framing around the samples.
		if len(m)+16 > noiseMessage {
			t.Errorf("message %d is %d bytes, too large for one noise frame of %d", i, len(m), noiseMessage)
		}
		joined = append(joined, m...)
	}
	if !bytes.Equal(joined, history) {
		t.Fatalf("the pieces do not make up the history: %d bytes back of %d", len(joined), len(history))
	}
}
