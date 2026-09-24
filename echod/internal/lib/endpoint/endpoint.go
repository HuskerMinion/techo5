// Package endpoint decides when the person talking to the device has finished, from how loud the audio
// being sent is.
//
// The pipeline in Home Assistant already decides this with a voice activity detector, and in a quiet
// room it decides well. With a television or someone else talking in the room it does not: a detector
// hears speech, and the room is full of it, so the turn never ends, runs to the device's listening
// limit, and is thrown away - recordings of real turns show the request in the first second or two and
// fourteen seconds of television after it. What tells the two apart is distance. The person asking is
// near the device and the room is not, so after the leveler has had its say the speaker stands several
// times louder than the room around them: in those recordings about 1500 against 300 to 700.
//
// So the turn is over once the audio has fallen back well below the speaker's own level and stayed
// there: relative to them, not to the room, which is the one level here that cannot be known in advance.
package endpoint

import "math"

// Window is how much audio one decision looks at: a tenth of a second at the microphone's rate.
const Window = 1600

// Settings are the thresholds, as fractions of the loudest window heard since the turn began.
type Settings struct {
	// On is how loud a window has to be to count as the speaker talking.
	On float64
	// Off is how far the level has to fall before it counts as them having stopped.
	Off float64
	// Quiet is how many windows in a row below Off end the turn: long enough to span a pause between
	// words, short enough that the room's own noise is not what the turn ends on.
	Quiet int
	// Speech is how many windows at or above On have to have been heard before anything can end:
	// a cough, a door, or the tail of the wake word is not a request.
	Speech int
}

// Default is what the recordings of real turns were measured at.
var Default = Settings{On: 0.5, Off: 0.3, Quiet: 6, Speech: 3}

// Detector follows one turn's audio. Feed it what is sent, in any size of frame.
type Detector struct {
	s Settings

	sum     float64 // squares in the window being filled
	n       int     // samples in it
	windows int     // windows seen

	peak   float64 // loudest window so far
	speech int     // windows counted as the speaker
	quiet  int     // windows in a row below Off since the last speech

	ended int // the window the turn ended at, or 0
}

// New starts a turn.
func New(s Settings) *Detector { return &Detector{s: s} }

// Feed takes samples and reports whether the speaker has finished. Once it has said so it goes on
// saying so.
func (d *Detector) Feed(samples []int16) bool {
	for _, v := range samples {
		if d.ended > 0 {
			return true
		}
		d.sum += float64(v) * float64(v)
		d.n++
		if d.n == Window {
			d.window(math.Sqrt(d.sum / Window))
			d.sum, d.n = 0, 0
		}
	}
	return d.ended > 0
}

func (d *Detector) window(rms float64) {
	d.windows++
	d.peak = max(d.peak, rms)
	switch {
	case rms >= d.s.On*d.peak:
		d.speech++
		d.quiet = 0
	case rms < d.s.Off*d.peak:
		d.quiet++
	}
	if d.speech >= d.s.Speech && d.quiet >= d.s.Quiet {
		d.ended = d.windows
	}
}

// EndedAt is how far into the turn the speaker finished, in samples, or 0 if they have not: the end of
// the last window they were heard in, which is where anything cut from this should stop.
func (d *Detector) EndedAt() int {
	if d.ended == 0 {
		return 0
	}
	return (d.ended - d.s.Quiet) * Window
}
