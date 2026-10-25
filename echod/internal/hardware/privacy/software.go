//go:build spot || panel

package privacy

import "time"

// The Echo Spot on LineageOS's kernel has no privacy driver: the mute button is the keypad's power
// key and nothing in the hardware cuts the microphones (techo5-spot docs/hardware.md, "Mute button").
// A panel's microphone is Android's, handed to the daemon by the app. On both the cut is the daemon's
// own: while muted, the capture source hands on silence from the moment a frame
// leaves the device (SoftwareCut, read in hardware/mic), so wake detection, the pre-roll history and
// anything streamed all hear nothing. It is as strong as the daemon, and no stronger.
type software struct{}

func (software) Get() (bool, error) { return softwareCut.Load(), nil }

func (software) Set(muted bool) error {
	softwareCut.Store(muted)
	return nil
}

func (s software) Toggle() (bool, error) {
	now := !softwareCut.Load()
	return now, s.Set(now)
}

// The press is only news to the key driver, so the daemon toggles.
func (software) HardwareActs(bool) bool { return false }

func (software) Lag() time.Duration { return 0 }
