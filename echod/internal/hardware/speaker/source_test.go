package speaker

import (
	"encoding/binary"
	"math"
	"testing"
)

// at records where it was asked to render, which is the whole point of a Source: the same audio has to
// land at the same output frame on every device in the group.
type recorder struct {
	at   []uint64
	fill int16
}

func (r *recorder) Render(at uint64, out []int16) {
	r.at = append(r.at, at)
	for i := range out {
		out[i] = r.fill
	}
}

func played(t *testing.T, p *Player) []int16 {
	t.Helper()

	buf := make([]byte, period*Channels*2)
	p.fill(buf)

	got := make([]int16, period*Channels)
	for i := range got {
		got[i] = int16(binary.LittleEndian.Uint16(buf[i*2:]))
	}
	return got
}

func newTestPlayer() *Player {
	p := &Player{}
	p.volume.Store(math.Float32bits(1))
	return p
}

func TestNothingAttachedPlaysTheQueueAlone(t *testing.T) {
	p := newTestPlayer()
	p.pending = []int16{100, 200, 300}

	got := played(t, p)

	if got[0] != 100 || got[1] != 200 || got[2] != 300 {
		t.Errorf("queue = %v, want 100 200 300", got[:3])
	}
	if got[3] != 0 {
		t.Errorf("past the queue = %d, want silence", got[3])
	}
}

// A reply is queued while a room stream is rendered, and both have to be audible.
func TestASourceIsSummedWithTheQueue(t *testing.T) {
	p := newTestPlayer()
	p.pending = []int16{100, 200}
	p.Attach(&recorder{fill: 10})

	got := played(t, p)

	if got[0] != 110 || got[1] != 210 {
		t.Errorf("mixed = %v, want 110 210", got[:2])
	}
	if got[2] != 10 {
		t.Errorf("past the queue = %d, want the source alone", got[2])
	}
}

func TestSummingTwoLoudThingsClampsInsteadOfWrapping(t *testing.T) {
	p := newTestPlayer()
	p.pending = []int16{math.MaxInt16 - 10}
	p.Attach(&recorder{fill: 1000})

	// The sum is pinned at full scale and then goes through the output's soft limiter, which bends it
	// down toward three quarters of full scale; what must never happen is the overflow wrapping it to
	// a large negative sample, which is a loud crack.
	if got := played(t, p)[0]; got < limit(math.MaxInt16*0.75) {
		t.Errorf("mixed = %d, want a loud positive sample, not a wrapped one", got)
	}
}

// Where audio lands must follow from the frame index, and the index only moves on a completed write.
func TestTheSourceIsAskedForTheFramesAboutToBePlayed(t *testing.T) {
	p := newTestPlayer()
	rec := &recorder{}
	p.Attach(rec)

	played(t, p)
	p.written.Add(period)
	played(t, p)

	want := []uint64{0, period}
	if len(rec.at) != 2 || rec.at[0] != want[0] || rec.at[1] != want[1] {
		t.Errorf("asked at %v, want %v", rec.at, want)
	}
}

func TestDetachingStopsTheSource(t *testing.T) {
	p := newTestPlayer()
	p.Attach(&recorder{fill: 10})
	p.Attach(nil)

	if got := played(t, p)[0]; got != 0 {
		t.Errorf("detached = %d, want silence", got)
	}
}

// An attached source is something playing while it places sound, and through two seconds of silence
// after, the gap between two tracks. One left attached with nothing to place, as Sendspin's is while
// Music Assistant has it paused, is not, or feature/takes would refuse to record over a pause.
func TestASourceWithNothingToPlaceIsNotSounding(t *testing.T) {
	p := newTestPlayer()
	if p.Sounding() {
		t.Fatal("nothing attached is sounding")
	}
	rec := &recorder{}
	p.Attach(rec)
	if !p.Sounding() {
		t.Fatal("a source just attached is not sounding")
	}
	for range srcQuietAfter - 1 {
		played(t, p)
	}
	if !p.Sounding() {
		t.Fatal("a source silent for less than two seconds is not sounding")
	}
	played(t, p)
	if p.Sounding() {
		t.Fatal("a source silent for two seconds is still sounding")
	}

	rec.fill = 10
	played(t, p)
	if !p.Sounding() {
		t.Fatal("a source placing sound again is not sounding")
	}
	rec.fill = 0
	for range srcQuietAfter {
		played(t, p)
	}
	p.Attach(&recorder{})
	if !p.Sounding() {
		t.Fatal("a new source took the silence of the one before it")
	}
	p.Attach(nil)
	if p.Sounding() {
		t.Fatal("a detached source is sounding")
	}
}
