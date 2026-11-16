//go:build !dot

package display

import "image/color"

// answerGreen and declineRed are the green that takes or places a call and the red that ends or
// refuses something, whatever the theme and on every device: every phone uses them, and a call is the
// one page where a wrong tap cannot be taken back. The red is also the Talk control's while the
// microphones are going to a camera, as a recording light is, since ending that is the same answer.
//
// The ringing page's Stop is not one of these on purpose: stopping a ring is the safe answer and the
// one most wanted, so it is the theme's highlighted button.
var (
	answerGreen = color.RGBA{0x2e, 0xa0, 0x4f, 0xff}
	declineRed  = color.RGBA{0xc6, 0x3a, 0x32, 0xff}
)
