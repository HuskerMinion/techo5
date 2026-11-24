// Package echoref is the echo canceller's playback reference made in software, for a board whose card
// has no loopback of what it plays, or one the canceller cannot use: athena on a mainline kernel
// without the AFE's DL1 write-back (DL1_AWB_Record, mic/athena.go), which is the fallback there when
// that device does not open, and the 1st gen Echo Show 5, whose loopback is aliased above about
// 1.2 kHz (mic/softref_cronos.go). The speaker hands over every
// period it sends the card, stamped with when its last frame will be heard; the microphones take out
// what was heard over the stretch of time each of their reads covers.
//
// Both stamps come from the cards' own delay readings. On athena both cards run off the same AFE
// clocks, so the two never drift; on the 1st gen Echo Show 5 the capture comes over an FPGA, and a
// shared clock is assumed there, not known, so a lasting shift is followed (settle, moveAfter). Either
// way what is left is the jitter of reading a delay and the clock together, a few
// hundred microseconds, which Clock smooths away. The canceller (mic/cancel.go) has no delay search of
// its own: its taps reach 64 ms back from the reference sample it is given, so a reference a little
// early only spends a few taps, while one late misses the start of every echo. Lead makes it early on
// purpose.
package echoref

import (
	"encoding/binary"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/audio"
)

// Rate is the reference's rate, the microphones' frame rate.
const Rate = 16000

// Lead is how far ahead of the microphones' own time the reference is read: the error budget for the
// two delay readings and the path after each card (converter and amplifier latency) that neither
// reading sees. A first value, to be set from `techo5 echo` on a unit.
const Lead = 5 * time.Millisecond

const (
	size = 1 << 15 // about 2 s at Rate

	// decTaps is the decimator's length at the speaker's 48 kHz, in the same ratio as the
	// microphones' (192 at 96 kHz), so each delays by the same 1 ms.
	decTaps = 96

	// stale is how long after the last period the reference still counts: past it the speaker has
	// stopped, and the stamps no longer say where its audio is.
	stale = 250 * time.Millisecond
)

// inUse is whether a capture reads this reference (Use).
var inUse atomic.Bool

// Use says whether the microphones read this reference: the speaker feeds it only then, so a capture
// with the card's own reference costs nothing here.
func Use(on bool) { inUse.Store(on) }

// InUse is what Use last said.
func InUse() bool { return inUse.Load() }

// base is the origin of every stamp, so times are plain seconds on the monotonic clock.
var base = time.Now()

func seconds(t time.Time) float64 { return t.Sub(base).Seconds() }

// Clock maps a stream's sample count to when that sample was heard (or recorded), smoothing the
// readings it is given. A reading too far from the smoothed line is taken as the stream starting
// over and is snapped to.
type Clock struct {
	rate float64
	off  float64 // seconds: when sample 0 was heard
	ok   bool
}

// NewClock is a clock for a stream at rate.
func NewClock(rate int) Clock { return Clock{rate: float64(rate)} }

const (
	snap   = 0.003 // seconds off the line before a reading is taken as a restart
	smooth = 1.0 / 32
)

// Mark takes one reading, sample n heard at t, and returns when sample 0 was heard.
func (c *Clock) Mark(n uint64, t time.Time) float64 {
	inst := seconds(t) - float64(n)/c.rate
	if !c.ok || math.Abs(inst-c.off) > snap {
		c.off, c.ok = inst, true
	} else {
		c.off += (inst - c.off) * smooth
	}
	return c.off
}

// At is when sample n was heard, from the last reading, in seconds since base.
func (c *Clock) At(n uint64) float64 { return c.off + float64(n)/c.rate }

// Reset forgets the line, for a stream that starts over.
func (c *Clock) Reset() { c.ok = false }

// ring is the reference itself: what the speaker sent, at Rate, and when each sample is heard.
type ring struct {
	mu    sync.Mutex
	l, r  [size]float32
	end   uint64 // samples written
	clock Clock
	last  time.Time
	dec   [2]*audio.Decimator
	taps  int
	rate  int

	// next is where the read after the last one starts, follow whether there was a last one, and
	// away how many reads in a row the clocks have placed further than settle from next.
	next   float64
	follow bool
	away   int
}

// settle is how far, in samples at Rate, the clocks may place a read from where the last one ended and
// it still carries on from there. Placed by the clocks alone, every read lands a few samples off the
// last one's end, and on a capture read every 20 ms the reference would skip or repeat a few samples at
// every read: no canceller follows that above the bass. 2 ms is well above that jitter and below Lead.
// Further than that, read after read, is a real shift (a speaker underrun's gap, or two clocks that
// drift apart, as the 1st gen Echo Show 5's capture over its FPGA may not share the speaker's), and a
// reference that slid late past Lead would stop cancelling the echo; one late read, which moves its own
// clock reading by milliseconds while the audio has not moved, is held against (moveAfter).
const settle = 2 * Rate / 1000

