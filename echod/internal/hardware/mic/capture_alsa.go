//go:build !panel

package mic

import (
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/alsa"
)

// hasMixer is whether the microphones' converters have gain controls in an ALSA mixer here.
const hasMixer = true

// afterOpen points the converters at their microphones and sets the gain, once the device is held.
func afterOpen() {
	routeInputs()
	applyGain(config.Get().Microphone.Gain)
}

// openPlain opens the one capture device that carries the microphones and the loopback together.
func openPlain() (capture, error) {
	card, device, cfg, err := captureDevice()
	if err != nil {
		return nil, err
	}
	pcm, err := alsa.Open(card, device, cfg)
	if err != nil {
		return nil, err
	}
	return pcm, nil
}
