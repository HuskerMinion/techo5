package media

import (
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// pipeSrc is a received track's source over an os.Pipe.
type pipeSrc struct{ *os.File }

func (p pipeSrc) SetReadDeadline(t time.Time) error { return p.File.SetReadDeadline(t) }

// How far into a received track the room has heard follows what went into the queue, less the card's
// latency, and only for the track that is loaded.
func TestHeardFollowsTheReceivedTrack(t *testing.T) {
	s := NewStream(speaker.NewDriver(speaker.New()), speaker.New(), func() {}, func(string) {})
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if _, ok := s.Heard("Video"); ok {
		t.Fatal("heard a track that is not playing")
	}
	s.PlayPCM("Video", pipeSrc{r}, speaker.Rate, 2)
	// Half a second of stereo.
	go func() { _, _ = w.Write(make([]byte, speaker.Rate/2*4)) }()
	want := time.Second/2 - speaker.OutputLatency
	deadline := time.Now().Add(5 * time.Second)
	for {
		at, ok := s.Heard("Video")
		if ok && at == want {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("heard %v (%v), want %v", at, ok, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, ok := s.Heard("Bluetooth"); ok {
		t.Error("heard a track by another name")
	}
	s.Stop()
	if _, ok := s.Heard("Video"); ok {
		t.Error("heard a track that was stopped")
	}
}

// Samples that arrive while the track is held up are kept for it, not dropped: dropped, a video's
// sound would run ahead of its picture by them.
func TestAHeldTrackKeepsWhatArrives(t *testing.T) {
	s := NewStream(speaker.NewDriver(speaker.New()), speaker.New(), func() {}, func(string) {})
	tr, _ := s.start(&track{item: "Video", received: true})
	s.Pause()
	if s.offer(tr, make([]int16, 100)) {
		t.Fatal("a paused track took samples")
	}
	s.Unpause()
	if !s.offer(tr, make([]int16, 100)) || tr.queued != 100 {
		t.Fatalf("queued %d", tr.queued)
	}
	s.Stop()
	if !s.offer(tr, make([]int16, 100)) || tr.queued != 100 {
		t.Error("a track that is over took samples, or held them")
	}
}

// What arrives while a received track is held up is dropped, as it always was, for every received
// track but one marked KeepWhileHeld (the video's), whose audio waits and is played.
func TestOnlyTheVideosAudioIsKeptWhileHeld(t *testing.T) {
	KeepWhileHeld("Video")
	type offer struct {
		item string
		ok   bool
	}
	offers := make(chan offer, 16)
	hook := func(item string, ok bool) { offers <- offer{item, ok} }
	offeredHook.Store(&hook)
	t.Cleanup(func() { offeredHook.Store(nil) })
	next := func(name string) bool {
		select {
		case o := <-offers:
			if o.item != name {
				t.Fatalf("%s: an offer from %s", name, o.item)
			}
			return o.ok
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: nothing offered", name)
			return false
		}
	}
	for _, tc := range []struct {
		name string
		kept bool
	}{{"Bluetooth", false}, {"AirPlay", false}, {"DLNA", false}, {"Video", true}} {
		s := NewStream(speaker.NewDriver(speaker.New()), speaker.New(), func() {}, func(string) {})
		src := newChanSrc()
		s.PlayPCM(tc.name, src, speaker.Rate, 2)
		<-src.reads // the track waiting on its first read
		s.Pause()
		src.data <- make([]byte, 4800*4)
		// Read while paused, the audio is offered and turned away: dropped, or kept for later.
		if next(tc.name) {
			t.Fatalf("%s: queued while paused", tc.name)
		}
		s.Unpause()
		if tc.kept && !next(tc.name) {
			t.Errorf("%s: turned away again after the pause", tc.name)
		}
		if !tc.kept {
			<-src.reads // on to the next read: nothing more of it is coming
		}
		s.mu.Lock()
		queued := uint64(0)
		if s.track != nil {
			queued = s.track.queued
		}
		s.mu.Unlock()
		if got := queued > 0; got != tc.kept {
			t.Errorf("%s: kept %v (queued %d), want %v", tc.name, got, queued, tc.kept)
		}
		s.Stop()
		_ = src.Close()
	}
}

// chanSrc is a received track's source the test feeds a read at a time, telling it each time a read
// begins.
type chanSrc struct {
	data   chan []byte
	reads  chan struct{}
	closed chan struct{}
	once   sync.Once
}

func newChanSrc() *chanSrc {
	return &chanSrc{data: make(chan []byte), reads: make(chan struct{}, 16), closed: make(chan struct{})}
}

func (c *chanSrc) Read(p []byte) (int, error) {
	c.reads <- struct{}{}
	select {
	case b := <-c.data:
		return copy(p, b), nil
	case <-c.closed:
		return 0, io.EOF
	}
}

func (c *chanSrc) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

func (c *chanSrc) SetReadDeadline(time.Time) error { return nil }
