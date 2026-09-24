package speaker

import (
	"sync"
	"testing"
)

// producer records what it was told. What matters is the state it is left in — a producer left
// suspended never plays again and nothing reports an error about it.
type producer struct {
	mu       sync.Mutex
	suspends int
	resumes  int
	ducked   bool
	requeues int
}

func (p *producer) Suspend() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.suspends++
}

func (p *producer) Resume() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.resumes++
}

func (p *producer) Duck(on bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ducked = on
}

// forget is what a real producer does when it gives the speaker back: its holds go with it.
func (p *producer) forget() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.resumes = p.suspends
}

func (p *producer) Requeue() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requeues++
}

func (p *producer) requeued() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.requeues
}

func (p *producer) held() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.suspends > p.resumes
}

func (p *producer) quiet() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ducked
}

func TestNewestProducerIsTheOneHeard(t *testing.T) {
	a := &Arbiter{}
	group, track := &producer{}, &producer{}

	a.Took(group)
	a.Took(track)

	if !group.held() {
		t.Error("the group kept playing under the track that displaced it")
	}
	if track.held() {
		t.Error("the track that just started was stood down")
	}
	if a.Playing() != track {
		t.Error("the track is not the one being heard")
	}
}

// The point of a stack rather than a single slot: a track is minutes, a group is hours, and the room
// should rejoin the house when the track ends instead of going quiet.
func TestWhatWasInterruptedCarriesOn(t *testing.T) {
	a := &Arbiter{}
	group, track := &producer{}, &producer{}

	a.Took(group)
	a.Took(track)
	a.Gave(track)

	if group.held() {
		t.Errorf("the group never came back: %d suspends, %d resumes", group.suspends, group.resumes)
	}
	if a.Playing() != group {
		t.Error("the group is not the one being heard again")
	}
}

// A producer that finishes while something else is on top was already stood down and is not the one
// being heard, so nothing should be resumed on its account.
func TestFinishingUnderneathChangesNothing(t *testing.T) {
	a := &Arbiter{}
	group, track := &producer{}, &producer{}

	a.Took(group)
	a.Took(track)
	a.Gave(group)

	if track.held() {
		t.Error("the track was stood down when something beneath it finished")
	}
	if a.Playing() != track {
		t.Error("the track stopped being the one heard")
	}
}

// A voice turn holds the speaker across several sounds. Whoever is heard has to stand down once and
// come back once, however the producers change underneath.
func TestTheDriverHoldWinsOverAHandover(t *testing.T) {
	a := &Arbiter{}
	group, track := &producer{}, &producer{}
	a.Took(group)

	a.Suspend()
	if !group.held() {
		t.Fatal("the group ignored the driver taking the speaker")
	}

	// A track starting mid-turn must not become audible just because it is newest.
	a.Took(track)
	if !track.held() {
		t.Error("a track that started while the speaker was held began playing")
	}

	a.Resume()
	if track.held() {
		t.Errorf("the track never started: %d suspends, %d resumes", track.suspends, track.resumes)
	}
}

// Ducking reaches the ones waiting too, so resuming one mid-turn does not bring it back at full volume.
func TestDuckingReachesTheOnesWaiting(t *testing.T) {
	a := &Arbiter{}
	group, track := &producer{}, &producer{}
	a.Took(group)
	a.Took(track)

	a.Duck("turn", true)
	if !group.quiet() || !track.quiet() {
		t.Error("ducking a turn missed a producer")
	}

	a.Duck("turn", false)
	if group.quiet() || track.quiet() {
		t.Error("the turn ended and something stayed quiet")
	}
}

// Ducking only reaches audio written after it. A stream is sent ahead of the clock, so seconds of it
// can already be queued at full volume — which is what "the duck happened, but late" sounds like. The
// one being heard has to go back and re-scale it, and only that one: the others queued nothing, and
// asking them all would attenuate the same samples twice.
func TestOnlyTheProducerBeingHeardRescalesWhatIsQueued(t *testing.T) {
	a := &Arbiter{}
	group, track := &producer{}, &producer{}
	a.Took(group)
	a.Took(track)

	a.Duck("turn", true)

	if got := track.requeued(); got != 1 {
		t.Errorf("the audible producer requeued %d times, want 1", got)
	}
	if got := group.requeued(); got != 0 {
		t.Errorf("a waiting producer requeued %d times, want 0", got)
	}
}

