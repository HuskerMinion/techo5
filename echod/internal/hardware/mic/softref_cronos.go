//go:build !dot && !spot && !panel

package mic

import (
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/alsa"
	"github.com/HuskerMinion/techo5/echod/internal/lib/echoref"
)

// The 1st gen Echo Show 5's playback loopback cannot be canceled against above the bass. The FPGA
// that packs it beside the microphones brings it down to 16 kHz without filtering it first, so
// everything the speaker was sent between 8 and 24 kHz folds back under 8 kHz: the loopback carries
// as much at 7.9 kHz as at 2 kHz, where the microphones hear the music fall away. Measured on a unit
// with music at a normal listening level, the loopback's coherence with the microphones was 0.93 at
// 150-300 Hz and under 0.06 from 1.2 to 4 kHz, so the canceller took 15 dB of music off the bass and
// nothing at all off the band a wake word is heard in.
//
// So on checkers the reference is the one made in software from what the speaker sent (lib/echoref),
// put where the loopback was, and nothing past the capture knows the difference. On the same music it
// took 17 dB off 0.6-1.2 kHz and 5 to 7 dB off 1.2-8 kHz, where the loopback took 0 to 3.5, and with
// the same music at the same volume the wake word was heard 8 times in two minutes where it had been
// heard twice.
//
// Only checkers: the 2nd gen Show 5 and the Show 8 (crown) read their loopback through what looks
// like the same FPGA, but nobody has measured theirs.

// delayedCapture is a capture that can say how long ago its newest frame was recorded, and how it is
// open: the External ASP's native path (native.go) reads the device's format from Config, and gets
// the software reference in channels 2 and 3, where it expects the card's loopback, as everything else
// does.
type delayedCapture interface {
	capture
	Delay() (int, error)
	Config() alsa.Config
}

// softRefCapture is a capture with the loopback channels replaced by the software reference.
type softRefCapture struct {
	delayedCapture
	clock echoref.Clock
	got   uint64 // frames read since the capture opened
	l, r  []float32
}

// withSoftRef is c, with the software reference in place of its loopback where the board wants that.
func withSoftRef(c capture) capture {
	d, ok := c.(delayedCapture)
	if !layout.Checkers() || !ok || Refs != 2 {
		return c
	}
	echoref.Use(true)
	echoref.Rejoin()
	return &softRefCapture{delayedCapture: d, clock: echoref.NewClock(Rate)}
}

// Read reads the capture and writes the reference over its loopback channels: what the speaker was
// heard playing over the stretch the read covers. A read the capture's delay cannot be had for carries
// silence there, as nothing playing does.
func (c *softRefCapture) Read(buf []byte) (int, error) {
	n, err := c.delayedCapture.Read(buf)
	if err != nil {
		// An overrun starts the capture over; the next reading places it again, and the reference
		// with it, not carrying on from before the frames that were lost.
		c.clock.Reset()
		echoref.Rejoin()
	}
	frameBytes := Channels * Bits / 8
	frames := n / frameBytes
	if frames <= 0 {
		return n, err
	}
	if len(c.l) < frames {
		c.l, c.r = make([]float32, frames), make([]float32, frames)
	}
	l, r := c.l[:frames], c.r[:frames]
	ok := false
	if d, derr := c.Delay(); derr == nil && d >= 0 {
		// The last frame read was recorded d frames ago.
		last := time.Now().Add(-time.Duration(d) * time.Second / Rate)
		c.clock.Mark(c.got+uint64(frames)-1, last)
		ok = echoref.Fill(l, r, c.clock.At(c.got))
	} else {
		// No delay to place this read by (the device is in a state that has none, as after an xrun
		// before the restart is noticed): the next read that has one is placed afresh, as after an
		// overrun, rather than followed on from a placing this gap has made stale.
		echoref.Rejoin()
	}
	if !ok {
		clear(l)
		clear(r)
	}
	for f := range frames {
		o := f*frameBytes + 3*RefFirst
		putS24(buf[o:], l[f])
		putS24(buf[o+3:], r[f])
	}
	c.got += uint64(frames)
	return n, err
}
