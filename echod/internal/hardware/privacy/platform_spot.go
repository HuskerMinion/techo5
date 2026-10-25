//go:build spot

package privacy

// noLED stands in for a mute light the Spot does not have; the screen will show the state (M5).
type noLED struct{}

func (noLED) SetBright(bool) error  { return nil }
func (noLED) Bright() (bool, error) { return true, nil }

func platformMute() (Mute, error) { return software{}, nil }

func platformLight() (LED, error) { return noLED{}, nil }

// The Spot's cut is the daemon's own, so a mute saved from last time has to be in force before
// capture opens rather than when the mute feature starts. See privacy.Seed.
func seedSoftwareCut(muted bool) { softwareCut.Store(muted) }
