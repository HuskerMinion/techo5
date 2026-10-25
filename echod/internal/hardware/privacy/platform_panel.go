//go:build panel

package privacy

// A panel has no mute line of its own: the tablet's microphone is Android's, and the app hands the
// daemon what it records. The Shows' latch is not looked for at all, because a tablet on the Echo Show
// 5's MediaTek chip (the 8th gen Fire HD 8) can carry nodes with the Shows' names, and they are not a
// mute the daemon may drive. The mute is the daemon's own, as on the Spot (software.go): while it is
// on, what the app records is handed on as silence. Android's own privacy switch silences the app as
// well, which the capture notices (hardware/mic, flat_panel.go).
func platformMute() (Mute, error) { return software{}, nil }

// noLED stands in for a mute light a tablet does not have.
type noLED struct{}

func (noLED) SetBright(bool) error  { return nil }
func (noLED) Bright() (bool, error) { return true, nil }

func platformLight() (LED, error) { return noLED{}, nil }

// The cut is the daemon's own, so a mute saved from last time is in force before capture opens. See
// privacy.Seed.
func seedSoftwareCut(muted bool) { softwareCut.Store(muted) }
