//go:build spot

package speaker

import "testing"

// The Spot's jack is its line-out: the headphone path turns it on, the speaker path and headphoneOff off.
func TestTheSpotJackOpensTheLineOut(t *testing.T) {
	lineOut := func(seq []kctl) string {
		v := ""
		for _, k := range seq {
			if k.name == "Audio_LineOut_Setting" {
				v = k.value
			}
		}
		return v
	}
	if got := lineOut(pathSequence[OutputHeadphone]); got != "On" {
		t.Errorf("headphone path: line-out %q, want On", got)
	}
	if got := lineOut(pathSequence[OutputSpeaker]); got != "Off" {
		t.Errorf("speaker path: line-out %q, want Off", got)
	}
	if got := lineOut(headphoneOff); got != "Off" {
		t.Errorf("headphoneOff: line-out %q, want Off", got)
	}
}
