package speaker

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Tested without hardware. Nothing is queued, so a claim ends as soon as its errand returns, which
// is what the arbitration below is about.
func driver() *Driver { return NewDriver(New()) }

func waitFor(t *testing.T, c *Claim) {
	t.Helper()
	select {
	case <-c.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("the claim never finished")
	}
}

func TestDriverPlays(t *testing.T) {
	d := driver()

	var ran atomic.Bool
	c := d.Claim("test", func(context.Context, *Player) error {
		ran.Store(true)
		return nil
	})

	waitFor(t, c)
	if !ran.Load() {
		t.Error("the errand never ran")
	}
	if c.Stopped() {
		t.Error("a claim that finished reports being stopped")
	}
}

// A second sound takes the speaker from the first, which is what makes a reply interruptible and a
// wake tone during one heard.
func TestClaimTakesOverFromWhatWasPlaying(t *testing.T) {
	d := driver()

	first := d.Claim("first", func(ctx context.Context, _ *Player) error {
		<-ctx.Done()
		return nil
	})

	second := d.Claim("second", func(context.Context, *Player) error { return nil })
	waitFor(t, second)

	if !first.Stopped() {
		t.Error("the first claim was left holding the speaker")
	}
	if second.Stopped() {
		t.Error("the second claim was stopped by taking over")
	}
}

// Silence is what the action button does, and it has to reach an errand that is still fetching rather
// than playing: that is the reply that used to arrive after being canceled.
func TestSilenceStopsAClaimBeforeItPlays(t *testing.T) {
	d := driver()

	var played atomic.Bool
	c := d.Claim("fetch", func(ctx context.Context, p *Player) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
		played.Store(true)
		return nil
	})

	time.Sleep(20 * time.Millisecond)
	d.Silence()
	waitFor(t, c)

	if played.Load() {
		t.Error("the errand played after being silenced")
	}
	if !c.Stopped() {
		t.Error("a silenced claim does not report being stopped")
	}
	if d.Busy() {
		t.Error("the speaker is still busy after being silenced")
	}
}

// Silencing nothing is what the button does most of the time.
func TestSilenceWithNothingPlaying(t *testing.T) {
	d := driver()
	d.Silence()

	if d.Busy() {
		t.Error("an idle speaker reports being busy")
	}
}

func TestClaimFailureIsKept(t *testing.T) {
	d := driver()
	want := errors.New("no such reply")

	c := d.Claim("broken", func(context.Context, *Player) error { return want })
	waitFor(t, c)

	if !errors.Is(c.Err(), want) {
		t.Errorf("Err = %v, want %v", c.Err(), want)
	}
}

// An errand that ignores its context must not hold the next sound up for longer than the driver's own
// patience, since the point is that something else can always be played.
func TestAnErrandThatWillNotStopDoesNotBlockForever(t *testing.T) {
	d := driver()

	d.Claim("stuck", func(context.Context, *Player) error {
		time.Sleep(3 * time.Second)
		return nil
	})

	start := time.Now()
	next := d.Claim("next", func(context.Context, *Player) error { return nil })
	waitFor(t, next)

	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("waited %s for a stuck errand", took.Round(time.Millisecond))
	}
}

// A claim over the music waits its turn rather than taking the speaker from what is being said: a doorbell
// that announces and shows the camera both rings and shows the picture, and the camera's own sound
// arriving second must not cut the announcement off mid-word.
func TestClaimOverWaitsForWhatIsBeingSaid(t *testing.T) {
	d := driver()

	release := make(chan struct{})
	speaking := d.ClaimSpeech("announce", func(context.Context, *Player) error {
		<-release
		return nil
	})

	var ran atomic.Bool
	over := d.ClaimOver("over the music", func(context.Context, *Player) error {
		ran.Store(true)
		return nil
	})

	// The announcement is still being made, so the sound over the music has not begun.
	time.Sleep(50 * time.Millisecond)
	if ran.Load() {
		t.Fatal("a sound over the music cut off what was being said rather than waiting for it")
	}
	if speaking.Stopped() {
		t.Fatal("the announcement was taken from rather than waited for")
	}

	close(release)
	waitFor(t, over)
	if !ran.Load() {
		t.Error("the sound over the music never played after waiting")
	}
}

