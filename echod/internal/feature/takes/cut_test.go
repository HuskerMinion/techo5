package takes

import (
	"strings"
	"testing"
	"time"
)

// levels builds a recording's frame levels: the room at floor, with a little movement in it, and each
// word laid over it from its frame for its length.
type levels struct {
	level []float32
	over  []bool
}

func roomOf(n int, floor float32) *levels {
	l := &levels{level: make([]float32, n), over: make([]bool, n)}
	for i := range l.level {
		l.level[i] = floor * (1 + 0.2*float32(i%5)/4)
	}
	return l
}

func (l *levels) word(at, length int, loud float32) *levels {
	for i := at; i < at+length && i < len(l.level); i++ {
		l.level[i] = loud
	}
	return l
}

func (l *levels) floor(from, to int, floor float32) *levels {
	for i := from; i < to; i++ {
		l.level[i] = floor * (1 + 0.2*float32(i%5)/4)
	}
	return l
}

func f2(d time.Duration) int { return int(d / frameTime) }

// A word said in a quiet room is one take, with the room either side of it.
func TestATakeIsCutWithTheRoomEitherSide(t *testing.T) {
	l := roomOf(400, 30).word(200, 30, 3000)
	takes, left := cut(l.level, l.over)
	if len(takes) != 1 || len(left) != 0 {
		t.Fatalf("cut into %v, left out %v", takes, left)
	}
	// The word and the padding either side, give or take the frame the levels are smoothed over.
	from, to := 200-f2(pad), 230+f2(pad)
	if got := takes[0]; got.from > from || got.from < from-2 || got.to < to || got.to > to+2 {
		t.Errorf("the take is %v, want about %v", got, span{from, to})
	}
}

// The dip between the words of "turn it up" does not split it.
func TestTwoWordsWithAPauseAreOneTake(t *testing.T) {
	l := roomOf(400, 30).word(200, 15, 3000).word(223, 15, 3000) // 160 ms between
	if takes, left := cut(l.level, l.over); len(takes) != 1 || len(left) != 0 {
		t.Errorf("cut into %v, left out %v, want one take", takes, left)
	}
}

// A click is too short to be a word, and talking on is too long to be one.
func TestAClickAndTalkingOnAreNotTakes(t *testing.T) {
	l := roomOf(800, 30).word(200, 4, 3000).word(400, f2(4*time.Second), 3000)
	takes, left := cut(l.level, l.over)
	if len(takes) != 0 || len(left) != 2 || left[0].why != whyShort || left[1].why != whyLong {
		t.Errorf("cut into %v, left out %v", takes, left)
	}
}

// A room nobody spoke in is cut into nothing, however its level moves.
func TestNothingIsCutFromAQuietRoom(t *testing.T) {
	l := roomOf(3000, 30)
	for i := range l.level {
		l.level[i] *= 1 + float32(i%37)/37 // up to twice the floor: 6 dB, under standOut
	}
	if takes, left := cut(l.level, l.over); len(takes) != 0 || len(left) != 0 {
		t.Errorf("a quiet room was cut into %v, left out %v", takes, left)
	}
	if takes, left := cut(nil, nil); takes != nil || left != nil {
		t.Error("an empty recording was cut into something")
	}
}

// A loud room is judged against itself: the words stand out from it as they would from a quiet one.
func TestALoudRoomIsCutRelativeToItself(t *testing.T) {
	l := roomOf(1000, 900)
	for k := range 5 {
		l.word(100+150*k, 30, 9000)
	}
	if takes, left := cut(l.level, l.over); len(takes) != 5 || len(left) != 0 {
		t.Errorf("cut into %d takes, left out %v, want 5", len(takes), left)
	}
}

// Takes said far away and near, one after another, with the leveler raising the room between them, are
// still one take each and never run together into one too long for a word.
func TestNearAndFarTakesAreNotMerged(t *testing.T) {
	l := roomOf(2000, 30)
	for k := range 10 {
		at := 100 + 180*k
		loud, floor := float32(3000), float32(30)
		if k%2 == 1 {
			loud, floor = 2000, 400 // far: the leveler turned up, and the room with it
		}
		l.floor(at-80, at+100, floor).word(at, 30, loud)
	}
	takes, left := cut(l.level, l.over)
	if len(takes) != 10 || len(left) != 0 {
		t.Errorf("cut into %d takes, left out %v, want 10", len(takes), left)
	}
}

// Three takes in two minutes are found: the loudest of a recording that is mostly room still stands out.
func TestFewTakesInALongRecordingAreFound(t *testing.T) {
	l := roomOf(f2(Longest), 30).word(500, 30, 3000).word(3000, 30, 3000).word(5500, 30, 3000)
	if takes, _ := cut(l.level, l.over); len(takes) != 3 {
		t.Errorf("found %d takes, want 3", len(takes))
	}
}

// A take with the device's own sound near it is left out: it is the device, or the word over it.
func TestATakeSaidOverTheSpeakerIsLeftOut(t *testing.T) {
	l := roomOf(600, 30).word(100, 30, 3000).word(300, 30, 3000)
	l.over[300+30+f2(pad)+f2(widen)/2] = true // just past the second take's padding
	takes, left := cut(l.level, l.over)
	if len(takes) != 1 || len(left) != 1 || left[0].why != whySpeaker {
		t.Errorf("cut into %v, left out %v", takes, left)
	}
}

