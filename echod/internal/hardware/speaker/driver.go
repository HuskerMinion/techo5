package speaker

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Driver decides who gets to make a sound, the way led.Driver decides what the ring shows. It does
// not touch the hardware: Player owns the device and the queue, and this owns who may fill it.
//
// One claim holds it at a time, and taking it silences whatever had it. A claim covers the whole
// errand rather than just the audio, so canceling a reply abandons the download instead of
// dropping it once it arrives.
type Driver struct {
	p *Player

	mu  sync.Mutex
	now *Claim
	bg  Background
	arb *Arbiter

	// yielded is whether the background has been told to stand down, kept equal to "something holds the
	// speaker". It is a flag rather than a count because claims displace one another: pairing a suspend
	// with every claim and a resume with every release leaks a hold each time one sound takes over from
	// another, and the background then never plays again.
	yielded bool
}

func NewDriver(p *Player) *Driver { return &Driver{p: p} }

var (
	soundOnce sync.Once
	sound     *Driver
)

// Sound is who may make one. Everything audible goes through it, so silencing the device is one call
// wherever the sound came from.
func Sound() *Driver {
	soundOnce.Do(func() { sound = NewDriver(Get()) })
	return sound
}

// Background is a long sound that yields to the others rather than being taken from: media, which
// plays for minutes and cannot be started again from where it was. Everything else is an errand
// that runs to the end, so a claim is enough for it.
type Background interface {
	// Suspend stops filling the queue and empties what is in it. It is called before the claim that
	// displaced it queues anything, so the two never fight over the same audio.
	Suspend()

	// Resume carries on, if the caller is still what it was suspended for.
	Resume()
}

// Yields registers the background sound. There is one.
func (d *Driver) Yields(b Background) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.bg = b
}

// Claim takes the speaker and does whatever it takes to make the sound: play queues audio and
// returns, and the claim ends once what it queued has played out. It must return when ctx is done,
// which is what being silenced means.
func (d *Driver) Claim(name string, play func(ctx context.Context, p *Player) error) *Claim {
	return d.claim(name, claimSpec{}, play)
}

// ClaimSpeech is Claim for words: an answer or an announcement. Unless the listener set music to pause
// for a turn, the background keeps playing under it, at whatever level it has been ducked to, rather
// than standing aside until the words are done.
func (d *Driver) ClaimSpeech(name string, play func(ctx context.Context, p *Player) error) *Claim {
	return d.claim(name, claimSpec{over: config.Get().Media.OnTurn != config.OnTurnPause}, play)
}

// ClaimOver takes the speaker for something that is not words but still belongs over the music rather
// than instead of it: the background is ducked for as long as the claim lasts and comes back up when it
// ends. It is ClaimSpeech without the words' setting deciding, because the two are not the same
// question. Music that stops for a turn is what somebody asked for; a camera's own sound is the outside
// coming in, and no setting about turns should make that the end of what the room was listening to.
//
// Unlike a claim for words it does not take the speaker from what is being said, it waits for it: a
// doorbell that announces and shows the camera both rings and shows the picture, and the camera's sound
// arriving second must not cut the announcement off mid-word. What it waits for is read as over the
// music either way, so nothing is resumed behind it that should not be.
func (d *Driver) ClaimOver(name string, play func(ctx context.Context, p *Player) error) *Claim {
	return d.claim(name, claimSpec{over: true, waits: true, deep: true}, play)
}

// overDeeper is how much more a sound played over the music quietens the background than words do. A reply
// is close and loud and is heard over a room's music at the ducking a turn gets; a camera's own sound is
// what its microphone hears of a street, and at that level it is the room's music that comes through.
const overDeeper = 15

// minDuck is as far down as a duck goes. At that point it is silence either way, and a number nobody chose
// is worse than a floor; the listener's own setting goes no further than -40.
const minDuck = -60

// duckDB is how far down the background is asked to be: the listener's own level for words, and deeper for a
// sound whose own level is low. The deepest ask is the one heard, whichever claim it came from (arbiter.go).
func duckDB(deep bool) int {
	db := config.Get().Media.DuckDB
	if db >= 0 {
		return 0 // asked for no ducking at all, which is what they get, camera or not
	}
	if deep {
		db -= overDeeper
	}
	return max(db, minDuck)
}

