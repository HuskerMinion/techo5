package speaker

import (
	"log/slog"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// The write loop never stops: it feeds silence while idle, because the stream paces it and the echo
// canceller wants the reference, and on the boards whose amplifier is gated by AmpSwitch that is
// enough. On the 1st gen Show 5 it is not. Its amplifier is a plain enable on a GPIO, set on once at
// start (showInit) and never touched again, so it amplifies the codec's idle output for as long as the
// daemon runs: a hum heard across a quiet room, which TWRP, with nothing holding the PCM, does not
// have (#291). Taking the amplifier off while nothing plays ends it, measured on a unit 2026-10-25:
// with Ext_Speaker_Amp_Switch On (amp_gpio hi) the room was quiet.
//
// So on a board with an ampGate the amplifier sleeps once nothing has wanted the speaker for
// idleAfter, and wakes before the next sound is let out. Everything else stays as it was: the stream
// keeps running on silence, and only the amplifier and the line-out's mute change.

// idleAfter is how long the speaker has to be wanted by nothing before its amplifier sleeps: long
// enough that the gaps inside a turn (a wake chime, then the reply) and between a ring's chimes do not
// put it to sleep and wake it again, which would cost a settle at the start of each.
const idleAfter = 10 * time.Second

// wakeSettle is how long the amplifier sits enabled behind the muted line-out before the mute is let
// go, so its own start-up is not heard: muteSettle, the Echo 2nd gen's (paths_radar_dot.go). Tried on
// a 1st gen Show 5 on 2026-10-25 with the wake chime, voice replies, a timer's ring and the radio, each
// starting on a sleeping amplifier: no pop going up or down, and every start intact.
const wakeSettle = muteSettle

// ampGate is an amplifier the player may switch off while idle: its enable, the values that turn it
// on and off, and the codec's mute in front of it, a switch at 1 when it plays. The values are kept
// apart from the control's name because on checkers they are the reverse of what they say (active
// low): "Off" is the amplifier on.
type ampGate struct {
	control string
	on, off string
	mute    string
}

// sleep is the order that does not pop going down: the line-out muted first, so the amplifier is
// switched off with nothing reaching it, then the amplifier.
func (g *ampGate) sleep() []kctl {
	return []kctl{
		{name: g.mute, level: 0},
		{name: g.control, value: g.off},
	}
}

// wake is sleep the other way round: the amplifier on, and the line-out let go wakeSettle later
// (unmute). Only where the speaker is the output does the mute belong to the amplifier; on the jack
// the headphone path has the line-out muted on purpose, and the amplifier only comes back on.
func (g *ampGate) wake(speaker bool) (on, unmute []kctl) {
	on = []kctl{{name: g.control, value: g.on}}
	if speaker {
		unmute = []kctl{{name: g.mute, level: 1}}
	}
	return on, unmute
}

// ampState is where the gated amplifier is. Sleeping and waking are the mixer writes in progress, off
// the write loop: on the 1st gen Show 5 codec writes are slow enough to miss the ring (see outNow).
type ampState int

const (
	ampAwake ampState = iota
	ampSleeping
	ampAsleep
	ampWaking
)

// ampAct is what the write loop is to start.
type ampAct int

const (
	ampNone ampAct = iota
	ampSleep
	ampWake
)

// idleAmp decides, a period at a time, when the gated amplifier sleeps and wakes. It only decides: the
// mixer writes are the Player's (ampSleep, ampWake), which report back with settled.
type idleAmp struct {
	mu    sync.Mutex
	state ampState
	// since is when the speaker was last found wanted by nothing, zero while it is wanted.
	since time.Time
}

// step is one period of the write loop, which wants the speaker (busy) or not. It says what to start,
// and whether to hold the queues back this period and write silence instead: from the moment a sound
// is waiting for an amplifier that is not on yet until the amplifier is on and settled, so the first
// syllable is late by the wake rather than lost to it. Going down holds nothing back unless something
// arrives meanwhile, which then waits for the wake after it.
func (a *idleAmp) step(now time.Time, busy bool) (act ampAct, hold bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch a.state {
	case ampAwake:
		if busy {
			a.since = time.Time{}
			return ampNone, false
		}
		if a.since.IsZero() {
			a.since = now
		}
		if now.Sub(a.since) < idleAfter {
			return ampNone, false
		}
		a.state = ampSleeping
		return ampSleep, false
	case ampAsleep:
		if !busy {
			return ampNone, false
		}
		a.state = ampWaking
		return ampWake, true
	case ampWaking:
		return ampNone, true
	default: // ampSleeping
		return ampNone, busy
	}
}

// is reports whether the amplifier is in s.
func (a *idleAmp) is(s ampState) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state == s
}