// moveAfter is how many reads in a row have to be placed further than settle before the reference does
// move there, about half a second of 20 ms reads: one late read is a late read, and a stream that
// started over (an overrun) stays where it moved to.
const moveAfter = 25

// Once a read is placed, the reference is held there: nothing nudges it after the clocks. Measured on
// an Echo Show 5 1st gen with music for two minutes, held, it stayed on the same sample throughout
// (both cards run off the same clocks); nudged a sample at a time toward the clocks' average, or
// placed afresh whenever the speaker's own clock reading snapped, it wandered by up to eight samples,
// and every sample it moved cost the canceller what it had learned above 2 kHz, where a sample is a
// good part of a period. It moves only where the audio really did: a stream that started over on
// either side (restart, Rejoin), or a placement that stays more than settle away (moveAfter).

var played = &ring{clock: NewClock(Rate)}

// Push is the speaker's: one period as it goes to the card, interleaved stereo S16_LE at rate (a
// whole multiple of Rate), whose last frame is heard at heardEnd.
func Push(pcm []byte, rate int, heardEnd time.Time) {
	if rate%Rate != 0 || rate < Rate {
		return
	}
	p := played
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.rate != rate {
		p.rate, p.taps = rate, decTaps*rate/48000
		for i := range p.dec {
			p.dec[i] = audio.NewDecimator(rate/Rate, p.taps)
		}
		p.restart()
	}
	if p.clock.ok && heardEnd.Sub(p.last) > stale {
		// The speaker stopped and started over; nothing before now lines up with it.
		p.restart()
	}
	for at := 0; at+4 <= len(pcm); at += 4 {
		l := float32(int16(binary.LittleEndian.Uint16(pcm[at:]))) / (1 << 15)
		r := float32(int16(binary.LittleEndian.Uint16(pcm[at+2:]))) / (1 << 15)
		yl, ok := p.dec[0].Push(l)
		yr, _ := p.dec[1].Push(r)
		if ok {
			p.l[p.end%size], p.r[p.end%size] = yl, yr
			p.end++
		}
	}
	if p.end == 0 {
		return
	}
	// The newest sample out of the decimator is the input from its delay back.
	delay := time.Duration(float64(p.taps-1) / 2 / float64(rate) * float64(time.Second))
	p.clock.Mark(p.end-1, heardEnd.Add(-delay))
	p.last = heardEnd
}

// restart starts the reference over: no samples, the decimators' history gone and the line forgotten.
// What was there before belongs to audio that ended, and kept, the new line would place it just
// before the new audio, and the decimators would smear its tail into the first new samples.
func (p *ring) restart() {
	p.end = 0
	p.follow = false
	for _, d := range p.dec {
		d.Reset()
	}
	p.clock.Reset()
}

// Fill writes into l and r what the speaker played over len(l) samples at Rate, the first heard at
// start (seconds since base, as Clock.At gives), read Lead ahead. Consecutive reads carry on from
// each other (settle), so it is meant for one reader, the capture. Where there is nothing (the speaker
// is not running, or the stretch is out of the ring) it writes silence. It reports whether any of it
// was the speaker's.
func Fill(l, r []float32, start float64) bool {
	p := played
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.clock.ok || time.Since(p.last) > stale+time.Second {
		clear(l)
		clear(r)
		p.follow = false
		return false
	}
	first := math.Round((start + Lead.Seconds() - p.clock.off) * Rate)
	if p.follow {
		switch d := first - p.next; {
		case math.Abs(d) <= settle:
			p.away = 0
			first = p.next
		case p.away+1 < moveAfter:
			p.away++
			first = p.next
		default:
			p.away = 0
		}
	} else {
		p.follow, p.away = true, 0
	}
	p.next = first + float64(len(l))
	any := false
	for i := range l {
		idx := first + float64(i)
		if idx < 0 || idx >= float64(p.end) || float64(p.end)-idx > size {
			l[i], r[i] = 0, 0
			continue
		}
		n := uint64(idx) % size
		l[i], r[i] = p.l[n], p.r[n]
		any = true
	}
	return any
}

// Rejoin is the reader's: its stream started over (it opened, or lost frames to an overrun), so its
// next read is placed by the clocks afresh rather than carrying on from the last one, which was
// before the gap: carried on, the reference would be late by the gap until moveAfter reads had passed.
func Rejoin() {
	p := played
	p.mu.Lock()
	p.follow = false
	p.mu.Unlock()
}

// Seconds is t as Fill and Clock count it.
func Seconds(t time.Time) float64 { return seconds(t) }
