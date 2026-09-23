package ring

import (
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/led"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

const (
	// RingFor is how long a ring sounds if nobody stops it, and RingEvery how often its chime
	// repeats. Every sound a ring can make has to fit inside RingEvery.
	RingFor   = 15 * time.Minute
	RingEvery = 2 * time.Second

	// level is the chime's loudness: louder than the device's feedback tones, since a ring is meant
	// to fetch somebody from another room.
	level = 0.6
)

// Color is the light a ring pulses.
var Color = led.Color{R: 0xFF, G: 0x40, B: 0x00}

// The bell's timing and what it does to the rest of the device, as variables so a test can run it
// fast and listen to it without a speaker or a light.
var (
	ringFor   = RingFor
	ringEvery = RingEvery

	chime = func(notes []speaker.Note) {
		step := Level()
		speaker.Sound().Interject(func(p *speaker.Player) { p.Bell(step, level, notes...) })
	}

	light = sync.OnceValue(func() *led.Claim { return led.Get().Claim(led.PriorityAlarm) })

	// attend is the bell taking the room while anything rings: the light pulses and the music is
	// ducked under the chime. Ducked rather than suspended: it is one room of what may be a whole
	// house, and the chime is audible over it.
	attend = func(on bool) {
		if on {
			light().Play(led.EffectPulse, Color)
		} else {
			light().Clear()
		}
		step := Level()
		speaker.Sound().Interject(func(p *speaker.Player) { p.Ringing(on, step) })
		speaker.Sound().Backgrounds().Duck("ring", on)
	}
)

// bell is the one thing on the device that rings. An alarm and a timer used to have a loop each,
// with a light each and a duck each, and ducking is a switch rather than a count: whichever of the
// two stopped first brought the music back up under the other one still ringing. One bell has one
// light and one duck, taken when the first ring starts and given back when the last one ends.
var bell struct {
	mu      sync.Mutex
	rings   []*ringer
	running bool
	wake    chan struct{}
}

func init() { bell.wake = make(chan struct{}, 1) }

// ringer is one ring on the bell.
type ringer struct {
	what    string
	notes   []speaker.Note
	until   time.Time // when it rings out
	stopped bool

	// silenced is a ring a button quieted, waiting on the snooze offer. Per ring rather than for the
	// whole bell, so one that starts during the offer still sounds rather than joining a silence
	// nobody asked of it, and is not ended when the offer runs out.
	silenced bool
	done     func() // its share of Sounding
	ended    func()
}

// Start rings: the chime every RingEvery, the light pulsing and the music ducked under it, until
// the returned stop is called, the ring is silenced and left unanswered, or it has sounded for
// RingFor. what names it in the log.
//
// ended is called once, whatever ended it, and always from the bell's own goroutine rather than
// from whoever called stop: an engine may hold its own lock while it stops a ring, and its ended
// will want that lock. Until ended is called the ring is still sounding, so an engine that clears
// its ringing state in ended stays consistent with IsSounding.
//
// Start counts as Sounding before it returns, so nothing can see a ring started and not sounding.
func Start(what string, notes []speaker.Note, ended func()) (stop func()) {
	r := &ringer{what: what, notes: notes, until: time.Now().Add(ringFor), done: Sounding(), ended: ended}

	bell.mu.Lock()
	bell.rings = append(bell.rings, r)
	if !bell.running {
		bell.running = true
		attend(true)
		safe.Go("bell", ringBell)
	}
	bell.mu.Unlock()
	nudge()

	return func() {
		bell.mu.Lock()
		r.stopped = true
		bell.mu.Unlock()
		nudge()
	}
}

// silenceBell quiets every ring sounding now.
func silenceBell() {
	bell.mu.Lock()
	for _, r := range bell.rings {
		r.silenced = true
	}
	bell.mu.Unlock()
	nudge()
}

// Again is a new reason to ring arriving while a ring already sounds - a second timer finishing into
// a silenced one, say. Every ring sounds again, for a full RingFor from now: the new reason is owed a
// chime of its own, not a place in a silence somebody asked of the last one.
func Again() {
	state.mu.Lock()
	state.offer = time.Time{}
	state.mu.Unlock()

	bell.mu.Lock()
	until := time.Now().Add(ringFor)
	for _, r := range bell.rings {
		r.silenced = false
		if until.After(r.until) {
			r.until = until
		}
	}
	bell.mu.Unlock()
	nudge()
}

// Sample plays one round of a ring's chime as it would ring, for somebody choosing a sound or a level.
func Sample(notes []speaker.Note) { chime(notes) }

// Level is how loud a ring goes, in the media volume's steps: its own setting, not the media volume.
func Level() int {
	c := config.Get()
	return c.Alarms.Ring(c.Speaker.Volume)
}

func nudge() {
	select {
	case bell.wake <- struct{}{}:
	default:
	}
}

// ringBell is the bell's loop. It runs while anything rings, and chimes every ring on it each
// round: an alarm and a timer going off together are both heard, as they always were.
func ringBell() {
	// ending is a ring leaving the bell and why, read under the lock: stop may still be called on a
	// ring after it has left.
	type ending struct {
		r   *ringer
		why string
	}
	var next time.Time
	for {
		now := time.Now()
		lapsed := Lapsed()

		bell.mu.Lock()
		var over []ending
		bell.rings = slices.DeleteFunc(bell.rings, func(r *ringer) bool {
			switch {
			case r.stopped:
				over = append(over, ending{r, ""})
			case lapsed && r.silenced:
				// A silenced ring whose offer ran out takes the answer it did not get, and stops.
				over = append(over, ending{r, " silenced by a button and left unanswered, stopping"})
			case !now.Before(r.until):
				over = append(over, ending{r, " rang out"})
			default:
				return false
			}
			return true
		})
		// What to chime and how long to wait, taken under the lock: Silence and Again change both.
		var loud [][]speaker.Note
		wait := time.Duration(-1)
		for _, r := range bell.rings {
			if !r.silenced {
				loud = append(loud, r.notes)
			}
			if d := r.until.Sub(now); wait < 0 || d < wait {
				wait = d
			}
		}
		last := len(bell.rings) == 0
		if last {
			// Under the lock, so a ring started now finds the bell stopped and takes the room
			// itself, rather than having this one give it back from under it.
			attend(false)
			bell.running = false
		}
		bell.mu.Unlock()

		if lapsed {
			lapsedDone()
		}
		for _, e := range over {
			switch e.why {
			case "":
			case " rang out":
				slog.Info(e.r.what+e.why, "for", ringFor)
			default:
				slog.Info(e.r.what + e.why)
			}
			// ended first: until it is called the ring is still sounding, as Start says.
			e.r.ended()
			e.r.done()
		}
		if last {
			return
		}

		// A near miss on the stop word holds every chime back for a moment; a button press holds
		// back only the rings it silenced. The light goes on pulsing through both, so the ring stays
		// obviously alive while it is silent.
		if !now.Before(next) {
			if !Hushed() {
				for _, notes := range loud {
					chime(notes)
				}
			}
			next = now.Add(ringEvery)
		}

		wait = min(wait, next.Sub(now))
		select {
		case <-bell.wake:
		case <-time.After(wait):
		}
	}
}
