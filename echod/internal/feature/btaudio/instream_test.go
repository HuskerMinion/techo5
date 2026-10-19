//go:build linux

package btaudio

import (
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/bluealsa"
)

// fakeStream is a phone's stream as read reads it: each read takes from the next step, at most what
// the buffer holds, and a step of no data is a read that waited readWait for nothing. Past the last
// step the phone has gone. The clock moves by what was read, at 44.1 kHz stereo.
type fakeStream struct {
	steps [][]byte
	now   time.Time
}

func (p *fakeStream) SetReadDeadline(time.Time) error { return nil }

func (p *fakeStream) Read(b []byte) (int, error) {
	if len(p.steps) == 0 {
		return 0, io.EOF
	}
	if len(p.steps[0]) == 0 {
		p.steps = p.steps[1:]
		p.now = p.now.Add(readWait)
		return 0, os.ErrDeadlineExceeded
	}
	n := copy(b, p.steps[0])
	if p.steps[0] = p.steps[0][n:]; len(p.steps[0]) == 0 {
		p.steps = p.steps[1:]
	}
	p.now = p.now.Add(time.Duration(n/4) * time.Second / 44100)
	return n, nil
}

func (p *fakeStream) clock() time.Time { return p.now }

// stereo is d of 44.1 kHz stereo S16_LE at level, read 4096 bytes at a time.
func stereo(level int16, d time.Duration) [][]byte {
	total := int(d*44100/time.Second) * 4
	var out [][]byte
	for total > 0 {
		n := min(total, 4096)
		chunk := make([]byte, n)
		for i := 0; i+1 < n; i += 2 {
			chunk[i], chunk[i+1] = byte(uint16(level)), byte(uint16(level)>>8)
		}
		out = append(out, chunk)
		total -= n
	}
	return out
}

// clockAt is where the next readAll starts its clock.
var (
	clockMu sync.Mutex
	clockAt = time.Unix(1000, 0)
)

func idle(d time.Duration) [][]byte { return make([][]byte, int(d/readWait)) }

func concat(parts ...[][]byte) [][]byte {
	var out [][]byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// readAll reads steps through s, recording each time the phone was heard to start or stop playing.
func readAll(s *inStream, steps [][]byte) (turns []bool, err error) {
	// Each call goes on from where the clock of the one before stopped, so a test reading in phases
	// does not go back in time.
	clockMu.Lock()
	start := clockAt
	clockMu.Unlock()
	src := &fakeStream{steps: steps, now: start}
	defer func() {
		clockMu.Lock()
		clockAt = src.now.Add(time.Second)
		clockMu.Unlock()
	}()
	err = s.read(context.Background(), src, 4, src.clock, func() {
		s.mu.Lock()
		turns = append(turns, s.plays.Playing())
		s.mu.Unlock()
	})
	return turns, err
}

// Silence, however long, is not a phone playing, nor is a sound of a second; music is, once it has
// played sendingAgain, and a phone that sends nothing audible for receiveQuiet has stopped.
func TestAPhoneIsHeardPlaying(t *testing.T) {
	s := &inStream{item: "Bluetooth: Pixel"}
	turns, err := readAll(s, concat(
		stereo(0, 30*time.Second),
		stereo(4000, time.Second), stereo(0, 2*time.Second),
		stereo(3000, 3*time.Second),
		idle(3*time.Second), // paused: its audio idle, and its stream still open
		stereo(0, 3*time.Second),
	))
	if !errors.Is(err, io.EOF) {
		t.Fatalf("read ended with %v, want the stream's end", err)
	}
	if !slices.Equal(turns, []bool{true, false}) {
		t.Fatalf("heard %v, want it to start playing once and stop once", turns)
	}
}

// Only a phone handed the speaker has its audio go to the player, whole frames of it, and the
// player's source ends when the stream does.
func TestOnlyThePhoneWithTheSpeakerIsPlayed(t *testing.T) {
	s := &inStream{item: "Bluetooth: Pixel"}
	src := &fakeStream{now: time.Unix(1000, 0)}
	data := stereo(3000, time.Second)[0]
	src.steps = [][]byte{data, data[:4095]} // not handed yet: read and let go
	ctx := context.Background()
	if err := s.read(ctx, src, 4, src.clock, func() {}); !errors.Is(err, io.EOF) {
		t.Fatalf("read ended with %v", err)
	}

	out := s.hand()
	src.steps = [][]byte{data[:4095], data[4095:], data}
	if err := s.read(ctx, src, 4, src.clock, func() {}); !errors.Is(err, io.EOF) {
		t.Fatalf("read ended with %v", err)
	}
	var got []byte
	buf := make([]byte, 8192)
	for {
		n, err := out.Read(buf)
		got = append(got, buf[:n]...)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				t.Fatalf("the player's read ended with %v, want the end of the stream", err)
			}
			break
		}
	}
	if len(got) != 2*4096 {
		t.Errorf("the player got %d bytes, want the 8192 sent once handed", len(got))
	}
}

