//go:build !arm64 || noasm

package picscale

// Built for anything but arm64, and for arm64 with the noasm tag, so that the Go passes can be
// measured against the NEON ones on the hardware that matters.

const haveNEON = false

func verticalNEON(mid *int16, src *byte, stride int, w *int16, taps int, n16 int) {
	panic("picscale: no NEON")
}

func horizontalNEON(out *byte, mid *int16, start *int32, w *int16, n8 int) {
	panic("picscale: no NEON")
}

func horizontal4NEON(out *byte, mid *int16, start *int32, w *int16, n2 int) {
	panic("picscale: no NEON")
}

func toRGBANEON(row *byte, y, cb, cr *byte, n8 int) { panic("picscale: no NEON") }

func horizontal4tapsNEON(out *byte, mid *int16, start *int32, w *int16, n8 int) {
	panic("picscale: no NEON")
}
