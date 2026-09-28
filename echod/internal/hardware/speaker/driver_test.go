package speaker

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
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

	ducked := func() bool {
		music.mu.Lock()
		defer music.mu.Unlock()
		return music.ducked
	}
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
