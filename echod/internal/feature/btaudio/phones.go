//go:build linux && !spot

package btaudio

// takesPhones: the Shows and the Dot run bluez-alsa as an A2DP sink too, so a phone can play to them,
// and they have phone pairing.
const takesPhones = true
