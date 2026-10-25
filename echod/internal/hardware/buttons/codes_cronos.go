//go:build !dot && !spot && !panel

package buttons

// longHoldAfter is zero: the Show's screen is where pairing and everything else is asked for.
const longHoldAfter = 0

// The Echo Show 5's three buttons, by evdev key code. Volume comes from gpio-keys; the mute button
// is its own input device, gpio-privacy-button, which the kernel reports as KEY_POWER (116). There
// is no action button.
//
// The 2nd gen Show 8 on TECHO5's mainline kernel has no gpio-privacy driver: its device tree puts the
// mute button in gpio-keys as KEY_MICMUTE (248), so nothing on the system takes it for a power key.
// None of the Shows on Amazon's kernels reports 248. The latch still moves in hardware there, so the
// mute feature follows it as on the 2nd gen Show 5 (privacy's mainlineMute).
var codes = map[uint16]Name{
	114: VolumeDown,
	115: VolumeUp,
	116: Mute,
	248: Mute,
}

// notButtons are input devices that are not buttons, by name, though they declare key codes. The Show
// 8's FocalTech lists KEY_POWER (116, the mute button's code) among its gesture keys: on the 2nd gen,
// read off the unit 2026-10-14, next to the letter and arrow gestures. A gesture read as a button would
// toggle the mute, so the touchscreen is left to the touch package.
//
// On the 2nd gen Show 8's mainline kernel the MT6358 PMIC's keys driver (mtk-pmic-keys) reports its
// power key as KEY_POWER too. The mute button there is KEY_MICMUTE in gpio-keys, so the PMIC's 116 is
// never the mute, and is not read.
//
// The Skylight Calendar 15 has no mute button at all (no microphone). Its RK809 PMIC's power key
// ("rk805 pwrkey", the rk808 driver's name for the whole family) reports KEY_POWER, which would read
// as the mute, so it is not read either. Its volume keys are adc-keys, 114 and 115.
var notButtons = map[string]bool{"fts_ts": true, "mtk-pmic-keys": true, "rk805 pwrkey": true}
