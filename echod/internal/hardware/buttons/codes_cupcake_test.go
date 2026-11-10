//go:build dot

package buttons

import "testing"

// The Echo Input has the action and mute buttons only; the Dots keep their four.
func TestTheEchoInputHasNoVolumeKeys(t *testing.T) {
	in := boardCodes(true)
	if len(in) != 2 || in[138] != Action || in[113] != Mute {
		t.Errorf("Echo Input codes %v, want action (138) and mute (113)", in)
	}
	dot := boardCodes(false)
	if len(dot) != 4 || dot[114] != VolumeDown || dot[115] != VolumeUp {
		t.Errorf("Dot codes %v, want the four buttons", dot)
	}
}