// Coming out of a turn there is nothing to fix: what is queued went in quiet and draining it is the
// end of the duck. Re-scaling by one would be a no-op, and re-scaling by anything else would be wrong.
func TestNothingIsRescaledWhenTheTurnEnds(t *testing.T) {
	a := &Arbiter{}
	track := &producer{}
	a.Took(track)

	a.Duck("turn", true)
	before := track.requeued()
	a.Duck("turn", false)

	if got := track.requeued(); got != before {
		t.Errorf("unducking requeued: %d then %d", before, got)
	}
}

// A producer starting during a turn has to arrive already quiet, or it blares over the reply.
func TestAProducerStartingMidTurnArrivesQuiet(t *testing.T) {
	a := &Arbiter{}
	a.Duck("turn", true)

	track := &producer{}
	a.Took(track)

	if !track.quiet() {
		t.Error("a track that started mid-turn came in at full volume")
	}
}

// The same producer starting twice — a stream reconnecting, say — must not leave a stale copy behind
// that later gets resumed as if it were still there.
func TestStartingTwiceLeavesOneEntry(t *testing.T) {
	a := &Arbiter{}
	group := &producer{}

	a.Took(group)
	a.Took(group)
	a.Gave(group)

	if a.Playing() != nil {
		t.Error("a duplicate entry outlived the producer that finished")
	}
}

// The media player is one producer that replaces its own track, so every noise or media switch is the
// same stream taking the speaker again. When that happens while the driver holds it, the hold already
// stood the stream down once — standing it down again leaves a resume outstanding, and it waits on a
// gate nobody opens: playing on paper, silent in the room, until the process restarts.
func TestAResumingTrackRetakingTheSpeakerIsNotHeldTwice(t *testing.T) {
	a := &Arbiter{}
	track := &producer{}
	a.Took(track)

	a.Suspend()
	if !track.held() {
		t.Fatal("the track ignored the driver taking the speaker")
	}

	// A track change mid-claim: the stream re-joins as itself.
	a.Took(track)
	if !track.held() {
		t.Error("a track must stay stood down while the speaker is held")
	}

	a.Resume()
	if track.held() {
		t.Errorf("the track never came back: %d suspends, %d resumes", track.suspends, track.resumes)
	}
}

// A sound that ends during the hold and starts again before it is over is a new start. A producer
// forgets its holds when it gives the speaker back - nothing would ever release them, since a hold's end
// goes to the producers there are - so it has to be stood down again as it rejoins, and come back once
// when the hold ends.
func TestARetakeAfterGivingBackDuringAHoldIsStoodDownAgain(t *testing.T) {
	a := &Arbiter{}
	track := &producer{}
	a.Took(track)

	a.Suspend()
	if !track.held() {
		t.Fatal("the track ignored the driver taking the speaker")
	}

	// A stop mid-claim, which is where the track forgets the hold, then a new sound before it ends.
	a.Gave(track)
	track.forget()
	a.Took(track)
	if !track.held() {
		t.Error("a track must stay stood down while the speaker is held")
	}

	a.Resume()
	if track.held() {
		t.Errorf("the track never came back: %d suspends, %d resumes", track.suspends, track.resumes)
	}
}

// Ducking is held while anyone asks for it: a ring ending in the middle of an answer leaves the music
// down under the answer, and it comes back only when the last asker lets go.
func TestDuckingLastsUntilTheLastAskerLetsGo(t *testing.T) {
	a := &Arbiter{}
	p := &producer{}
	a.Took(p)
	a.Duck("turn", true)
	a.Duck("ring", true)
	a.Duck("ring", false)
	if !a.duck {
		t.Fatal("one asker letting go brought the music up under the other")
	}
	a.Duck("turn", false)
	if a.duck {
		t.Error("the music stayed down after everyone let go")
	}
}

// A producer stood down by another and then taking the speaker back is let go of the stand-down: it
// was suspended once when the other took over, and taking over again is where that ends.
func TestTakingTheSpeakerBackUndoesBeingStoodDown(t *testing.T) {
	a := &Arbiter{}
	station, song := &producer{}, &producer{}
	a.Took(station)
	a.Took(song) // the song stands the station down
	if !station.held() {
		t.Fatal("the station was not stood down by the song")
	}
	a.Took(station) // the station is started again
	if station.held() {
		t.Errorf("the station took the speaker back and stayed held: %d suspends, %d resumes",
			station.suspends, station.resumes)
	}
	if !song.held() {
		t.Error("the song kept playing under the station")
	}

	// And under a hold: taking the speaker back mid-hold stays down until the hold ends, then plays.
	a.Took(song)
	a.Suspend()
	a.Took(station)
	if !station.held() {
		t.Fatal("the station played over the hold")
	}
	a.Resume()
	if station.held() {
		t.Errorf("the hold ended and the station stayed held: %d suspends, %d resumes", station.suspends, station.resumes)
	}
}
