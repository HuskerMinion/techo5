package announce

import (
	"context"
	"log/slog"
	"math"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/mic"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// Recording what somebody says, to send to the other rooms.
//
// An announcement is a voice, not a sentence: the device records the person standing in front of it
// and pushes the audio, so a Dot in a hallway carries it as well as a Show does, and nothing has to
// be typed or understood or sent anywhere to be turned into speech. The plan said so from the start
// — "record the clip on the device that is speaking, push it to each of the others".
//
// It is bounded at both ends. A tone says when to start, because the device cannot say it; the
// recording stops when somebody stops talking, and gives up at the ceiling if they do not.

const (
	// longest is the ceiling: an announcement is a sentence or two, and anything longer is a mistake
	// nobody wants played in every room.
	longest = 15 * time.Second

	// hush is how much silence ends it. Long enough for the pause in the middle of a sentence, short
	// enough that nobody stands there wondering whether it is still listening.
	hush = 1500 * time.Millisecond

	// leadIn is dropped from the front: the prompt tone is still leaving the speaker when the
	// microphone opens, and the array hears it better than it hears the talker.
	leadIn = 250 * time.Millisecond

	// quietFloor is the level below which a frame counts as silence. The microphone's own noise
	// floor sits well under this; a voice across a room sits well over it.
	quietFloor = 600
)

// prompt is the two notes that say the device is listening, and confirm is the one that says it has
// stopped. They are not speech, so they need no language and no clips.
var (
	prompt  = []speaker.Note{{Freq: 587, Ms: 90}, {Freq: 880, Ms: 140}}
	confirm = []speaker.Note{{Freq: 880, Ms: 90}}
)

// record plays the prompt, takes what is said, and hands it back at the microphone's rate. It
// returns nothing when there was nothing to hear, which is somebody pressing the button and walking
// away, and is not an announcement.
//
// It ends on whichever comes first: somebody stopping talking, the ceiling, a finish (the screen
// saying that is the end of it) or ctx (the screen throwing it away). Finishing keeps what was
// said; cancelling does not, which is the difference between changing your mind and being done.
func record(ctx context.Context, finish <-chan struct{}) []int16 {
	speaker.Sound().Interject(func(p *speaker.Player) { p.Chime(promptLevel, prompt...) })

	frames, stop := mic.Get().Listen("announce")
	defer stop()

	started := time.Now()
	deadline := time.NewTimer(longest)
	defer deadline.Stop()

	var said []int16
	var lastSound time.Time
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-finish:
			return trim(said)
		case <-deadline.C:
			slog.Info("announcement recording reached the ceiling", "seconds", longest.Seconds())
			return trim(said)
		case f, ok := <-frames:
			if !ok {
				return trim(said)
			}
			if time.Since(started) < leadIn {
				continue
			}
			said = append(said, f...)

			if loud(f) {
				lastSound = time.Now()
				continue
			}
			// Silence only ends it once something has been said: a device that gave up before anybody
			// started would be a device you have to talk over.
			if !lastSound.IsZero() && time.Since(lastSound) > hush {
				return trim(said)
			}
		}
	}
}

// promptLevel is how loud the prompt is: enough to be heard by whoever asked for it, not enough to
// carry into the next room at night.
const promptLevel = 0.35

// loud reports whether a frame has anything in it worth keeping.
func loud(frame []int16) bool {
	for _, s := range frame {
		if s > quietFloor || s < -quietFloor {
			return true
		}
	}
	return false
}

// trim takes the trailing silence off, since it would be played in every room, and reports nothing
// at all when what is left is too short to be anybody speaking.
func trim(said []int16) []int16 {
	const shortest = mic.Rate / 2 // half a second

	end := len(said)
	for end > 0 && !loud(said[max(0, end-mic.Rate/20):end]) {
		end -= mic.Rate / 20
	}
	if end < shortest {
		return nil
	}
	return said[:end]
}

// How a recording is levelled before it is sent.
//
// The live gain in front of the microphone is set for what speech recognition needs, not for
// playing back in another room: a clip can leave here peaking forty decibels below full scale, which
// is perfectly good for a recognizer and far too quiet to hear over a kitchen. It adapts slowly too,
// so the first seconds of a short recording are the quietest part of it.
//
// None of that matters once the whole clip is in hand. The peak and the loudness are exactly
// measurable, so the gain is worked out in one go rather than estimated as it goes.
const (
	// wantRMS is how loud the result should be on average. The average is what carries across a
	// room: a clip can sit at the very top of the scale on one transient and still be too quiet to
	// hear, which is exactly what the first attempt at this produced.
	//
	// It is set near ordinary programme material, so an announcement is about as loud as the music it
	// interrupts rather than noticeably under it.
	wantRMS = -16.0

	// knee is where peaks start being folded rather than passed, as a fraction of full scale. Speech
	// peaks twelve to eighteen decibels above its own average, so letting the loudest sample decide
	// the gain throws away everything the average needed; instead the gain serves the average and the
	// few samples that then overshoot are rounded off here. Below the knee nothing is touched at all,
	// which is the great majority of every clip.
	knee = 0.7

	// mostLift bounds it. A recording of an empty room is mostly noise, and enough gain would make
	// that noise sound like a fault; anything needing more than this was too quiet to save.
	mostLift = 30.0
)

// level brings a recording up to something that can be heard in another room.
func level(said []int16) []int16 {
	if len(said) == 0 {
		return said
	}

	var peak float64
	var sum float64
	for _, s := range said {
		v := float64(s)
		if a := math.Abs(v); a > peak {
			peak = a
		}
		sum += v * v
	}
	if peak == 0 {
		return said
	}
	rms := math.Sqrt(sum / float64(len(said)))

	const full = 32767.0
	gain := math.Pow(10, mostLift/20)
	if rms > 0 {
		gain = math.Min(gain, full*math.Pow(10, wantRMS/20)/rms)
	}
	// Only a clip that arrived too hot to play is turned down, and then by the peak.
	if peak*gain > full {
		if down := full / peak; down < 1 {
			gain = math.Min(gain, down)
		}
	}

	if gain >= 0.999 && gain <= 1.001 {
		return said
	}
	slog.Info("announcement levelled",
		"gain_db", math.Round(20*math.Log10(gain)*10)/10,
		"was_peak_dbfs", math.Round(20*math.Log10(peak/full)*10)/10,
		"was_rms_dbfs", math.Round(20*math.Log10(math.Max(rms, 1)/full)*10)/10)

	for i, s := range said {
		said[i] = fold(float64(s) * gain)
	}
	return said
}

// fold applies the gain's result to one sample, rounding off anything above the knee instead of
// letting it clip. Below the knee it is arithmetic and nothing else; above it, the remaining
// headroom is spread over everything louder, so a plosive that would have been a click becomes a
// plosive. Nothing can reach the rail, so nothing wraps.
func fold(v float64) int16 {
	const full = 32767.0
	const t = knee * full

	a := math.Abs(v)
	if a <= t {
		return int16(v)
	}
	over := math.Tanh((a - t) / (full - t))
	out := t + (full-t)*over
	if v < 0 {
		return int16(-out)
	}
	return int16(out)
}
