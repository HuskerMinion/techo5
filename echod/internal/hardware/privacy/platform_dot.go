//go:build dot

package privacy

// Fire OS 5 leaves the mute line and the LED line unclaimed, so echod drives them itself through
// sysfs GPIO. Fire OS 6 binds both to the keypad driver — the mute button is the PMIC power key, and
// the driver's own handler performs the cut — so exporting them fails and the driver's interface is
// the only way in. Which one a device has is a question about the device, not about its firmware, so
// it is answered by looking.
func platformMute() (Mute, error) {
	if present(state) {
		return driver{}, nil
	}
	return exported()
}

func platformLight() (LED, error) {
	if present(brightness) {
		return driverLED{}, nil
	}
	return exportedLED()
}

// Nothing to seed: the hardware holds the microphones, and the saved state reaches the latch through
// the mute feature. See privacy.Seed.
func seedSoftwareCut(bool) {}