// The player letting go of a phone's audio (its track replaced or over) stops it going to the
// player; the phone is still read.
func TestThePlayerLettingGo(t *testing.T) {
	s := &inStream{item: "Bluetooth: Pixel"}
	out := s.hand()
	_ = out.Close()
	src := &fakeStream{steps: stereo(3000, time.Second), now: time.Unix(1000, 0)}
	_ = s.read(context.Background(), src, 4, src.clock, func() {})
	if s.out != nil {
		t.Fatal("the phone's audio still goes to a player that let it go")
	}
	if n, err := out.Read(make([]byte, 16)); n != 0 || !errors.Is(err, os.ErrClosed) {
		t.Errorf("a closed source read %d, %v", n, err)
	}
}

// The player's read waits for the phone no longer than its deadline, as it did on the stream
// itself: a phone that stopped sending ends its track.
func TestThePlayersDeadline(t *testing.T) {
	out := newPhoneOut()
	_ = out.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
	if _, err := out.Read(make([]byte, 16)); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("read with nothing sent gave %v, want the deadline", err)
	}
	// With the deadline past, what has arrived is still read first, every time.
	for i := range 50 {
		out.put([]byte{1, 2, 3, 4})
		if n, err := out.Read(make([]byte, 4)); n != 4 || err != nil {
			t.Fatalf("read %d: %d, %v with audio waiting", i, n, err)
		}
	}
	out.put([]byte{1, 2, 3, 4, 5, 6, 7, 8})
	b := make([]byte, 4)
	if n, _ := out.Read(b); n != 4 {
		t.Fatalf("read %d bytes into 4", n)
	}
	if n, _ := out.Read(b); n != 4 || b[0] != 5 {
		t.Errorf("the rest of a read was lost: %d %v", n, b)
	}
}

// Two phones read through their streams, the speaker chosen by what each sends: phone A playing has
// it, phone B opening its audio for silence does not take it, and B playing music does, A then owed
// it back.
func TestTwoPhonesThroughTheirStreams(t *testing.T) {
	const a, b = "Bluetooth: A", "Bluetooth: B"
	pa, pb := &inStream{item: a}, &inStream{item: b}
	if _, err := readAll(pa, stereo(3000, 3*time.Second)); !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	h := holding{holder: a, playing: true}
	if _, err := readAll(pb, stereo(0, 30*time.Second)); !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	if got := choose([]heard{pa.look(), pb.look()}, h, map[string]bool{}); got != "" {
		t.Fatalf("B sending silence took the speaker from A playing: %q", got)
	}
	if _, err := readAll(pb, stereo(3000, 3*time.Second)); !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	if got := choose([]heard{pa.look(), pb.look()}, h, map[string]bool{}); got != b {
		t.Fatalf("B playing music did not take the speaker: %q", got)
	}
}

// A stream whose reading ended, or a phone back with another format, is read afresh rather than
// handed the speaker with nothing reading it.
func TestAStaleStreamIsReadAfresh(t *testing.T) {
	pcm := bluealsa.PCM{Rate: 44100, Channels: 2}
	s := &inStream{item: "Bluetooth: Pixel", pcm: pcm}
	if s.stale(pcm) {
		t.Fatal("a stream being read is stale")
	}
	if !s.stale(bluealsa.PCM{Rate: 48000, Channels: 2}) {
		t.Error("a phone back at another rate is read by the old stream")
	}
	src := &fakeStream{now: time.Unix(1000, 0)} // it ends at once, as on an error
	_ = s.read(context.Background(), src, 4, src.clock, func() {})
	if !s.stale(pcm) {
		t.Error("a stream whose reading ended is not read afresh")
	}
}

// A phone asked to pause that pauses and plays again moments later, before receiveQuiet, is heard
// starting again, so it takes the speaker back; one that ignores the pause and plays on is not, nor
// at the gap between two of its songs.
func TestAPhonePausedAwayAndPlayedAgainStarts(t *testing.T) {
	again := &inStream{item: "Bluetooth: Pixel"}
	if turns, _ := readAll(again, stereo(3000, 3*time.Second)); len(turns) != 1 {
		t.Fatalf("heard %v, want it playing", turns)
	}
	again.look()
	again.askedToPause()
	turns, _ := readAll(again, concat(stereo(0, time.Second), stereo(3000, 3*time.Second)))
	if l := again.look(); !slices.Equal(turns, []bool{false, true}) || !l.fresh {
		t.Errorf("paused and played again: heard %v, %+v, want it stopping and starting", turns, l)
	}

	on := &inStream{item: "Bluetooth: Pad"}
	readAll(on, stereo(3000, 3*time.Second))
	on.look()
	on.askedToPause()
	turns, _ = readAll(on, concat(stereo(3000, 7*time.Second), stereo(0, time.Second),
		stereo(3000, 3*time.Second)))
	if len(turns) != 0 || on.look().fresh {
		t.Errorf("a phone playing on through the pause was heard starting again: %v", turns)
	}
}
