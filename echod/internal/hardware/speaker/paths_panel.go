//go:build panel

package speaker

import (
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/asp"
	"github.com/HuskerMinion/techo5/echod/internal/lib/panelctl"
)

// TECHO5 Panel: the app plays what the daemon writes through an Android AudioTrack. There is no codec
// to set up, no jack to watch and no amplifier to switch: Android routes the sound. The volume is
// Android's media volume, which the app sets from the daemon's step (volumeChanged), so the tablet's
// own buttons and slider and Home Assistant all move the one level; mute, the lowest steps and a ring
// over quiet music are worked out in software on top of it (gainForStep, followRing).

// Output is the device's audio output. A panel has the speaker only; Android moves the sound to
// headphones or Bluetooth on its own.
type Output string

const (
	OutputSpeaker   Output = "speaker"
	OutputHeadphone Output = "headphone"
	OutputBoth      Output = "both"
)

// The write loop's period, 20 ms, and how many the app's buffer is sized against.
const (
	period  = Rate / 50
	periods = 4
)

// DRAMHold is an Echo Show workaround; nothing here.
const DRAMHold = ""

// A mixer write, as the shared player applies it. A panel has no mixer, so none are made.
type kctl struct {
	name      string
	value     string
	level     int32
	blob      []byte
	ifPresent bool
}

var (
	initSequence []kctl
	pathSequence = map[Output][]kctl{}
	headphoneOff []kctl
)

const jackPoll = time.Second

// DetectOutput is always the speaker: Android routes it.
func DetectOutput() Output { return OutputSpeaker }

// VolumeSteps is the number of volume steps, config's range.
const VolumeSteps = config.VolumeSteps

// mute is the attenuation for step 0.
const mute = -90

// The levels a panel plays at. mediaStep is the media volume, as Home Assistant and the tablet's
// buttons set it. ringStep is what a ring or a chime sounding now asks for, 0 when nothing does.
// deviceStep is what Android's media volume is set to, the louder of the two and never below
// deviceFloor, and everything is scaled against it in software (gainForStep). So mute and the lowest
// steps are kept here, as on an Echo, and a ring is heard at its own step however low the music is:
// Android's volume is the ceiling of everything played, so it is raised for the ring and the music
// under it is turned down here by as much.
var mediaStep, ringStep, deviceStep atomic.Int32

// deviceFloor is the lowest step Android is ever sent: muted music is silenced here, over a speaker
// that is still on, so a ring that comes up on a muted tablet is not held at zero. The app sets
// Android no lower than its first index for it.
const deviceFloor = 1

func init() {
	mediaStep.Store(VolumeSteps)
	deviceStep.Store(VolumeSteps)
}

// deviceFor is the step Android's volume goes to for the levels now.
func deviceFor() int {
	return max(int(mediaStep.Load()), int(ringStep.Load()), deviceFloor)
}

// gainForStep is the software gain on top of Android's media volume (deviceStep), on the same curve
// a Bluetooth sink uses (-40 dB at the first step, unity at the top): the music at the media step, a
// ring or a bell at its own, each scaled against the level Android is at. Android's volume is the
// tablet's own, so its buttons and slider and Home Assistant all move one level, and with no ring
// sounding and the music above deviceFloor the media step plays at unity.
func gainForStep(_ Output, step int) float32 {
	if step <= 0 {
		return 0
	}
	d := int(deviceStep.Load())
	if d <= 0 {
		return curve(step)
	}
	return min(curve(step)/curve(d), 1)
}

func curve(step int) float32 {
	step = max(0, min(step, VolumeSteps))
	return float32(math.Pow(10, (float64(step)/VolumeSteps-1)*40/20))
}

// androidLag is how long a sample takes from the write loop to the room: the socket's buffer
// (sendBuffer, 160 ms) and the app's AudioTrack (200 ms). A level change that comes with a ring is
// sent to Android that much after the samples scaled for it, so it lands with them; on the long side,
// so the music under a ring that starts is turned down a moment early rather than played out loud.
var androidLag = 360 * time.Millisecond

// sendToApp is the control line, set apart so a test can read what Android is told.
var sendToApp = panelctl.Send

// The level changes for Android in the order they were made: a late one is not sent over a newer one.
// toldStep is the last step sent, the level Android is at as far as the daemon knows, and toldAt when
// it was sent.
var (
	levelMu      sync.Mutex
	levelSeq     uint64
	levelSentSeq uint64
	toldStep     = VolumeSteps
	toldAt       time.Time
)

