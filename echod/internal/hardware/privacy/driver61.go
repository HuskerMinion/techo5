//go:build dot

package privacy

import (
	"errors"
	"time"
)

// On the Echo 2nd gen's Linux 6.1 kernel (LibreEcho's) the mute is its own amz_privacy platform driver
// (drivers/misc/amz_privacy.c). It owns the mute and lamp lines, so they cannot be exported, and it acts
// on the button itself. privacy_state is the button's mute; mute_lamp is the software one: writing 1 cuts
// the microphones in hardware and lights the lamp, 0 clears it. Nothing lets software leave a mute the
// button made: the kernel refuses that by design, so Set(false) says so instead of claiming it worked.
const (
	dir61   = "/sys/bus/platform/devices/amz_privacy"
	state61 = dir61 + "/privacy_state"
	lamp61  = dir61 + "/mute_lamp"
)

type driver61 struct{}

func (driver61) Get() (bool, error) {
	button, err := reads(state61, "1")
	if err != nil {
		return false, err
	}
	soft, err := reads(lamp61, "1")
	return button || soft, err
}

func (driver61) HardwareActs(bool) bool { return true }

func (driver61) Lag() time.Duration { return 500 * time.Millisecond }

func (d driver61) Set(muted bool) error {
	if muted {
		if is, err := d.Get(); err != nil || is {
			return err
		}
		return write(lamp61, "1")
	}
	// The software mute is cleared whatever the button says, so a button press that ends the mute later
	// leaves nothing behind; while the button holds the microphones, the kernel keeps them.
	_ = write(lamp61, "0")
	if button, _ := reads(state61, "1"); button {
		return errors.New("the mute button holds the microphones; press it to release them")
	}
	return nil
}

func (d driver61) Toggle() (bool, error) {
	is, err := d.Get()
	if err != nil {
		return false, err
	}
	return !is, d.Set(!is)
}

// driverLED61 is the lamp on 6.1: one level, lit with the mute line, with nothing to dim it.
type driverLED61 struct{}

func (driverLED61) SetBright(bool) error  { return nil }
func (driverLED61) Bright() (bool, error) { return true, nil }
