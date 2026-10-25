//go:build !panel

package speaker

// roomGain is the device's own volume on top of what is written: none on an Echo, whose volume is all
// in software.
func roomGain() float64 { return 1 }

// roomShare is how far apart two levels may be, as a share of them, beyond a step of a sample: none on
// an Echo, where both are the same sums.
const roomShare = 0
