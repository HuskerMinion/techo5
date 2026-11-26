//go:build !dot && !spot && !panel

package mic

import (
	"encoding/binary"
	"errors"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/alsa"
	"github.com/HuskerMinion/techo5/echod/internal/lib/echoref"
)

// fakeDelayed is a capture that fills every read with the same frame and has nothing waiting.
type fakeDelayed struct {
	frame    []byte
	delayErr error
}

func (f *fakeDelayed) Read(buf []byte) (int, error) {
	n := len(buf) / len(f.frame) * len(f.frame)
	for at := 0; at < n; at += len(f.frame) {
		copy(buf[at:], f.frame)
	}
	return n, nil
}

func (f *fakeDelayed) Close() error        { return nil }
func (f *fakeDelayed) Delay() (int, error) { return 0, f.delayErr }
func (f *fakeDelayed) Config() alsa.Config { return alsa.Config{Channels: Channels} }

func s24(b []byte) int32 { return int32(uint32(b[0])|uint32(b[1])<<8|uint32(b[2])<<16) << 8 >> 8 }

// The microphones come through as they were read, and the loopback channels carry the speaker's
// audio, left and right, where the card's loopback was; with nothing to place a read by, silence.
func TestSoftRefReplacesTheLoopback(t *testing.T) {
	frameBytes := Channels * Bits / 8
	frame := make([]byte, frameBytes)
	for ch := range Channels {
		putS24(frame[3*ch:], 0.25) // the loopback channels too: the card's, to be replaced
	}

	// The speaker has been playing a steady -6 dBFS on the left and silence on the right for the last
	// second and goes on for a while yet: the pushes are stamped ahead, so the read below finds audio
	// however long the test takes to reach it.
	const rate, period = 48000, 768
	pcm := make([]byte, period*4)
	for i := range period {
		binary.LittleEndian.PutUint16(pcm[4*i:], uint16(int16(16384)))
	}
	echoref.Use(true)
	defer echoref.Use(false)
	end := time.Now().Add(500 * time.Millisecond)
	for n := range 94 {
		echoref.Push(pcm, rate, end.Add(-time.Duration(93-n)*period*time.Second/rate))
	}

	fake := &fakeDelayed{frame: frame}
	c := &softRefCapture{delayedCapture: fake, clock: echoref.NewClock(Rate)}
	buf := make([]byte, FrameSamples*frameBytes)
	// The read is placed at its last frame, now, well inside what was pushed.
	if _, err := c.Read(buf); err != nil {
		t.Fatal(err)
	}
	mic, left, right := s24(buf[0:]), s24(buf[3*RefFirst:]), s24(buf[3*(RefFirst+1):])
	if want := s24(frame); mic != want {
		t.Errorf("microphone sample %d, read %d", mic, want)
	}
	if want := int32(1 << 22); left < want*9/10 || left > want*11/10 {
		t.Errorf("left reference %d, want about %d (the speaker's -6 dBFS)", left, want)
	}
	if right != 0 {
		t.Errorf("right reference %d, want 0: the speaker's right was silent", right)
	}

	if _, ok := capture(c).(interface{ Config() alsa.Config }); !ok {
		t.Error("the wrapper hides Config, which the External ASP's native path reads")
	}

	fake.delayErr = errors.New("no delay")
	if _, err := c.Read(buf); err != nil {
		t.Fatal(err)
	}
	for f := range FrameSamples {
		if l := s24(buf[f*frameBytes+3*RefFirst:]); l != 0 {
			t.Fatalf("frame %d: reference %d without a delay to place the read by, want silence", f, l)
		}
	}
}
