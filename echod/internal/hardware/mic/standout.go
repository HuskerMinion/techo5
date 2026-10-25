package mic

import (
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// How far what was just said stood out from the room, for deciding which of several devices that heard
// the same wake word heard it best (feature/nearest).
//
// It is measured against this device's own floor rather than full scale, so a device with hotter
// microphones, or one in a louder room, does not win for that alone; and in the leveler's band, where a
// voice is and the ring's whine and the room's hum are not. It is taken before the noise reduction, which
// would lower the floor and so make a device that has it on stand out more for nothing.
//
// It runs only while something has asked for it: while nearest device answers is on, which it is by
// default, with or without a house word to take part with. What it costs is a band filter and a sum on
// each frame.
type standout struct {
	on atomic.Bool

	// fresh asks the reader to forget what it learned, at its next frame: the floor and the filter are
	// the reader's alone, so nothing else may clear them.
	fresh atomic.Bool

	// The reader's own: the band filter, the floor in that band, and how far each block of frames
	// stood over it.
	lp    [levelPoles]float32
	hp    float32
	floor float32
	sum   float64
	n     int

	mu     sync.Mutex
	blocks [standoutBlocks]float32 // dB over the floor, newest at at-1
	at     int
	filled bool
}

const (
	// standoutBlock is how much is averaged into one reading: long enough that a click or a single
	// plosive does not decide it, short enough to land inside the word.
	standoutBlock = 5 // frames, 100 ms

	// standoutBlocks is how far back the readings go: the longest wake word and then some.
	standoutBlocks = 20 // 2 s

	// standoutMax is the most anything stands out, in dB: more than any voice over any room.
	standoutMax = 90
)

var (
	standoutLP = float32(1 - math.Exp(-2*math.Pi*levelHighHz/Rate))
	standoutHP = float32(1 - math.Exp(-2*math.Pi*levelLowHz/Rate))
)

// add takes one frame of the mix. least is the lowest the floor may go at the analog gain now.
func (s *standout) add(frame []int16, least float32) {
	if !s.on.Load() || len(frame) == 0 {
		return
	}
	if s.fresh.Swap(false) {
		s.lp, s.hp, s.floor, s.sum, s.n = [levelPoles]float32{}, 0, 0, 0, 0
	}
	frameDur := float64(len(frame)) / Rate
	down := float32(1 - math.Exp(-frameDur/floorFall.Seconds()))
	up := float32(1 - math.Exp(-frameDur/floorRise.Seconds()))

	var band float64
	for _, v := range frame {
		x := float32(v)
		for i := range s.lp {
			s.lp[i] += (x - s.lp[i]) * standoutLP
			x = s.lp[i]
		}
		s.hp += (x - s.hp) * standoutHP
		x -= s.hp
		band += float64(x) * float64(x)
	}
	rms := float32(math.Sqrt(band / float64(len(frame))))
	if s.floor == 0 {
		s.floor = max(rms, least)
	}
	s.floor = follow(s.floor, rms, down, up, least)

	s.sum += band / float64(len(frame))
	s.n++
	if s.n < standoutBlock {
		return
	}
	// Clamped, since a floor of nothing (no least to hold it up) would make anything at all infinitely
	// loud, and an infinity is not a number a claim can carry.
	over := float32(0)
	if mean := math.Sqrt(s.sum / float64(s.n)); s.floor > 0 && mean > float64(s.floor) {
		over = float32(min(20*math.Log10(mean/float64(s.floor)), standoutMax))
	}
	s.sum, s.n = 0, 0

	s.mu.Lock()
	s.blocks[s.at] = over
	s.at = (s.at + 1) % len(s.blocks)
	s.filled = s.filled || s.at == 0
	s.mu.Unlock()
}

// leastNow is the lowest a floor may go at the analog gain now, for a meter of its own.
func (l *leveler) leastNow() float32 { return math.Float32frombits(l.least.Load()) }

// best is the most any reading in the last d stood over the floor, in dB.
func (s *standout) best(d time.Duration) float64 {
	want := int(d / (standoutBlock * FrameSamples * time.Second / Rate))
	s.mu.Lock()
	defer s.mu.Unlock()
	have := s.at
	if s.filled {
		have = len(s.blocks)
	}
	want = min(max(want, 1), have)
	var top float32
	for i := 1; i <= want; i++ {
		top = max(top, s.blocks[(s.at-i+len(s.blocks))%len(s.blocks)])
	}
	return float64(top)
}

// SetStandout starts or stops measuring how far speech stands out from the room. It takes a moment to
// learn the floor after it starts, so whatever wants it turns it on ahead of needing it. Turned on, it
// starts from nothing: readings from before it was last turned off are about another time.
func (s *Source) SetStandout(on bool) {
	if on && !s.standout.on.Load() {
		s.standout.mu.Lock()
		s.standout.blocks, s.standout.at, s.standout.filled = [standoutBlocks]float32{}, 0, false
		s.standout.mu.Unlock()
		s.standout.fresh.Store(true)
	}
	s.standout.on.Store(on)
}

// Standout is the most that anything in the last d stood out from this device's room, in dB over its own
// floor; zero while it is not being measured.
func (s *Source) Standout(d time.Duration) float64 {
	if !s.standout.on.Load() {
		return 0
	}
	return s.standout.best(d)
}
