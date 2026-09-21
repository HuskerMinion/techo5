package announce

import (
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/led"
)

// What the ring says about an announcement.
//
// A microphone that is open and does not look open is the thing people mind most about a device like
// this, and on a Dot the ring is the only thing that can say so: it pulses for as long as the
// recording runs. An announcement arriving lights it briefly as well, so somebody who missed the
// first words knows where the voice came from.
//
// This is not the screens' job — a Show and a Spot say it in words — but the ring is on every device,
// so it is said here once for all three.

// arrivedFor is how long the ring says an announcement arrived: long enough to catch the eye, short
// enough not to outlast the voice.
const arrivedFor = 2500 * time.Millisecond

var (
	// speakingColor is the ring while this device is recording: the amber a turn already uses, since
	// it means the same thing — this microphone is open and what it hears is going somewhere.
	speakingColor = led.Color{R: 0xE0, G: 0x8A, B: 0x10}

	// arrivedColor is an announcement playing, which is somebody else's voice rather than this
	// device's own business.
	arrivedColor = led.Color{R: 0x2E, G: 0x8B, B: 0xC8}
)

func init() {
	f := Get()
	ring := led.Get().Claim(led.PriorityBusy)
	notice := led.Get().Claim(led.PriorityNotice)

	recording := false
	f.Changed.Listen(func(struct{}) {
		if now := f.Recording(); now != recording {
			recording = now
			if recording {
				ring.Play(led.EffectPulse, speakingColor)
			} else {
				ring.Clear()
			}
		}
	})

	f.Arrived.Listen(func(Message) { notice.PaintFor(led.Solid(arrivedColor), arrivedFor) })
}
