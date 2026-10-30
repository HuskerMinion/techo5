package takes

import (
	"sync"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/led"
)

// What the ring says about a recording.
//
// On a Dot the ring is the only thing that can say the microphones are open, so it pulses amber for as
// long as a recording runs, as it does for an announcement being recorded, and a soft white while a
// recording waits for the press, so that whoever was asked to press knows the device is the one
// asking. A Show and a Spot have no ring and say both on the screen instead (feature/display).

var (
	// recordingColor is announce's: this microphone is open and what it hears is going somewhere.
	recordingColor = led.Color{R: 0xE0, G: 0x8A, B: 0x10}

	// waitingColor is a recording waiting for the press.
	waitingColor = led.Color{R: 0x90, G: 0x90, B: 0x90}
)

func init() {
	f := Get()
	ring := led.Get().Claim(led.PriorityBusy)
	// Changed is emitted from the page, the button, the recording and the timers at once: what the ring
	// shows is decided one at a time, from the recorder as it is then.
	var mu sync.Mutex
	shown := Idle
	f.Changed.Listen(func(struct{}) {
		mu.Lock()
		defer mu.Unlock()
		now := f.Card().Phase
		if now == shown {
			return
		}
		shown = now
		switch now {
		case Armed:
			ring.Play(led.EffectPulse, waitingColor)
		case Recording:
			ring.Play(led.EffectPulse, recordingColor)
		default:
			ring.Clear()
		}
	})
}
