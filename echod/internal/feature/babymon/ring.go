package babymon

import (
	"sync"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/led"
)

// What the ring says while somebody listens.
//
// On a Dot the ring is the only thing that can say the microphones are open to someone, so it pulses
// amber for as long as anybody listens, as it does for an announcement being recorded and for takes of a
// word (feature/takes). A Show and a Spot have no ring and say it on the screen instead (feature/display).
var listeningColor = led.Color{R: 0xE0, G: 0x8A, B: 0x10}

func init() {
	f := Get()
	ring := led.Get().Claim(led.PriorityBusy)
	var mu sync.Mutex // Changed is emitted from every listener's own goroutine
	shown := false
	f.Changed.Listen(func(struct{}) {
		mu.Lock()
		defer mu.Unlock()
		now := f.Count() > 0
		if now == shown {
			return
		}
		shown = now
		if now {
			ring.Play(led.EffectPulse, listeningColor)
		} else {
			ring.Clear()
		}
	})
}
