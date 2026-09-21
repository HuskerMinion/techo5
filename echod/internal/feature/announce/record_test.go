package announce

import (
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/mic"
)

// What trim has to get right is the difference between somebody who said nothing and somebody who
// said something and then stopped: the first is a press of a button and a walk away, and sending it
// would put a chime and a silence in every room.
func TestSilenceIsNotAnAnnouncement(t *testing.T) {
	quiet := make([]int16, mic.Rate*3) // three seconds of nothing
	if got := trim(quiet); got != nil {
		t.Errorf("three seconds of silence came back as %d samples", len(got))
	}

	// Too short to be anybody speaking, even with sound in it.
	brief := make([]int16, mic.Rate/5)
	for i := range brief {
		brief[i] = 5000
	}
	if got := trim(brief); got != nil {
		t.Errorf("a fifth of a second came back as %d samples", len(got))
	}
}

// And the trailing silence goes, since it would be played in every room.
func TestTheTailIsTrimmed(t *testing.T) {
	said := make([]int16, mic.Rate*4)
	for i := 0; i < mic.Rate*2; i++ {
		said[i] = 8000 // two seconds of talking, then two of nothing
	}

	got := trim(said)
	if len(got) == 0 {
		t.Fatal("two seconds of speech came back as nothing")
	}
	if len(got) > mic.Rate*5/2 {
		t.Errorf("kept %.1f s, which is most of the silence after it", float64(len(got))/mic.Rate)
	}
	if len(got) < mic.Rate*2 {
		t.Errorf("kept %.1f s, which is less than what was said", float64(len(got))/mic.Rate)
	}
}

// The floor has to sit between a quiet room and somebody talking in it.
func TestWhatCountsAsSound(t *testing.T) {
	room := make([]int16, 160)
	for i := range room {
		room[i] = int16(i % 40) // the microphone's own noise, nowhere near the floor
	}
	if loud(room) {
		t.Error("an empty room counted as somebody talking")
	}

	talking := make([]int16, 160)
	for i := range talking {
		talking[i] = int16(2000 - i*10)
	}
	if !loud(talking) {
		t.Error("somebody talking counted as an empty room")
	}
}