// The other way round, words take the speaker from a sound over the music: an announcement is worth
// interrupting a camera for. What the sound's own feature makes of that is the difference between being
// stopped and being taken — the view still wants its sound, so it is asked for again.
func TestWordsTakeTheSpeakerFromASoundOverTheMusic(t *testing.T) {
	d := driver()

	playing := make(chan struct{})
	over := d.ClaimOver("over the music", func(ctx context.Context, _ *Player) error {
		close(playing)
		<-ctx.Done()
		return nil
	})
	<-playing

	announced := make(chan struct{})
	said := d.ClaimSpeech("announce", func(context.Context, *Player) error {
		close(announced)
		return nil
	})
	<-announced
	waitFor(t, said)

	if !over.Stopped() {
		t.Error("a sound over the music was left holding the speaker while an announcement was made")
	}
}

// Muting a claim that sounds over the background lets the music back up to its own level, and brings it
// down again when the sound is brought back: a camera's sound that has been silenced from the screen
// should not leave the room's music quiet for the rest of the view.
func TestMutingAClaimLetsTheBackgroundUp(t *testing.T) {
	d := driver()
	music := &producer{}
	d.Backgrounds().Took(music)

	playing := make(chan struct{})
	over := d.ClaimOver("over the music", func(ctx context.Context, _ *Player) error {
		close(playing)
		<-ctx.Done()
		return nil
	})
	<-playing

	ducked := func() bool { return music.quiet() }
	if !ducked() {
		t.Fatal("a sound over the music did not hold the music down while it played")
	}

	over.Mute(true)
	if ducked() {
		t.Error("muting a sound over the music left the music down")
	}

	over.Mute(false)
	if !ducked() {
		t.Error("bringing the sound back did not hold the music down again")
	}

	// And however it ends, muted or not, the background is not left down.
	over.Mute(true)
	d.Silence()
	if ducked() {
		t.Error("the music was left down after a muted sound over it ended")
	}
}

// A sound played over the music ducks deeper than words do. A reply is close and loud and is heard over a
// room's music at the ducking a turn gets; a camera's own sound is what its microphone hears of a street,
// and at that level it is the music that comes through instead.
func TestASoundOverTheMusicDucksDeeperThanWords(t *testing.T) {
	d := driver()
	music := &producer{}
	d.Backgrounds().Took(music)

	level := func() int {
		music.mu.Lock()
		defer music.mu.Unlock()
		return music.db
	}

	// Words, at the listener's own level while they last.
	talking, done := make(chan struct{}), make(chan struct{})
	words := d.ClaimSpeech("announce", func(context.Context, *Player) error {
		close(talking)
		<-done
		return nil
	})
	<-talking
	byWords := level()
	if byWords >= 0 {
		t.Fatalf("words ducked the music by %d dB, want it quietened", byWords)
	}
	close(done)
	waitFor(t, words)

	// A sound over the music, on its own, asks for more.
	playing := make(chan struct{})
	over := d.ClaimOver("over the music", func(ctx context.Context, _ *Player) error {
		close(playing)
		<-ctx.Done()
		return nil
	})
	<-playing

	if byOver := level(); byOver != byWords-overDeeper {
		t.Errorf("a sound over the music ducked by %d dB against the %d dB words get, want %d",
			byOver, byWords, byWords-overDeeper)
	}
	d.Silence()
	waitFor(t, over)
}

// While more than one thing wants the background down, the deepest ask is the one heard, and letting go of
// one leaves what the others asked for: a camera's own sound during an announcement is heard, and when it
// ends the music goes back to the announcement's ducking rather than up under it.
func TestTheDeepestDuckIsTheOneHeard(t *testing.T) {
	a := &Arbiter{}
	music := &producer{}
	a.Took(music)

	level := func() int {
		music.mu.Lock()
		defer music.mu.Unlock()
		return music.db
	}

	a.Duck("turn", true)
	byTurn := level()
	if byTurn >= 0 {
		t.Fatal("a turn did not duck the music")
	}

	a.duckTo("camera sound", duckDB(true), true)
	if byCamera := level(); byCamera != byTurn-overDeeper {
		t.Errorf("the music is at %d dB with a camera over a turn, want the camera's %d", byCamera, byTurn-overDeeper)
	}

	// The turn ends first: the camera's ask is the deeper one, so the music stays where it was.
	a.Duck("turn", false)
	if got := level(); got != byTurn-overDeeper {
		t.Errorf("the music came up to %d dB while a camera was still playing over it", got)
	}

	a.duckTo("camera sound", duckDB(true), false)
	if got := level(); got != 0 {
		t.Errorf("the music is at %d dB with nobody asking, want it back at its own level", got)
	}
}