// Two takes close together share the room between them, each padded only halfway to the other.
func TestPaddingNeverReachesTheNextTake(t *testing.T) {
	l := roomOf(400, 30).word(100, 30, 3000).word(150, 30, 3000) // 400 ms apart
	takes, _ := cut(l.level, l.over)
	if len(takes) != 2 {
		t.Fatalf("cut into %v", takes)
	}
	if takes[0].to > takes[1].from {
		t.Errorf("the takes overlap: %v", takes)
	}
	if takes[0].to < 130 || takes[1].from > 150 {
		t.Errorf("a take was cut into: %v", takes)
	}
}

// A word still being said when the recording ended is not a whole take.
func TestAWordCutOffByTheEndIsLeftOut(t *testing.T) {
	l := roomOf(400, 30).word(100, 30, 3000).word(385, 15, 3000)
	takes, left := cut(l.level, l.over)
	if len(takes) != 1 || len(left) != 1 || left[0].why != whyCutOff {
		t.Errorf("cut into %v, left out %v", takes, left)
	}
}

// Sixty takes at the most; the rest are said to be left out, not dropped without a word.
func TestAtMostSixtyTakes(t *testing.T) {
	l := roomOf(70*60+100, 30)
	for k := range 70 {
		l.word(50+60*k, 20, 3000)
	}
	takes, left := cut(l.level, l.over)
	if len(takes) != mostTakes || len(left) != 10 || !strings.Contains(left[0].why, "60") {
		t.Errorf("cut into %d takes, left out %d (%v)", len(takes), len(left), left[:min(len(left), 1)])
	}
}

// Somebody talking on, syllables and the dips between them, is too long for a word: it is not chopped
// into takes at the dips, as the room stepped up after a far take is.
func TestTalkingOnIsNotChoppedIntoTakes(t *testing.T) {
	l := roomOf(600, 30)
	for at := 200; at < 400; at += 30 { // 4 s of 300 ms syllables and 300 ms dips
		l.word(at, 15, 3000).floor(at+15, at+30, 400)
	}
	takes, left := cut(l.level, l.over)
	if len(takes) != 0 || len(left) != 1 || left[0].why != whyLong {
		t.Errorf("talking on was cut into %d takes, left out %v", len(takes), left)
	}
}

// Talking in phrases, with pauses as long as the ones between takes, is not takes either: a room
// stepped up is mostly room, and this is mostly talk.
func TestTalkingInPhrasesIsNotTakes(t *testing.T) {
	l := roomOf(800, 30)
	for k := range 3 {
		at := 200 + 90*k
		l.word(at, 60, 3000) // 1.2 s of talk
		if k < 2 {
			l.floor(at+60, at+90, 400) // 600 ms, the room raised under it
		}
	}
	if takes, left := cut(l.level, l.over); len(takes) != 0 || len(left) != 1 || left[0].why != whyLong {
		t.Errorf("talking in phrases was cut into %d takes, left out %v", len(takes), left)
	}
}

// A word already being said when the recording's first frame was kept began in the lead-in that was
// dropped: it is missing its start, and is left out as cut off.
func TestAWordCutOffByTheStartIsLeftOut(t *testing.T) {
	l := roomOf(400, 30).word(0, 20, 3000).word(200, 30, 3000)
	takes, left := cut(l.level, l.over)
	if len(takes) != 1 || len(left) != 1 || left[0].why != whyCutIn || left[0].from != 0 {
		t.Errorf("cut into %v, left out %v", takes, left)
	}
	// Begun a moment after the first frame kept, with the room before it: it may still be the second half
	// of a word said into the lead-in.
	l = roomOf(400, 30).word(6, 20, 3000).word(200, 30, 3000)
	if takes, left := cut(l.level, l.over); len(takes) != 1 || len(left) != 1 || left[0].why != whyCutIn {
		t.Errorf("a word begun just after the lead-in was cut into %v, left out %v", takes, left)
	}
}

// What the device played, loud as it was, does not raise the bar for the words: far takes said after a
// loud reply are still takes.
func TestTheDevicesOwnSoundDoesNotHideFarTakes(t *testing.T) {
	l := roomOf(1000, 30).word(100, 60, 30000)
	// Marked over only up to six frames before its end: the mark is read as each frame is taken, and the
	// speaker sounds on after its queue has run out.
	for i := 100; i < 154; i++ {
		l.over[i] = true
	}
	for k := range 4 {
		l.word(400+120*k, 25, 300)
	}
	if takes, _ := cut(l.level, l.over); len(takes) != 4 {
		t.Errorf("found %d far takes after a loud reply, want 4", len(takes))
	}
}

// A long phrase said far away, with the room raised around it for a little longer than a word, is one
// take: what stands out of the stretch is no more than a word's length.
func TestALongPhraseSaidFarAwayIsATake(t *testing.T) {
	l := roomOf(600, 30).floor(200, 340, 400).word(200, 75, 2000) // 1.5 s, in 2.8 s of raised room
	if takes, left := cut(l.level, l.over); len(takes) != 1 || len(left) != 0 {
		t.Errorf("a long far phrase was cut into %v, left out %v", takes, left)
	}
}
