package babymon

import (
	"sync"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/led"
)

// What the ring says while somebody listens.
//
// On a Dot the ring is the only thing that can say the microphones are open to someone, so it pulses
// amber for as long as anybody listens, as it does for an announcement being recorded and for takes of a
// word (feature/takes). A Show and a Spot have no ring and say it on the screen instead (feature/display).
//
// Baby monitor light changes that: Dimmest keeps one segment at the least amber the ring can show,
// steady, and Off leaves the ring to whatever else it shows.
var (
	listeningColor = led.Color{R: 0xE0, G: 0x8A, B: 0x10}
	dimmestColor   = led.Color{R: 10, G: 5, B: 0}
)

// dimmestFrame is the ring at Dimmest: one segment lit.
func dimmestFrame() []led.Color {
	frame := make([]led.Color, led.Segments)
	frame[0] = dimmestColor
	return frame
}

// ringFor is what the ring shows: nothing ("") while nobody listens or the light is Off, else the
// light choice ("normal" for Normal).
func ringFor(listening bool, light string) string {
	switch {
	case !listening || light == config.BabyLightOff:
		return ""
	case light == config.BabyLightDim:
		return config.BabyLightDim
	}
	return "normal"
}

func init() {
	f := Get()
	ring := led.Get().Claim(led.PriorityBusy)
	var mu sync.Mutex // Changed is emitted from every listener's own goroutine
	shown := ""
	f.Changed.Listen(func(struct{}) {
		mu.Lock()
		defer mu.Unlock()
		now := ""
		if f.Count() > 0 {
			now = ringFor(true, light())
		}
		if now == shown {
			return
		}
		shown = now
		switch now {
		case "":
			ring.Clear()
		case config.BabyLightDim:
			ring.Paint(dimmestFrame())
		default:
			ring.Play(led.EffectPulse, listeningColor)
		}
	})
}