// Nothing goes past silence: a listener who has already ducked the music as far as it goes does not have it
// ducked further, out past the range of any number anybody chose.
func TestTheDuckStopsAtItsFloor(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))

	if err := config.Set().Media().DuckDB(-3); err != nil {
		t.Fatal(err)
	}
	if got := duckDB(false); got != -3 {
		t.Errorf("words duck by %d dB when the listener asked for -3", got)
	}
	if got := duckDB(true); got != -3-overDeeper {
		t.Errorf("a sound over the music ducks by %d dB, want %d", got, -3-overDeeper)
	}

	if err := config.Set().Media().DuckDB(-100); err != nil {
		t.Fatal(err)
	}
	if got := duckDB(true); got != minDuck {
		t.Errorf("a duck went to %d dB, want the floor of %d", got, minDuck)
	}

	// And no ducking asked for is no ducking, camera or not: somebody who does not want their music
	// quietened should not have it quietened at all.
	if err := config.Set().Media().DuckDB(0); err != nil {
		t.Fatal(err)
	}
	if got := duckDB(true); got != 0 {
		t.Errorf("a sound over the music ducked by %d dB with ducking turned off, want none", got)
	}
	if got := duckDB(false); got != 0 {
		t.Errorf("words ducked by %d dB with ducking turned off, want none", got)
	}
}

// A claim that waits its turn is not what holds the speaker while it waits, and must not be: a reply
// arriving meanwhile has to take the speaker from what is being said rather than from the wait, or it
// plays mixed over the announcement it was meant to follow. The claim that waited plays when its turn
// comes, after the reply.
func TestAWaitingClaimDoesNotHoldTheSpeaker(t *testing.T) {
	d := driver()

	release := make(chan struct{})
	announcement := d.ClaimSpeech("announce", func(ctx context.Context, _ *Player) error {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil
	})

	waiting := d.ClaimOver("over the music", func(context.Context, *Player) error { return nil })
	time.Sleep(50 * time.Millisecond)

	d.mu.Lock()
	held := d.now
	d.mu.Unlock()
	if held != announcement {
		name := "nothing"
		if held != nil {
			name = held.name
		}
		t.Fatalf("the speaker is held by %q while a claim waits its turn, want the announcement", name)
	}
	if waiting.Finished() {
		t.Fatal("a claim waiting its turn was treated as though it had taken the speaker")
	}

	// A reply arriving now takes the speaker from the announcement, which is what leaves the announcement
	// behind rather than under the reply.
	replied := make(chan struct{})
	reply := d.ClaimSpeech("reply", func(context.Context, *Player) error {
		close(replied)
		return nil
	})
	<-replied
	waitFor(t, reply)

	if !announcement.Stopped() {
		t.Error("a reply arrived while a claim waited and left the announcement playing under it")
	}

	// And the claim that waited has its turn once the reply is done.
	close(release)
	waitFor(t, waiting)
	if waiting.Stopped() {
		t.Error("the claim that waited was stopped instead of played when its turn came")
	}
}

// With music set to stop under a turn, a claim waiting its turn must not bring it back up: it is not the
// holder, so nothing about the background is its to change until it starts.
func TestAWaitingClaimLeavesTheBackgroundStandingDown(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Media().OnTurn(config.OnTurnPause); err != nil {
		t.Fatal(err)
	}

	d := driver()
	music := &producer{}
	d.Backgrounds().Took(music)

	release := make(chan struct{})
	d.ClaimSpeech("announce", func(ctx context.Context, _ *Player) error {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil
	})
	if !music.held() {
		t.Fatal("words with music set to stop under them left it playing")
	}

	waiting := d.ClaimOver("over the music", func(context.Context, *Player) error { return nil })
	time.Sleep(50 * time.Millisecond)
	if !music.held() {
		t.Error("a claim waiting its turn brought the music back up under the announcement")
	}

	close(release)
	waitFor(t, waiting)
}
