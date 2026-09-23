package ring

import (
	"log/slog"
	"slices"
	"sync"
	"time"

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
		speaker.Sound().Interject(func(p *speaker.Player) { p.Chime(level, notes...) })
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
		speaker.Sound().Backgrounds().Duck(on)
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
	done    func() // its share of Sounding
	ended   func()
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

// Sample plays one round of a ring's chime as it would ring, for somebody choosing a sound.
func Sample(notes []speaker.Note) { chime(notes) }

func nudge() {
	select {
	case bell.wake <- struct{}{}:
	default:
	}
}

// ringBell is the bell's loop. It runs while anything rings, and chimes every ring on it each
// round: an alarm and a timer going off together are both heard, as they always were.
func ringBell() {
	var next time.Time
	for {
		now := time.Now()
		lapsed := Lapsed()

		bell.mu.Lock()
		var over []*ringer
		bell.rings = slices.DeleteFunc(bell.rings, func(r *ringer) bool {
			if r.stopped || lapsed || !now.Before(r.until) {
				over = append(over, r)
				return true
			}
			return false
		})
		rings := slices.Clone(bell.rings)
		last := len(rings) == 0
		if last {
			// Under the lock, so a ring started now finds the bell stopped and takes the room
			// itself, rather than having this one give it back from under it.
			attend(false)
			bell.running = false
		}
		bell.mu.Unlock()

		for _, r := range over {
			switch {
			case r.stopped:
			case lapsed:
				// A silenced ring whose offer ran out takes the answer it did not get, and stops.
				slog.Info(r.what + " silenced by a button and left unanswered, stopping")
			default:
				slog.Info(r.what+" rang out", "for", ringFor)
			}
			r.done()
			r.ended()
		}
		if last {
			return
		}

		// Quiet covers both reasons the chime is held back: a near miss on the stop word, and a
		// button press waiting on an answer. The light goes on pulsing through it, so the ring
		// stays obviously alive while it is silent.
		if !now.Before(next) {
			if !Quiet() {
				for _, r := range rings {
					chime(r.notes)
				}
			}
			next = now.Add(ringEvery)
		}

		wait := next.Sub(now)
		for _, r := range rings {
			wait = min(wait, r.until.Sub(now))
		}
		select {
		case <-bell.wake:
		case <-time.After(wait):
		}
	}
}
