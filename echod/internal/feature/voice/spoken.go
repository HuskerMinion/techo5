package voice

import (
	"sync/atomic"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// spokenPace is how many characters of a reply a voice says in a second, near enough: what a reply's
// length is taken to be until its audio has all arrived, and the whole of it for one with no audio here.
const spokenPace = 15.0

// replyClock is the reply being said, for Spoken: when its words arrived, and once its audio plays here,
// when that began, how long the audio that has come so far is, and whether that is all of it.
type replyClock struct {
	text    time.Time
	at      time.Time
	seconds float64
	whole   bool
}

var spoken atomic.Pointer[replyClock]

// replyArrived starts the clock for a reply's words.
func replyArrived(now time.Time) {
	spoken.Store(&replyClock{text: now})
}

// replyPlaying says the reply's audio is playing since at, seconds of it so far, and whole once that is
// all of it. It runs on the errand that plays the reply.
func replyPlaying(at time.Time, seconds float64, whole bool) {
	c := spoken.Load()
	if c == nil {
		c = &replyClock{text: at}
	}
	spoken.Store(&replyClock{text: c.text, at: at, seconds: seconds, whole: whole})
}

// replyAudioSeconds is how long bytes of reply audio play for.
func replyAudioSeconds(bytes int) float64 {
	return float64(bytes) / float64(2*speaker.VoiceRate)
}

// Spoken is how much of text has been said by now, from 0 to 1, for a screen that turns the pages of a
// reply too long for it (#333). While the reply's audio plays here it goes by that audio: its length is
// what has arrived, or what the words would take at spokenPace when that is longer, until all of it has.
// A reply with no audio here, played from an address or not spoken at all, goes at spokenPace from when
// its words arrived.
func Spoken(text string, now time.Time) float64 {
	c := spoken.Load()
	if c == nil || text == "" {
		return 0
	}
	paced := float64(len([]rune(text))) / spokenPace
	start, length := c.text, paced
	if !c.at.IsZero() {
		start, length = c.at, c.seconds
		if !c.whole {
			length = max(length, paced)
		}
	}
	if length <= 0 {
		return 1
	}
	return min(max(now.Sub(start).Seconds()/length, 0), 1)
}
