package speaker

import (
	"bytes"
	"context"
	"math"
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// An answer and a track sound together: the track's lane is summed with the queue as it goes out, so
// what reaches the speaker is the same as the two added up.
func TestTheTrackLaneIsHeardUnderTheQueue(t *testing.T) {
	together := &Player{}
	together.volume.Store(math.Float32bits(1))
	together.pending = make([]int16, period*Channels)
	together.bed = make([]int16, period*Channels)
	for i := range together.pending {
		together.pending[i], together.bed[i] = 1000, 300
	}

	summed := &Player{}
	summed.volume.Store(math.Float32bits(1))
	summed.pending = make([]int16, period*Channels)
	for i := range summed.pending {
		summed.pending[i] = 1300
	}

	a, b := make([]byte, period*Channels*2), make([]byte, period*Channels*2)
	together.fill(a)
	summed.fill(b)
	if !bytes.Equal(a, b) {
		t.Fatal("the track and the answer did not go out summed")
	}
	if together.Queued() != 0 || together.Bed().Queued() != 0 {
		t.Error("a period of each lane was not taken")
	}
}

// Stopping an answer empties the answer's queue, not the track's: the track is left alone.
func TestSilencingAnAnswerLeavesTheTrackQueued(t *testing.T) {
	d := driver()
	d.p.pending = make([]int16, 100)
	d.p.bed = make([]int16, 100)
	d.Silence()
	if d.p.Bed().Queued() == 0 {
		t.Fatal("silencing the answer threw the track away")
	}
	if d.p.Queued() != 0 {
		t.Error("the answer was still queued after silencing it")
	}
}

// With music set to duck, an answer plays over the track, which stays ducked for as long as it
// lasts; any other sound still has the speaker to itself. With music set to pause, an answer is
// like any other sound.
func TestAnAnswerPlaysOverTheTrackUnlessMusicPauses(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))

	d := driver()
	a := d.Backgrounds()
	track := &producer{}
	a.Took(track)

	var duckedDuring bool
	waitFor(t, d.ClaimSpeech("reply", func(context.Context, *Player) error {
		a.mu.Lock()
		duckedDuring = a.duck
		a.mu.Unlock()
		return nil
	}))
	if track.suspends != 0 {
		t.Fatal("an answer stood the track down with music set to duck")
	}
	if !duckedDuring {
		t.Error("the track was not ducked under the answer")
	}
	if a.duck {
		t.Error("the track stayed ducked after the answer")
	}

	waitFor(t, d.Claim("call", func(context.Context, *Player) error { return nil }))
	if track.suspends != 1 || track.resumes != 1 {
		t.Errorf("another sound did not stand the track down and back up: %d suspends, %d resumes",
			track.suspends, track.resumes)
	}

	if err := config.Set().Media().OnTurn(config.OnTurnPause); err != nil {
		t.Fatal(err)
	}
	waitFor(t, d.ClaimSpeech("reply", func(context.Context, *Player) error { return nil }))
	if track.suspends != 2 || track.resumes != 2 {
		t.Errorf("an answer with music set to pause did not stand the track down: %d suspends, %d resumes",
			track.suspends, track.resumes)
	}
}
