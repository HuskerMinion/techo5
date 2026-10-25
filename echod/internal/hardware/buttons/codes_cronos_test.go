//go:build !dot && !spot && !panel

package buttons

import "testing"

// The mute button is KEY_POWER under Amazon's kernels and KEY_MICMUTE on the 2nd gen Show 8's
// mainline kernel; both are the mute, and neither is anything else.
func TestShowMuteCodes(t *testing.T) {
	for code, want := range map[uint16]Name{114: VolumeDown, 115: VolumeUp, 116: Mute, 248: Mute} {
		if got, ok := codes[code]; !ok || got != want {
			t.Errorf("code %d is %q (%v), want %q", code, got, ok, want)
		}
	}
	if len(codes) != 4 {
		t.Errorf("%d codes, want 4: %v", len(codes), codes)
	}
}

// Devices that declare the mute's code but are not the mute button: the FocalTech touchscreen's
// gestures, the 2nd gen Show 8's PMIC power key on mainline and the Skylight's RK809 power key. The
// real buttons, the Skylight's adc-keys among them, are read.
func TestShowNotButtons(t *testing.T) {
	for name, want := range map[string]bool{
		"fts_ts": true, "mtk-pmic-keys": true, "rk805 pwrkey": true,
		"gpio-keys": false, "gpio-privacy-button": false, "goodix-ts": false, "adc-keys": false,
	} {
		if notButtons[name] != want {
			t.Errorf("notButtons[%q] = %v, want %v", name, notButtons[name], want)
		}
	}
}
