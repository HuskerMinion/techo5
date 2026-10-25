//go:build !dot

package display

import (
	"fmt"
	"image"
	"image/color"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/talkback"
)

// talkLive is the Talk control while the microphones are going to a camera: red, as a recording
// light is, since the room is being heard outside it.
var talkLive = color.RGBA{0xb0, 0x30, 0x28, 0xff}

// cameraControl draws one of the camera pages' bar controls in b: the program's button, its words in
// the sheet's button face and centered in the box the caller measured from them. live fills it in
// talkLive, the red that says the room is being heard outside it; the rest take the quiet button.
func (r *paint) cameraControl(b image.Rectangle, label string, live bool) {
	if !live {
		r.labelButton(b, label, btnSecondary)
		return
	}
	r.roundButton(b, float64(b.Dy())/2, talkLive)
	r.buttonLabel(b, label, cream)
	r.marked = true
}

// talkLabel is what the camera page's Talk control says for the view of entity: what a tap does, and
// while a talk runs, how long it has left.
func talkLabel(st talkback.State, entity string) string {
	if st.Entity != entity {
		return "Talk"
	}
	switch st.Phase {
	case talkback.Opening:
		return "Connecting…"
	case talkback.Talking:
		left := st.Left.Round(time.Second)
		return fmt.Sprintf("End talk  %d:%02d", int(left.Minutes()), int(left.Seconds())%60)
	}
	if st.Error != "" {
		return "Try again"
	}
	return "Talk"
}
