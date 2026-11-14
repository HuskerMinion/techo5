package speaker

import (
	"log/slog"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// The jack's idle sleep (#537). With the jack the output, the headphone path stays up while nothing
// plays, as the speaker's does, and powered speakers on the jack hiss with the codec's noise floor
// all the while. Stock firmware let the output go after a few seconds of silence, with a soft pop
// going down and coming back. Jack sleep is that: off by default, and when it is on, the
// headphone path comes down (headphoneOff) once nothing has wanted the speaker for idleAfter, and
// goes back up before the next sound, which waits for it. The stream keeps running on silence as
// before; only the path changes. It trades a pop at each end for quiet in between, which is why it
// is a choice and not the default.
//
// Only Headphones: with Speaker and headphones the speaker is playing too, and its own idle sleep is
// the speaker's gate (idle.go).

// jackSettle is how long a woken headphone path is given before the sound held for it goes out, so its
// start is not lost to the codec powering the path up.
const jackSettle = 250 * time.Millisecond

// SetJackSleep turns the jack's idle sleep on or off. Off wakes a sleeping jack at the write loop's
// next period.
func (p *Player) SetJackSleep(on bool) { p.jackSleep.Store(on) }

// JackSleepPossible is whether this build has a jack to put to sleep.
func JackSleepPossible() bool { return HasJack }

// jackStep is the write loop's turn at the jack's sleep, once a period, as idleStep is the
// amplifier's: it starts a sleep or a wake where one is due and reports whether this period's audio
// must wait. A jack that is not the output, or with the switch off, counts as wanted: it stays up, and
// one asleep is woken.
func (p *Player) jackStep() bool {
	busy := !p.jackSleep.Load() || p.Output() != OutputHeadphone || p.wanted()
	act, hold := p.jackIdle.step(time.Now(), busy)
	switch act {
	case ampSleep:
		safe.Go("headphone jack sleep", p.jackDown)
	case ampWake:
		safe.Go("headphone jack wake", p.jackUp)
	}
	return hold
}

// jackDown takes the headphone path down, where the jack is still the output, is not draining into a
// switch, the switch is still on and nothing wants the speaker since: otherwise it counts as awake and
// is tried again idleAfter later. Asleep even when a write failed, so the next sound's wake writes the
// whole path back.
func (p *Player) jackDown() {
	p.pathMu.Lock()
	defer p.pathMu.Unlock()
	if p.out != OutputHeadphone || p.draining || !p.jackSleep.Load() || !p.jackIdle.is(ampSleeping) || p.wanted() {
		p.jackIdle.settled(ampAwake)
		return
	}
	if !gateWrite(p, headphoneOff) {
		slog.Warn("speaker: taking the headphone path down failed; the next sound writes it again")
	}
	slog.Debug("speaker: headphone jack asleep")
	p.jackIdle.settled(ampAsleep)
}

// jackUp puts the headphone path back and gives it jackSettle before the held sound goes out. Where
// the jack is no longer the output, whatever moved it wrote its own path, and there is nothing to put
// back. A wake whose writes fail leaves the jack asleep, as the amplifier's does (ampWake): an alarm or
// a ring would play into a path still down, so the next period tries again with the sound held, and
// after wakeTries in a row it is let out anyway rather than held for good.
func (p *Player) jackUp() {
	p.pathMu.Lock()
	defer p.pathMu.Unlock()
	if p.out == OutputHeadphone && !p.draining {
		if !gateWrite(p, p.path(OutputHeadphone)) {
			if p.jackWakeFails.Add(1) < wakeTries {
				slog.Warn("speaker: putting the headphone path back failed; trying again")
				p.jackIdle.settled(ampAsleep)
				return
			}
			slog.Error("speaker: the headphone path would not come back; playing anyway", "tries", wakeTries)
		}
		time.Sleep(jackSettle)
	}
	p.jackWakeFails.Store(0)
	p.jackIdle.settled(ampAwake)
}

// jackAwake records that a path was just written whole (a start, a change of output), which leaves the
// jack as awake as its output says. Called with pathMu held.
func (p *Player) jackAwake() {
	p.jackWakeFails.Store(0)
	p.jackIdle.settled(ampAwake)
}

// WakeDelay is how much later than it is queued a sound queued now starts, because the headphone path
// is asleep or still coming back: what times a sound by its length (the wake tone and the microphones)
// adds it. The mixer writes ahead of the settle are not counted.
func (p *Player) WakeDelay() time.Duration {
	if p == nil || !HasJack || !p.jackSleep.Load() || p.jackIdle.is(ampAwake) {
		return 0
	}
	return jackSettle
}