// claimSpec is what makes one kind of claim different from another. They are not one thing: a claim for
// words ducks the background, and a sound over the music ducks it too, but it is not words — it never
// displaces what is being said, and it needs more room than words do.
type claimSpec struct {
	over  bool // sounds over the background rather than standing it down
	waits bool // waits for whatever holds the speaker rather than displacing it
	deep  bool // ducks deeper than words: a sound whose own level is low, heard over the room's music
}

func (d *Driver) claim(name string, spec claimSpec, play func(ctx context.Context, p *Player) error) *Claim {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Claim{name: name, over: spec.over, cancel: cancel, done: make(chan struct{})}
	if spec.over {
		// Only a claim that sounds over the background ducks anything, and working out how far is the
		// only thing here that reads the listener's setting. A plain claim must not read it: a ring left
		// running by one test would then race the next test's config, and it never ducks anyway.
		c.duck = duckDB(spec.deep)
	}

	// Words over the background have it ducked while they last, under a name of their own so the claim
	// that follows lets go of nothing but its own. A turn has usually ducked it already.
	var bg *Arbiter
	if spec.over {
		d.mu.Lock()
		bg = d.arb // only one that exists: nothing to duck is nothing to make
		d.mu.Unlock()
	}
	if bg != nil {
		c.bg = bg
	}

	if !spec.waits {
		// The ordinary way: the claim holds the speaker from the moment it is made, and takes it from
		// whatever had it. Before the errand queues anything, so the two never fight over the same audio.
		d.mu.Lock()
		previous := d.now
		d.now = c
		d.mu.Unlock()

		d.settle()
		previous.preempt(d.p)
	}

	go func() {
		defer close(c.done)
		if bg != nil {
			defer bg.duckTo(c.duckName(), c.duck, false)
		}
		defer d.release(c)

		if spec.waits {
			// A claim that waits its turn takes the speaker only when its turn comes, and ducks only
			// then: it is not what holds the speaker while it waits, and must not be, or a reply arriving
			// meanwhile would preempt the wait rather than what is being said, and leave the announcement
			// playing on under it.
			if !d.take(ctx, c) {
				return // silenced, or passed over, before it started
			}
		}
		if bg != nil {
			bg.duckTo(c.duckName(), c.duck, true)
		}
		if err := play(ctx, d.p); err != nil {
			c.fail(err)
			return
		}
		c.mark(d.await(ctx))
	}()
	return c
}

// take waits for the speaker to come free and takes it, for a claim that waits its turn rather than
// displacing what holds it. It reports whether its turn came: a claim cancelled while it waited —
// silenced, or passed over — does not, because the context is cancelled with it.
func (d *Driver) take(ctx context.Context, c *Claim) bool {
	for {
		if ctx.Err() != nil {
			return false
		}

		d.mu.Lock()
		if d.now == nil || d.now == c {
			d.now = c
			d.mu.Unlock()
			// The background's side of holding the speaker is this claim's now, which is what settle
			// keeps equal to "something holds it" — an over claim holds it without standing the
			// background down.
			d.settle()
			return true
		}
		held := d.now
		d.mu.Unlock()

		select {
		case <-held.Done():
		case <-ctx.Done():
			return false
		}
	}
}

// release lets the background sound carry on, once nothing else wants the speaker. A claim that was
// displaced releases nothing: the one that took it from it is still playing. Neither does one that never
// took the speaker at all — a claim that waited and was passed over, or silenced before its turn.
func (d *Driver) release(c *Claim) {
	d.mu.Lock()
	if d.now != c {
		d.mu.Unlock()
		return
	}
	d.now = nil
	d.mu.Unlock()

	d.settle()
}

// settle tells the background whether it may play, which is whenever nothing holds the speaker. Every
// change to now goes through here, so the two cannot drift apart.
func (d *Driver) settle() {
	d.mu.Lock()
	want := d.now != nil && !d.now.over
	if want == d.yielded {
		d.mu.Unlock()
		return
	}
	d.yielded = want
	bg := d.bg
	d.mu.Unlock()

	if bg == nil {
		return
	}
	if want {
		bg.Suspend()
		return
	}
	bg.Resume()
}

// Interject makes a sound without taking the speaker from what has it. Short feedback — a volume
// beep, a mute tone — is worth hearing, and not worth losing a reply over: it goes into the queue
// behind whatever is already there rather than replacing it.
func (d *Driver) Interject(play func(p *Player)) { play(d.p) }

