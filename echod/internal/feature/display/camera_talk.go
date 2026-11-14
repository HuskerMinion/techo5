//go:build !dot

package display

import (
	"fmt"
	"image"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/talkback"
)

// cameraControl draws one of the camera pages' bar controls in b: the program's button, its words in
// the sheet's button face and centered in the box the caller measured from them. live fills it in
// declineRed, the red that says the room is being heard outside it and that a tap ends that; the rest
// take the quiet button.
func (r *paint) cameraControl(b image.Rectangle, label string, live bool) {
	if !live {
		r.labelButton(b, label, btnSecondary)
		return
	}
	r.roundButton(b, float64(b.Dy())/2, r.pressFill(b, declineRed, cream))
	r.buttonLabel(b, label, cream)
	r.marked = true
}

// talkLabel is what the camera page's Talk control says for the view of entity: what a tap does, and
// while a talk runs, how long it has left.
func talkLabel(st talkback.State, entity string) string {
	if st.Entity != entity {
		return tr("drawer_talk")
	}
	switch st.Phase {
	case talkback.Opening:
		return tr("drawer_connecting")
	case talkback.Talking:
		left := st.Left.Round(time.Second)
		return fmt.Sprintf(tr("drawer_end_talk"), int(left.Minutes()), int(left.Seconds())%60)
	}
	if st.Error != "" {
		return tr("drawer_try_again")
	}
	return tr("drawer_talk")
}
