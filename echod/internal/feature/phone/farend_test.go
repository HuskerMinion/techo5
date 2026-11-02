package phone

import (
	"math"
	"testing"
)

// A room's steady noise over the fixed level stops counting as the far end talking once its floor is
// learned; a voice over that noise still counts, and a quiet line is never talking (#392).
func TestFarEndTalkingIsOverItsNoise(t *testing.T) {
	db := func(d float64) float64 { return farEndLoud * math.Pow(10, d/10) }
	var f farEnd
	room := db(6) // 6 dB over the fixed level: a Dot's room in the report
	talked := 0
	for range 50 {
		if f.talking(room) {
			talked++
		}
	}
	if talked > 0 {
		t.Errorf("a room's steady noise counted as talking in %d of 50 frames", talked)
	}
	if !f.talking(db(20)) {
		t.Error("a voice 14 dB over the room's noise did not count as talking")
	}
	var quiet farEnd
	for range 10 {
		if quiet.talking(db(-10)) {
			t.Fatal("a quiet line counted as talking")
		}
	}
	if !quiet.talking(db(3)) {
		t.Error("a voice over the fixed level, on a quiet line, did not count as talking")
	}
	var silent farEnd
	silent.talking(0)
	if !silent.talking(db(3)) {
		t.Error("a voice after digital silence did not count as talking")
	}
}
