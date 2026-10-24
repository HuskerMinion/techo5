//go:build panel

package privacy

import (
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/micsilent"
)

// Android's physical switch can cut capture without changing AudioManager's mute flag or posting an
// input event. The capture's flat-stream detector observes that cut. Home Assistant and the screen
// also have a software cut; Get reports either one, while Set changes only the software cut. A
// physical cut is never saved as a software preference across a restart.
type panelMute struct{}

func (panelMute) Get() (bool, error) { return softwareCut.Load() || micsilent.Cut(), nil }
func (panelMute) Set(muted bool) error {
	softwareCut.Store(muted)
	return nil
}
func (p panelMute) Toggle() (bool, error) {
	was, _ := p.Get()
	if err := p.Set(!was); err != nil {
		return was, err
	}
	return p.Get()
}
func (panelMute) HardwareActs(bool) bool { return false }
func (panelMute) Lag() time.Duration     { return 0 }
func (panelMute) Persisted() bool        { return softwareCut.Load() }
func (panelMute) Observe(fn func()) (cancel func()) {
	return micsilent.CutChanged.Listen(func(bool) { fn() })
}

func platformMute() (Mute, error) { return panelMute{}, nil }

// noLED stands in for a mute light a tablet does not have.
type noLED struct{}

func (noLED) SetBright(bool) error  { return nil }
func (noLED) Bright() (bool, error) { return true, nil }

func platformLight() (LED, error) { return noLED{}, nil }

// A software mute saved from Home Assistant must apply before microphone capture starts.
func seedSoftwareCut(muted bool) { softwareCut.Store(muted) }