// sendMu keeps the level lines to the app in the order they are sent. It is held over the write and
// nothing else is, so an app that stops reading holds up only the next level's send, never the write
// loop or a volume change.
var sendMu sync.Mutex

// stepMu keeps deviceStep, and the order its changes are sent to Android in, to one writer at a time:
// a ring starting or ending works it out from mediaStep as the media step moves.
var stepMu sync.Mutex

// tellAndroid sends the step to the app after wait, unless a newer level has been sent by then. It
// never waits on the app: even with no wait the line goes out on a goroutine of its own, as it is
// called with stepMu held, by the write loop among others.
func tellAndroid(step int, wait time.Duration) {
	levelMu.Lock()
	levelSeq++
	seq := levelSeq
	levelMu.Unlock()
	send := func() {
		sendMu.Lock()
		defer sendMu.Unlock()
		levelMu.Lock()
		if seq <= levelSentSeq {
			levelMu.Unlock()
			return
		}
		levelSentSeq, toldStep, toldAt = seq, step, time.Now()
		levelMu.Unlock()
		sendToApp(fmt.Sprintf("volume %d %d", step, VolumeSteps))
	}
	time.AfterFunc(max(wait, 0), send)
}

// volumeChanged is the media step moving (Home Assistant, the buttons, mute): Android's level
// follows at once, as the tablet's slider shows it.
func volumeChanged(step int) {
	stepMu.Lock()
	defer stepMu.Unlock()
	mediaStep.Store(int32(step))
	d := deviceFor()
	deviceStep.Store(int32(d))
	tellAndroid(d, 0)
}

// followRing is the write loop's word, every period, on whether a ring or a chime is sounding and at
// what step. When that moves Android's level, the music's gain is worked out again against the new
// one from this period on. A raise is sent androidLag later, when these samples are heard; a drop at
// once, though never on the loop itself (tellAndroid), so the samples still on their way, scaled for
// the louder level, come out quieter for a moment rather than the music coming back at its own gain
// over a level still raised for the ring.
func (p *Player) followRing(on bool, step int) {
	need := 0
	if on {
		need = max(0, min(step, VolumeSteps))
	}
	if int(ringStep.Swap(int32(need))) == need {
		return
	}
	stepMu.Lock()
	defer stepMu.Unlock()
	d := deviceFor()
	was := int(deviceStep.Swap(int32(d)))
	if was == d {
		return
	}
	if p.sink.Load() == nil {
		p.volume.Store(math.Float32bits(gainForStep(p.Output(), int(p.step.Load()))))
	}
	wait := androidLag
	if d < was {
		wait = 0
	}
	tellAndroid(d, wait)
}

// AndroidLevel is the step Android was last told, when it was sent, and whether a ring raised it
// above the music. A level Android reports while raised is the ring's level moved by a press of the
// tablet's buttons, so the press is applied to the music by as much as it moved Android; one reported
// just after a new level was sent may be the app's word on the level before, and is not taken
// (media/volume_panel.go).
func AndroidLevel() (told int, at time.Time, raised bool) {
	stepMu.Lock()
	defer stepMu.Unlock()
	levelMu.Lock()
	told, at = toldStep, toldAt
	levelMu.Unlock()
	return told, at, told > max(int(mediaStep.Load()), deviceFloor)
}

// MediaService is empty: nothing has to be stopped to play.
const MediaService = ""

const AmpSwitch = ""

const OutputBoost = 1.0

// DriverTuning is off: the vendor tuning is an Echo's.
const DriverTuning = false

var (
	HasJack = false
	HasBoth = false
)

// firstCurves is empty on a panel: Android plays the sound, with no Amazon tuning to put the volume
// in front of (paths_cronos.go).
var firstCurves = map[string][VolumeSteps + 1]float64{}

// MuteSwitch is a mute on the amplifier besides AmpSwitch: none, Android owns the amplifier.
const MuteSwitch = ""

// idleGate is none: Android owns the amplifier (idle.go).
var idleGate *ampGate

// mayHaveTAS5805M is false: there is no amplifier of the daemon's own to find (GainAvailable), so
// the speaker gain control never shows on a panel.
const mayHaveTAS5805M = false

// tuningSet is nil: there is no vendor tuning to load (DriverTuning).
var tuningSet *asp.Set

// softRef is off: a panel keeps its own reference of what it sent the app (playback_panel.go,
// lib/loopback), which the microphone's canceller reads.
const softRef = false

// hwRate is Rate and nothing is trimmed after the volume: the app takes the stream as it comes and
// Android resamples it for the hardware.
const (
	hwRate   = Rate
	hwTrimDB = 0.0
	hwBits   = Bits
)
