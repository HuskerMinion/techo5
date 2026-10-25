//go:build arm64 && !noasm

package picscale

// haveNEON is whether the passes run in scale_arm64.s. Every arm64 core has NEON.
const haveNEON = true

// verticalNEON is verticalGo for 16*n16 columns, taps rows of w.
//
//go:noescape
func verticalNEON(mid *int16, src *byte, stride int, w *int16, taps int, n16 int)

// horizontalNEON is horizontalGo for 8*n8 outputs of wide taps each.
//
//go:noescape
func horizontalNEON(out *byte, mid *int16, start *int32, w *int16, n8 int)

// horizontal4NEON is horizontal4Go for 2*n2 outputs of wide taps each.
//
//go:noescape
func horizontal4NEON(out *byte, mid *int16, start *int32, w *int16, n2 int)

// toRGBANEON is toRGBAGo for 8*n8 pixels.
//
//go:noescape
func toRGBANEON(row *byte, y, cb, cr *byte, n8 int)

// horizontal4tapsNEON is horizontalGo for 8*n8 outputs of four taps each.
//
//go:noescape
func horizontal4tapsNEON(out *byte, mid *int16, start *int32, w *int16, n8 int)
