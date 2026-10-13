//go:build linux && spot

package btaudio

// takesPhones: the Echo Spot runs bluez-alsa as an A2DP source only (tools/linux/techo5-lib.sh, a
// Broadcom controller), so a phone would pair and never have its audio come up. No phone pairing.
const takesPhones = false
