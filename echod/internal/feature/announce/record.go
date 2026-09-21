package announce

import (
	"context"
	"log/slog"
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