// settled is the mixer writes done, leaving the amplifier in s. Awake starts the idle count again.
func (a *idleAmp) settled(s ampState) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state = s
	a.since = time.Time{}
}

// Hold is the Driver saying whether a claim holds the speaker. A claim is taken before its errand has
// queued anything (a reply is still being fetched), so a sleeping amplifier wakes from here rather
// than from the first audio, and its settle is over by the time that audio arrives.
func (p *Player) Hold(held bool) { p.claimed.Store(held) }

// wanted is whether anything wants the speaker this period: a claim, or audio in any lane, a ring in
// progress, a Source attached. Audio going to a sink does not want the codec's amplifier at all.
func (p *Player) wanted() bool {
	if p.sink.Load() != nil {
		return false
	}
	if p.claimed.Load() || p.ringing.Load() {
		return true
	}
	p.srcMu.Lock()
	src := p.src
	p.srcMu.Unlock()
	if src != nil {
		return true
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.pending) > 0 || len(p.bed) > 0 || len(p.under) > 0 || len(p.bell) > 0
}

// idleStep is the write loop's turn at the gated amplifier, once a period: it starts a sleep or a wake
// where one is due and reports whether this period's audio must wait (idleAmp.step).
func (p *Player) idleStep() bool {
	act, hold := p.idle.step(time.Now(), p.wanted())
	switch act {
	case ampSleep:
		safe.Go("speaker amplifier sleep", p.ampSleep)
	case ampWake:
		safe.Go("speaker amplifier wake", p.ampWake)
	}
	return hold
}

// ampSleep switches the gated amplifier off, line-out first. Only on the speaker: with the jack the
// output, the line-out's mute is the headphone path's and is left alone, and the amplifier stays as
// it is; and not while a switch to the jack drains, which writes the path itself after (setOutput).
// Declining counts as awake, so it is tried again idleAfter later. So does a sleep overtaken while it
// waited for pathMu: by a wake (amp, from a path change) or by something wanting the speaker again,
// which would otherwise go out through an amplifier switched off under it. A sleep whose writes fail
// leaves the amplifier on, so it counts as awake too.
func (p *Player) ampSleep() {
	p.pathMu.Lock()
	defer p.pathMu.Unlock()
	if p.out != OutputSpeaker || p.draining || !p.idle.is(ampSleeping) || p.wanted() {
		p.idle.settled(ampAwake)
		return
	}
	// Asleep even when a write failed: half a sleep (the line-out muted, the amplifier left on, or the
	// other way round) counted as awake would never be undone, and a ring would play into it unheard.
	// Asleep, the next sound runs the whole wake, which puts both back.
	if !gateWrite(p, p.gate.sleep()) {
		slog.Warn("speaker: putting the amplifier to sleep failed; the next sound wakes it in full")
	}
	p.idle.settled(ampAsleep)
}

// ampWake switches the gated amplifier on and, where the speaker is the output and its path is in
// place, lets go of the line-out wakeSettle later. pathMu is held across the settle, as amp holds it
// across muteSettle, so no path is written between the two halves.
func (p *Player) ampWake() {
	p.pathMu.Lock()
	defer p.pathMu.Unlock()
	on, unmute := p.gate.wake(p.out == OutputSpeaker && !p.draining)
	if !gateWrite(p, on) {
		// The amplifier may still be off: left awake, a ring or an alarm would play into it unheard.
		// Asleep, the next period that wants the speaker tries the wake again, the audio held meanwhile;
		// after wakeTries in a row the audio is let out anyway rather than held for good.
		if p.wakeFails.Add(1) < wakeTries {
			p.idle.settled(ampAsleep)
			return
		}
		slog.Error("speaker: the amplifier would not switch on; playing anyway", "tries", wakeTries)
	}
	p.wakeFails.Store(0)
	if len(unmute) > 0 {
		time.Sleep(wakeSettle)
		p.apply(unmute)
	}
	p.idle.settled(ampAwake)
}

// wakeTries is how many wakes in a row may fail before the audio held for them is let out anyway.
const wakeTries = 5

// gateWrite makes the gated amplifier's writes and says whether all of them were made; a variable for
// the tests, which have no mixer.
var gateWrite = func(p *Player, seq []kctl) bool { return p.applyAll(seq) }