// Silence stops whatever is playing and empties the queue whether anything claimed it or not. It is
// safe when nothing is playing.
func (d *Driver) Silence() {
	d.mu.Lock()
	c := d.now
	d.now = nil
	d.mu.Unlock()

	c.stop(d.p)
	d.p.Drain()
	d.settle()
}

// Busy reports whether anything holds the speaker.
func (d *Driver) Busy() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.now != nil && !d.now.Finished()
}

// HardwareTail is how long the playback buffer goes on sounding after the queue has run out, so
// nothing should conclude the room is quiet until it has passed.
const HardwareTail = 150 * time.Millisecond

// dry is how long the queue has to stay empty to count as finished. Audio arrives in chunks with gaps
// between them, so one empty read means nothing.
const dry = 400 * time.Millisecond

// await waits for what was queued to play out, and reports when the queue first ran dry: what comes
// after that is confirmation, and counting it would overstate how long the sound took.
func (d *Driver) await(ctx context.Context) time.Time {
	const tick = 50 * time.Millisecond

	var empty time.Time
	for {
		select {
		case <-ctx.Done():
			return time.Now()
		case <-time.After(tick):
		}

		if d.p.Queued() > 0 {
			empty = time.Time{}
			continue
		}
		if empty.IsZero() {
			empty = time.Now()
		}
		if time.Since(empty) >= dry {
			time.Sleep(HardwareTail)
			return empty
		}
	}
}

// Claim is a hold on the speaker.
type Claim struct {
	name   string
	over   bool // sounds over the background rather than standing it down
	cancel context.CancelFunc
	done   chan struct{}

	// duck is how far down this claim asks the background to be, and bg the background to ask, for one
	// that sounds over it. Muting lets it back up rather than leaving the room quiet and ducked at once.
	duck int
	bg   *Arbiter

	mu       sync.Mutex
	err      error
	stopped  bool
	started  time.Time
	finished time.Time
}

// duckName is what the claim asks the background to duck under.
func (c *Claim) duckName() string { return fmt.Sprintf("%s %p", c.name, c) }

// Mute silences a claim's own sound without giving the speaker up, which is what the camera page's
// control does: the background comes back up to its own level while the sound is silent, and goes down
// again when it is brought back. Letting go of the duck rather than taking a second one is what keeps
// that level where the claim put it, however many times the control is tapped.
//
// The audio is the caller's to stop writing: what a claim is playing is its own, and a mute here is only
// the background's level. A claim that sounds instead of over the background has none to let up, and
// nothing to do.
func (c *Claim) Mute(on bool) {
	if c == nil || c.bg == nil {
		return
	}
	c.bg.duckTo(c.duckName(), c.duck, !on)
}

// Started records that sound has begun, which is where a reply's timing starts counting from.
func (c *Claim) Started() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started.IsZero() {
		c.started = time.Now()
	}
}

// Playing is when the sound began, zero if it never did.
func (c *Claim) Playing() time.Time {
	if c == nil {
		return time.Time{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.started
}

// Quiet is when the queue first ran dry, zero while the sound is still going.
func (c *Claim) Quiet() time.Time {
	if c == nil {
		return time.Time{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.finished
}

// Done closes when the sound is over, whether it played out or was silenced.
func (c *Claim) Done() <-chan struct{} {
	if c == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return c.done
}

// Stopped reports whether the claim was taken away rather than finishing.
func (c *Claim) Stopped() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stopped
}

// Err is what the errand failed with, if it did.
func (c *Claim) Err() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *Claim) Finished() bool {
	select {
	case <-c.Done():
		return true
	default:
		return false
	}
}

// preempt makes way for another sound. A claim that has already played out is left alone: its audio
// has been heard, and draining then would cut off whatever is playing without a claim.
func (c *Claim) preempt(p *Player) {
	if c == nil || c.Finished() {
		return
	}
	c.stop(p)
}

// stop cancels the errand and silences the queue it was filling. Waiting for it to unwind is
// bounded: something that will not return must not stop the next sound from being made.
func (c *Claim) stop(p *Player) {
	if c == nil {
		return
	}

	c.mu.Lock()
	c.stopped = true
	c.mu.Unlock()

	c.cancel()
	p.Drain()

	select {
	case <-c.done:
	case <-time.After(time.Second):
		slog.Warn("sound would not stop", "claim", c.name)
	}

	// Whatever it queued on the way out goes too.
	p.Drain()
}

func (c *Claim) mark(quiet time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.finished = quiet
}

func (c *Claim) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.err = err
}
