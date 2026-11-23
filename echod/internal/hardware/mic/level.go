package mic

import (
	"log/slog"
	"math"
	"sync/atomic"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Leveling the mix. Nothing upstream applies gain, so speech reaches recognition 20 dB or more
// below what the models were trained on, and no fixed hardware gain suits both a talker across the
// room and one leaning over the device.
//
// The shape is the vendor's own full band AGC: a target, a ceiling on the gain, quick down and slow
// up, following only audio that stands above the room.
const (
	targetDBFS = -23.0
	maxGainDB  = 27.0

	// How far above the quietest thing recently heard a frame has to be before it is taken for
	// someone talking. Relative, because an absolute threshold cannot be right: the room is unknown
	// and the analog gain in front of this is a setting, so any fixed level is below the noise in one
	// house and above the speech in another. What is fixed is how far speech stands out from a room,
	// which is a property of speech.
	speechOverFloorDB = 12.0

	// The floor drops to the quietest audio quickly, since a pause between words is the best look at
	// the room anyone gets, and climbs slowly enough that talking without pausing cannot convince it
	// the room got louder. Slower still and a room that genuinely turns noisy would never be noticed.
	floorFall = 200 * time.Millisecond
	floorRise = 60 * time.Second

	// How close to full scale a frame is allowed to come. Speech peaks 12 to 18 dB above its own
	// average, so a target set on the average alone asks for gain that clips the peaks: the loudest
	// sample in the frame is what decides how much gain it can actually take.
	peakDBFS = -3.0

	// Where the gain starts, so the first thing said after a boot is not the one utterance that
	// climbs from nothing. The vendor's own leveller carries a fixed 20 dB and adapts around it.
	startGainDB = 20.0

	fallTime = 500 * time.Millisecond
	riseTime = 5 * time.Second
)

// How loud the room is, published for anything that wants to show it rather than hear it.
//
// It is measured against the floor rather than against full scale, which makes it a number about the
// room instead of a number about the gain in front of it: silence reads zero in a quiet house and a
// noisy one, and someone talking reads much the same either way. That is what a ring reacting to the
// room needs, and no absolute level could give it.
const (
	// The level is measured on a low-passed copy of the frame, because the LED ring's own whine covers
	// everything above about a kilohertz — 25 dB of it at 3 to 5 kHz, measured, against nothing at all
	// below 1 kHz. Left in, it makes a lit ring read as a busy room, which for anything driving the
	// ring from the level is a loop: light, floor rises, reads busier, lights more.
	//
	// Speech keeps its fundamentals and its first formant down here, so this costs little: what is
	// given up is the difference between a voice and a fricative, which is not what a light needs.
	//
	// The bottom is cut for the opposite reason. A room's own steady noise is concentrated there —
	// measured 7 dB louder at 100 to 400 Hz than at 400 to 1000 in a quiet room, which is mains hum,
	// a compressor and a fan — and it is the part of the room that never stops. Speech keeps its
	// formants above it either way.
	levelLowHz  = 200
	levelHighHz = 900
	levelPoles  = 3

	// Where the top of the scale sits, over the floor. Measured on someone talking in the room: 23 dB
	// over the floor at the 90th percentile of frames and 30 dB at the loudest, in this band. So
	// ordinary speech sits high without pinning, and only the loudest of it fills the ring.
	//
	// The gate under it is Microphone.Sensitivity, and the span between them is what is left. Raising
	// the gate without this fixed would push the top past anything a room produces, and nothing would
	// ever fill the ring.
	levelTopDB = 30.0

	// The level rises almost as fast as the audio does and falls slowly, because it is watched at 25
	// frames a second by something with twelve lights: following the decay of every syllable turns a
	// meter into a flicker, while a slow attack loses the start of the word that is the whole point.
	levelAttack  = 40 * time.Millisecond
	levelRelease = 180 * time.Millisecond
)

type leveler struct {
	gain float32

	// least is how low the floors that decide anything may go, and roomLeast how low the one published as
	// the room's floor may go (see quietest and roomQuietest). Both follow the analog gain and so are set
	// from outside the reader that uses them. quiet is the same for the gate over the floor, in dB.
	least     atomic.Uint32
	roomLeast atomic.Uint32
	quiet     atomic.Int32

	// peak is the most the level has reached since somebody asked, for something sampling far more slowly
	// than the level moves. Only the reader writes it; asking clears it.
	peak atomic.Uint32

	// floor is the quietest the room has been heard to be, which is what speech is judged against.
	floor float32

	// How much of the way to the wanted value one frame moves, for the gain and for the floor.
	fall, rise          float32
	floorDown, floorUp  float32
	target, ceiling     float32
	headroom, overFloor float32

	// How fast the published level follows the audio, up and down.
	levelUp, levelDown float32

	// The published level runs on its own band-limited measurement: lp and hp are the filter state,
	// lpA and hpA their coefficients, and bandFloor follows the room in that band the way floor does
	// in the whole, down to roomLeast rather than least: it is the room's floor as published, and the
	// level is measured against it or least, whichever is higher.
	lp                 [levelPoles]float32
	hp                 float32
	lpA, hpA           float32
	bandRMS, bandFloor float32
	// levelFloor is bandFloor held at least, followed on its own: the floor the published level is
	// measured against. Taken from bandFloor it would sit at least while bandFloor climbed back up
	// from far under it, so a fan starting after a long quiet spell would read as loud for most of a
	// minute rather than becoming the room as it did before #377.
	levelFloor float32

	// unclamped follows the band the way bandFloor does without the least it may go to, and lowest is the
	// least it reached since the last log line, unclampedFrames frames ago: how quiet the room really is,
	// with nothing holding it up. Logged hourly, it is what roomQuietest was set from (#377) and what
	// either least is to be checked against on boards they were never measured on.
	unclamped, lowest float32
	unclampedFrames   int

	// published is the gain, for anything outside the reader that wants to log it. level is how loud
	// the room is, for anything that wants to show it. bandPublished is bandFloor, copied out for the
	// same reason: the reader updates it every frame and diagnostics ask for it from its own
	// goroutine, so the float itself is the reader's alone and this is what anyone else reads.
	published     atomic.Uint32
	bandPublished atomic.Uint32
	level         atomic.Uint32
	clipped       atomic.Uint64
}

// forget throws away what has been learned, so switching leveling off and on again is a way out of
// a room it has adapted badly to.
func (l *leveler) forget() {
	l.gain = float32(math.Pow(10, startGainDB/20))
	l.floor, l.bandFloor, l.levelFloor = fullScale, fullScale, fullScale
	l.publish()
}

func (l *leveler) publish() {
	l.published.Store(math.Float32bits(l.gain))
	l.bandPublished.Store(math.Float32bits(l.bandFloor))
}

func newLeveler() *leveler {
	frame := float64(FrameSamples) / float64(Rate)

	l := &leveler{
		gain:      float32(math.Pow(10, startGainDB/20)),
		floor:     fullScale,
		fall:      float32(1 - math.Exp(-frame/fallTime.Seconds())),
		rise:      float32(1 - math.Exp(-frame/riseTime.Seconds())),
		floorDown: float32(1 - math.Exp(-frame/floorFall.Seconds())),
		floorUp:   float32(1 - math.Exp(-frame/floorRise.Seconds())),
		target:    float32(math.Pow(10, targetDBFS/20) * fullScale),
		ceiling:   float32(math.Pow(10, maxGainDB/20)),
		headroom:  float32(math.Pow(10, peakDBFS/20) * fullScale),
		overFloor: float32(math.Pow(10, speechOverFloorDB/20)),

		levelUp:   float32(1 - math.Exp(-frame/levelAttack.Seconds())),
		levelDown: float32(1 - math.Exp(-frame/levelRelease.Seconds())),

		bandFloor:  fullScale,
		levelFloor: fullScale,
		unclamped:  fullScale,
		lowest:     fullScale,
		lpA:        float32(1 - math.Exp(-2*math.Pi*levelHighHz/Rate)),
		hpA:        float32(1 - math.Exp(-2*math.Pi*levelLowHz/Rate)),
	}

	l.atGain(config.Get().Microphone.Gain)
	l.atSensitivity(config.Get().Microphone.Sensitivity)
	return l
}

// atGain tells the leveler what the analog gain is now, which is what decides how low its floors may go.
func (l *leveler) atGain(db int) {
	l.least.Store(math.Float32bits(quietest(db)))
	l.roomLeast.Store(math.Float32bits(roomQuietest(db)))
}

// atSensitivity sets how far over the floor counts as something happening.
func (l *leveler) atSensitivity(db int) { l.quiet.Store(int32(db)) }

const fullScale = 32768

func abs(s int16) int32 {
	if s < 0 {
		return -int32(s)
	}
	return int32(s)
}

// observe measures a frame and follows the room with it, whether or not the gain is being applied.
// Leveling is a switch, and turning it off should not blind anything watching how loud the room is:
// what is published here is a ratio, so it means the same either way.
func (l *leveler) observe(frame []int16) (rms, peak float32) {
	var sum, band float64
	for _, s := range frame {
		sum += float64(s) * float64(s)
		peak = max(peak, float32(abs(s)))

		// The same sample band-passed, for the level. A cascade of one-poles rather than anything
		// sharper: what is being kept out is octaves away on both sides, so gentle is plenty, and this
		// runs on every frame of every capture.
		v := float32(s)
		for i := range l.lp {
			l.lp[i] += (v - l.lp[i]) * l.lpA
			v = l.lp[i]
		}
		l.hp += (v - l.hp) * l.hpA
		v -= l.hp

		band += float64(v) * float64(v)
	}
	rms = float32(math.Sqrt(sum / float64(len(frame))))
	l.bandRMS = float32(math.Sqrt(band / float64(len(frame))))

	// Follow the room, quickly down and slowly up. Both bands track their own, since a floor from the
	// wrong band is not a floor. The whole band's floor only gates the gain, so it stops at least; the
	// band's is also the room's floor as published, so it goes on down to roomLeast, and the level
	// holds it at least itself.
	least := float32(math.Float32frombits(l.least.Load()))
	l.floor = follow(l.floor, rms, l.floorDown, l.floorUp, least)
	l.bandFloor = follow(l.bandFloor, l.bandRMS, l.floorDown, l.floorUp, math.Float32frombits(l.roomLeast.Load()))
	l.bandPublished.Store(math.Float32bits(l.bandFloor))
	l.logUnclamped(least)

	l.levelFloor = follow(l.levelFloor, l.bandRMS, l.floorDown, l.floorUp, least)
	l.publishLevel(l.bandRMS, l.levelFloor)
	return rms, peak
}

// The floors track how quiet the room gets, and this is as low as a floor that decides something may go:
// the one the gain's speech gate, the published level and how far a voice stood out (standout.go) are
// measured against. Not because a room cannot be quieter, but because down there a frame's loudness says
// more about the converter and the rounding than about the room: with the microphones cut this band reads
// 4 to 8 at the default gain on the board it was first measured on, and a floor of one quantisation step
// puts that 12 to 15 dB over nothing — so the wobble of noise alone crosses whatever gate sits above it
// and back, for as long as the floor takes to climb.
//
// The Dots, the Echo 2 and the Spot are quieter than that, and a quiet room on them sits under this nearly
// all the time (#377): hourly, the lowest the band got at the default gain was a median of -88 to -96
// dBFS across five devices, against -66.2 here: around one sample step (-90.3) or under it. That is not
// a reason to lower this one, because everything measured against it was set with the floor resting here
// in a quiet room. Let the floor fall another 25 to 30 dB and each of them moves by that much: the level's
// sensitivity and its span (levelTopDB) would put ordinary speech 50 dB over the floor and pin the ring,
// and start the level on sounds that read nothing today; the gain's speech gate would open on the faint
// sounds between words and carry the gain to its ceiling, bringing the room up with it; and a device
// whose converters round a quiet room to almost nothing would stand out further than one across the room
// for that alone. Below a step the frames are also mostly rounding, a few stray steps among zeros, so the
// lower the floor goes the more of what crosses a gate over it is the rounding. Lowering it for these
// devices means measuring all of that again. What it was too high for is showing the room, and the floor
// published for that has a least of its own (roomQuietest).
//
// It moves with the analog gain because that gain is ahead of the converter and lifts its noise along with
// the room. A fixed number would be right at one setting and wrong across the range: too low near the
// default and, at low gain, high enough to sit above a quiet room's whole band and silence it.
//
// Extrapolated from the one gain it was measured at, so quantisation is treated as the part that does not
// scale. Measuring the cut-microphone band noise at 0 and 59 dB would replace the extrapolation.
const (
	quietestAtGain = 16.0
	quietestGainDB = config.DefaultMicGain
	quietestLimit  = 2.0
)

// The room's floor as published goes this far under quietest, so a quiet room reads as quiet as it is
// rather than resting on a limit. The quietest hour logged on any of the five devices in #377 was 41 dB
// under quietest (-107.4 dBFS at the default gain); 50 leaves room for a quieter house than any of those,
// and still stops short of nothing, which would read as minus infinity. It moves with the gain because
// quietest does: a room is measured through that gain, so it moves the room's reading with it.
const roomUnderQuietestDB = 50.0

// roomQuietest is the lowest the room's published floor may go at a given analog gain.
func roomQuietest(db int) float32 {
	return quietest(db) * float32(math.Pow(10, -roomUnderQuietestDB/20))
}

// unclampedEvery is how many frames apart the unclamped floor is logged: an hour.
const unclampedEvery = int(time.Hour / time.Second * Rate / FrameSamples)

// logUnclamped follows the room's band with no least, and once an hour logs the quietest it got beside
// the least the level is held to and the least the published floor is.
func (l *leveler) logUnclamped(least float32) {
	l.unclamped = follow(l.unclamped, l.bandRMS, l.floorDown, l.floorUp, 0)
	l.lowest = min(l.lowest, l.unclamped)
	if l.unclampedFrames++; l.unclampedFrames < unclampedEvery {
		return
	}
	dB := func(v float32) float64 { return math.Round(200*math.Log10(float64(max(v, 0.01))/fullScale)) / 10 }
	room := math.Float32frombits(l.roomLeast.Load())
	slog.Info("room floor, unclamped", "lowest_dbfs", dB(l.lowest), "least_dbfs", dB(least),
		"room_least_dbfs", dB(room), "lowest", math.Round(float64(l.lowest)*10)/10,
		"least", math.Round(float64(least)*10)/10)
	l.lowest, l.unclampedFrames = fullScale, 0
}

// quietest is the lowest a floor may go at a given analog gain.
func quietest(db int) float32 {
	return float32(max(quietestAtGain*math.Pow(10, float64(db-quietestGainDB)/20), quietestLimit))
}

// follow moves a floor toward what was just heard, quickly down and slowly up, and never below the least
// the converter can say anything about.
func follow(floor, rms, down, up, least float32) float32 {
	step := up
	if rms < floor {
		step = down
	}
	return max(floor+(rms-floor)*step, least)
}

// levelFrom maps how far a frame stands over the room onto 0 to 1: nothing until it clears quiet,
// full at span above that, and curved in between.
//
// Kept separate from the smoothing so it can be swept over real captures — which is the only way to
// choose these numbers, since what they have to be right about is one particular room.
func levelFrom(overDB, quiet, span float64) float64 {
	x := min(max((overDB-quiet)/span, 0), 1)

	// The curve leaves the middle alone and pulls the bottom down: a room that has only just cleared
	// quiet reads as a fraction of what it would, while anything that counts as someone talking is
	// where it was. Raising quiet instead does the first part and loses the second, because it moves
	// everything down by the same amount.
	return x * x * x * (x*(6*x-15) + 10)
}

// publishLevel turns this frame's loudness into how loud the room is, from 0 to 1, measured over floor:
// the room's floor, held at quietest.
func (l *leveler) publishLevel(rms, floor float32) {
	over := 0.0
	if rms > floor {
		over = 20 * math.Log10(float64(rms/floor))
	}
	quiet := float64(l.quiet.Load())
	want := float32(levelFrom(over, quiet, levelTopDB-quiet))

	was := math.Float32frombits(l.level.Load())
	step := l.levelUp
	if want < was {
		step = l.levelDown
	}
	now := was + (want-was)*step
	l.level.Store(math.Float32bits(now))

	if now > math.Float32frombits(l.peak.Load()) {
		l.peak.Store(math.Float32bits(now))
	}
}

// apply scales a frame in place, following the level it has been carrying rather than this frame
// alone: gain that jumped per frame would pump audibly and change what a wake model hears mid-word.
func (l *leveler) apply(frame []int16) { l.applyFrom(frame, nil) }

// applyFrom levels frame as apply does, but brings up exact instead where it is as long as the frame:
// the same samples as frame before they were rounded to whole steps (noise reduction's output, #662),
// so a quiet stretch that rounded to zero comes back as the faint sound it was rather than staying
// digital silence. The gain is worked out from frame either way.
func (l *leveler) applyFrom(frame []int16, exact []float32) {
	if len(frame) == 0 {
		return
	}
	fine := len(exact) >= len(frame)
	rms, peak := l.observe(frame)

	// Silence says nothing about how loud speech is, so the gain is left where speech left it.
	if rms > l.floor*l.overFloor {
		want := min(max(l.target/rms, 1), l.ceiling)

		step := l.rise
		if want < l.gain {
			step = l.fall
		}
		l.gain += (want - l.gain) * step
	}

	// Whatever the average asked for, this frame does not get gain that would push its loudest sample
	// into the ceiling. Smoothing cannot do this part: an onset arrives inside one frame.
	if peak > 0 {
		l.gain = min(l.gain, l.headroom/peak)
	}

	l.publish()

	for i, s := range frame {
		in := float32(s)
		if fine {
			in = exact[i]
		}
		switch v := float32(math.Round(float64(in * l.gain))); {
		case v > fullScale-1:
			frame[i] = fullScale - 1
			l.clipped.Add(1)
		case v < -fullScale:
			frame[i] = -fullScale
			l.clipped.Add(1)
		default:
			frame[i] = int16(v)
		}
	}
}

// SetLeveling turns leveling on or off. It takes effect on the next frame.
func (s *Source) SetLeveling(on bool) {
	s.leveling.Store(on)
	slog.Info("microphone leveling", "on", on)
}

// SetDenoising turns the noise estimator on or off. It takes effect on the next frame.
func (s *Source) SetDenoising(on bool) {
	s.denoising.Store(on)
	slog.Info("microphone noise reduction", "on", on)
}

// SetSensitivity sets how far over the room's own floor counts as something happening, in dB.
func (s *Source) SetSensitivity(db int) {
	s.leveler.atSensitivity(db)
	slog.Info("room sensitivity", "over_floor_db", db)
}

// Floor is the quietest the room has been heard to be, in dBFS, in the band the level measures. It is the
// room's own noise: it follows a fan starting, a window opening, a house waking up. The level is measured
// against it, or against quietest where the room is quieter than that, so in a quiet room this can read
// well under where the level starts counting (#377).
//
// It moves with the analog gain as well, so changing that is a step in the history rather than the room
// having changed.
// Against this package's own full scale, not audio.DBFS: the leveler works in the int16 the frames are
// narrowed to, and that helper is against 24 bit.
func (s *Source) Floor() float64 {
	return 20 * math.Log10(float64(math.Float32frombits(s.leveler.bandPublished.Load()))/fullScale)
}

// Peak is the most the level reached since the last time it was asked, and asking resets it.
//
// The level itself is worth reading at 25 frames a second and nothing else; sampled every few minutes it
// reads zero almost always and catches a syllable now and then. The peak over the interval is the useful
// shape of the same thing: whether anything happened in this room while nobody was looking.
func (s *Source) Peak() float64 {
	return float64(math.Float32frombits(s.leveler.peak.Swap(0)))
}

// Gain is what the leveler is currently applying, in dB.
func (s *Source) Gain() float64 {
	return 20 * math.Log10(float64(math.Float32frombits(s.leveler.published.Load())))
}

// Clipped is how many samples the leveler has had to hold at full scale.
func (s *Source) Clipped() uint64 { return s.leveler.clipped.Load() }

// Level is how loud the room is now, from 0 for quiet to 1 for someone talking close by. It is
// measured against the room's own noise floor rather than against full scale, so it means the same in
// a quiet house as in a noisy one, and it is published whether or not leveling is switched on.
//
// It is deliberately smoothed for looking at rather than for measuring: quick to rise, slow to fall.
func (s *Source) Level() float64 {
	return float64(math.Float32frombits(s.leveler.level.Load()))
}
