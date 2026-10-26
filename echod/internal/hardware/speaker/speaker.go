package speaker

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/alsa"
	"github.com/HuskerMinion/techo5/echod/internal/lib/asp"
	"github.com/HuskerMinion/techo5/echod/internal/lib/audio"
	"github.com/HuskerMinion/techo5/echod/internal/lib/echoref"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hook"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
	"github.com/HuskerMinion/techo5/echod/internal/lib/spectrum"
	"github.com/HuskerMinion/techo5/echod/internal/service"
)

// The playback codec accepts one format only: 48 kHz, S16_LE, stereo.
const (
	Rate     = 48000
	Channels = 2
	Bits     = 16

	// period and periods, the ring geometry, are the device's own: see paths_dot.go and
	// paths_cronos.go. On cronos the vendor driver panics the kernel past 12 KB.

	// full is int16 full scale, which is what the tuning's thresholds are in dB of.
	full = 32768

	// codecSettle is how long the codec sits powered and idle before the amplifier is enabled,
	// measured from the vendor HAL doing the same thing on a route change.
	codecSettle = 1100 * time.Millisecond

	// muteSettle is how long an amplifier with a mute of its own (MuteSwitch) sits switched on before
	// the mute is let go, so the switch's own step is behind the mute and not heard.
	muteSettle = 75 * time.Millisecond

	// unmuteAfter is how many periods the stream writes before an amplifier mute is let go: one more
	// than the ring holds, so the write that returns has had to wait for the card to take a period,
	// and the card is playing, not only filled.
	unmuteAfter = periods + 1

	// upsampleTaps is the length of each phase of the filter that takes a period up to the card's rate, where
	// the card runs faster than Rate: 96 taps at 96 kHz, flat to 21 kHz and about 74 dB down on the
	// images from 27 kHz. 1 ms of delay.
	upsampleTaps = 48
)

// VoiceRate is the rate Home Assistant's pipeline works at, and VoiceUpsample how many playback
// samples each of its samples becomes.
const (
	VoiceRate     = 16000
	VoiceUpsample = Rate / VoiceRate
)

// Card is the sound card; PlaybackDevice, the PCM on it, is the board's own (paths_*.go).
const Card = 0

// OutputMode selects automatic routing, the speaker, headphones when plugged in, or both at once.
type OutputMode int32

const (
	// OutputModeAuto follows the jack switch.
	OutputModeAuto OutputMode = iota
	// OutputModeSpeaker selects the internal speaker.
	OutputModeSpeaker
	// OutputModeHeadphone selects headphones when plugged in, otherwise the speaker.
	OutputModeHeadphone
	// OutputModeBoth plays the speaker and, when plugged in, the headphones too, where the board can
	// (HasBoth); elsewhere it is OutputModeAuto.
	OutputModeBoth
)

// Player owns the speaker: one playback stream held open for the life of the process, with the
// amplifier enabled while it runs.
type Player struct {
	// The hardware, taken by Start and let go by Close. Nil in between: the handle outlives the
	// device so that a restart can take it again without everything holding a Player being rebuilt.
	devMu sync.Mutex
	pb    playback
	mixer *alsa.Mixer
	// pbFormat is what pb was opened in (formatFor), which Run starts from: the output can change
	// between the open and the first period, and the format it had at the open is what the card holds.
	pbFormat cardFormat

	// hold is DRAMHold, kept open for as long as pb is: see paths_cronos.go.
	hold     *os.File
	mainline bool

	// Jack changed: a headphone was physically plugged in or pulled out.
	OnJack hook.Hook[Output]

	// sink, when set, is where the audio goes instead of the codec: see sink.go.
	sink atomic.Pointer[sinkState]
	// OnSinkSound is sound beginning to go to the sink after it was quiet (SinkSounding). It is emitted
	// from the write loop, so a listener must not block.
	OnSinkSound hook.Hook[struct{}]

	volume atomic.Uint32 // linear gain, derived from step and the current output's curve
	step   atomic.Int32
	// gainMu serializes amplifier writes and the saved gain reapplied when the stream starts.
	gainMu sync.Mutex
	gainDB int

	pathMu sync.Mutex
	out    Output
	// outNow is out for readers that must not wait on pathMu: the write loop and the volume. setOutput
	// holds pathMu across the codec writes, and on the 1st gen Show 5 those are slow enough that a write
	// loop waiting on them missed the ring once at every switch. Written under pathMu, with out.
	outNow atomic.Value
	// draining is a switch to the jack waiting for audio at the old gain to clear (setOutput).
	draining     bool
	jack         Output
	outputMode   OutputMode
	outputModeMu sync.Mutex

	voiceMu    sync.Mutex
	voice      Resampler
	resampling config.Resampling
	splices    atomic.Uint64

	// Each internal speaker driver needs its own filter and limiter history. The vendor tuning
	// is shared and immutable; only the write loop touches either processing chain.
	// Mono uses the first lane; athena, which can play stereo, has a second lane and chain for its
	// other driver, so stereo turns on and off while playing (SetStereo).
	tuning *asp.Tuning
	// canStereo is a board with two internal drivers that can play stereo: athena. stereo is whether
	// it does, which starts mono unless speaker.stereo is saved.
	canStereo bool
	stereo    atomic.Bool
	// On athena the first playback PCM slot feeds the physical right speaker.
	// Reverse logical L/R on internal speakers, never on the headphone-only path.
	swapLR bool
	chains []*asp.Chain
	lanes  [][]float32
	// first is the volume curve to apply in front of the tuning, where this tuning has one
	// (firstCurves); nil turns down what the tuning produced instead.
	first *[VolumeSteps + 1]float64
	on    atomic.Bool
	stale atomic.Bool

	// tone is the listener's own shelves, waiting to be picked up: the chain belongs to the write
	// loop, so a setting changed from anywhere else is left here and applied there. applied is what
	// the chain is carrying now, and is the write loop's alone.
	tone    atomic.Value // asp.Tone
	applied asp.Tone

	// fed is whether the last block had anything in it, and belongs to the write loop alone.
	fed bool

	// playing is whether the card is taking the stream's periods (unmuteAfter), which is when an
	// amplifier mute (MuteSwitch) may be let go.
	playing atomic.Bool

	// gate is the amplifier switched off while idle (idleGate), nil on every board that keeps it as it
	// is; idle decides when, and claimed is a claim holding the speaker (Hold). See idle.go.
	gate    *ampGate
	idle    idleAmp
	claimed atomic.Bool
	// wakeFails is the gated amplifier's wakes that failed in a row (ampWake).
	wakeFails atomic.Int32

	// underruns is the card running out while we were away. Write blocks against the whole ring, so
	// each one means the loop was starved for as long as the ring is deep.
	underruns atomic.Uint64

	// deaf counts what was thrown away for want of a device.
	deaf atomic.Uint64

	mu      sync.Mutex
	pending []int16 // interleaved stereo waiting to go out
	// overlay is how many samples at the front of pending carry a sound mixed in by Overlay (a chime, a
	// beep), so whatever else is queued there knows not to drain it away (Overlaying).
	overlay int

	// bed is a track's audio, queued apart from pending so an answer or an announcement can sound
	// over it rather than after it: the two are summed as they go out, as a Source is. Same stereo,
	// same media volume; a track ducks its own samples before they are queued.
	bed []int16

	// under is a call's far end played under whatever took the speaker from it (a turn's reply, an
	// announcement), a lane of its own beside bed: bed is a track's, which keeps what is left in it as the
	// track's own audio and resumes paused music into it. Summed as bed is, at the media volume.
	under []int16

	// bell is a ring's chime, queued apart from everything else because it goes out at a level of
	// its own: bellStep rather than the media volume. ringing holds that level for the whole of a
	// ring rather than only while a chime sounds, so the music under it does not change tuning
	// between one chime and the next. See fill.
	bell     []int16
	bellStep atomic.Int32
	ringing  atomic.Bool

	// written counts frames handed to the card, which is what a Source places audio against.
	written atomic.Uint64

	// ring is the card's last word on what it holds, published as one value with the write count it
	// belongs to, so a reader never pairs a count with a delay read at another write. Nil until the
	// card has said (no device yet, or a driver that will not answer): see Latency and Position.
	ring atomic.Pointer[ringReading]

	srcMu  sync.Mutex
	src    Source
	srcBuf []int16
}

// Source is asked for the frames the card is about to play, addressed by absolute output frame index.
// A room playing along with the house needs that: where audio lands has to follow from when the server
// said to play it, not from when it happened to arrive.
type Source interface {
	// Render fills out with the frames starting at output frame index at, silence where it has none.
	Render(at uint64, out []int16)
}

// Attach sets the Source, or clears it with nil. Only one at a time: two things placing audio by
// absolute frame would be two things deciding what the room plays.
func (p *Player) Attach(s Source) {
	p.srcMu.Lock()
	defer p.srcMu.Unlock()
	p.src = s
}

// Written is the output frame index of the next frame to be handed to the card.
func (p *Player) Written() uint64 { return p.written.Load() }

// ringReading is one answer from the card: just after the write that brought the count to written,
// it still had frames to play, as of at.
type ringReading struct {
	written uint64
	frames  int64
	at      time.Time
}

// OutputExtra is what the path adds after the ring: the tuning's limiter looks 2 ms ahead (lib/asp
// mbcl.go), and the AFE's FIFO and the codec's DAC filters about another. Estimated from the parts,
// not measured; a room that still sits a few milliseconds off another is where to look first.
const OutputExtra = 3 * time.Millisecond

// DefaultLatency is Latency when the card will not say: a full ring, which is what the write loop
// keeps it at, and the path after it.
const DefaultLatency = time.Duration(period*periods)*time.Second/Rate + OutputExtra

// Latency is how long a frame rendered now waits before it is heard: the frames the card still had
// in its ring after the last write, less what it has played since it said so, and the path after the
// ring. This is the output latency a synchronized stream has to lead by. It is not HardwareTail,
// which is a margin for deciding the room has gone quiet and is deliberately generous.
//
// The reading is taken in the write loop and used from the network goroutine, up to a period later
// (16 ms on a Show 5 or a Spot, 21 on a Dot), so the time since it was taken comes off: without that,
// the anchor sat up to a period early.
//
// A full ring is 3072 frames (64 ms) on a Show 5 or a Spot and 4096 (about 85 ms) on a Dot, and just
// after a write the card holds close to all of it. Measured on a 1st gen Show 5 (2026-10-02, idle loop feeding
// silence), at arbitrary moments it held 2528 to 2880 frames, 53 to 60 ms.
//
// A sink (Bluetooth) is not counted: the codec keeps its pace on silence while the sink plays the
// audio with a delay of its own that nothing here knows.
func (p *Player) Latency() time.Duration {
	_, l := p.Position()
	return l
}

// Position is the index of the next frame to go to the card and how long, from now, until it is
// heard: both from the same reading, so the frame and its latency always belong together.
func (p *Player) Position() (written uint64, latency time.Duration) {
	r := p.ring.Load()
	if r == nil {
		return p.written.Load(), DefaultLatency
	}
	left := time.Duration(r.frames)*time.Second/Rate - time.Since(r.at)
	return r.written, max(left, 0) + OutputExtra
}

// stereoInternalSpeakers permits stereo only on athena after explicit opt-in.
// Every board uses two-channel PCM, so channel count alone cannot decide.
func stereoInternalSpeakers(board string, enabled bool) bool {
	return board == "athena" && enabled
}

// New makes the speaker without taking the hardware, so callers can hold it before there is anything
// to play through. Audio queued before Start waits; Volume and the rest work throughout.
func New() *Player {
	detected := DetectOutput()
	p := &Player{
		out:       detected,
		jack:      detected,
		canStereo: stereoInternalSpeakers(layout.Board, true),
		swapLR:    layout.Board == "athena",
		gate:      idleGate,
	}
	p.stereo.Store(stereoInternalSpeakers(layout.Board, config.Get().Speaker.Stereo))
	if p.canStereo {
		slog.Info("speaker stereo", "on", p.stereo.Load())
	}
	p.outNow.Store(detected)
	p.voice, p.resampling = NewResampler(config.ResampleSinc)
	p.SetVolume(VolumeSteps)
	p.on.Store(config.DefaultASP)

	if !DriverTuning {
		slog.Info("this board plays without the driver tuning for now", "board", layout.Board)
		return p
	}
	load := asp.Load
	if tuningSet != nil {
		load = func(dir string) (*asp.Tuning, error) { return asp.LoadSet(dir, *tuningSet) }
	}
	t, err := load(asp.VendorDir)
	if err != nil {
		slog.Error("the driver tuning is not available, playing untuned", "err", err)
		return p
	}
	lanes := 1
	if p.canStereo {
		lanes = Channels
	}
	for range lanes {
		c, err := t.Chain(period)
		if err != nil {
			slog.Error("the driver tuning will not run, playing untuned", "err", err)
			p.chains, p.lanes = nil, nil
			return p
		}
		p.chains = append(p.chains, c)
		p.lanes = append(p.lanes, make([]float32, period))
	}
	p.tuning = t
	if curve, ok := firstCurves[t.Name()]; ok {
		p.first = &curve
	}
	return p
}

var (
	once   sync.Once
	shared *Player
)

func init() {
	// The audio devices are held for the life of the process: whatever is free, Android takes. Losing
	// one to Android is what a restart takes back, which is why the handle outlives the service.
	//
	// The speaker also feeds silence while idle, because the amplifier hisses when nothing drives the
	// DAC and toggling it pops. Where the amplifier can be put to sleep without a pop (idleGate, the
	// 1st gen Show 5), it is, and the silence goes on under it: see idle.go.
	component.Register(component.Hardware, Get(), component.Order(6),
		component.Supervise(service.Restart(time.Second, 30*time.Second)))
}

// Get is the speaker. There is one, and everything audible goes through it.
func Get() *Player {
	once.Do(func() { shared = New() })
	return shared
}

func (p *Player) Name() string { return "speaker" }

// open takes the playback stream and the mixer. Mixer writes happen in Run: the first one enumerates
// the card, which takes over a second.
func (p *Player) open() error {
	f := formatFor(p.Output())
	pb, m, hold, mainline, err := openDevice(f)
	if err != nil {
		return err
	}

	p.devMu.Lock()
	p.pb, p.mixer, p.hold, p.pbFormat = pb, m, hold, f
	p.mainline = mainline
	p.devMu.Unlock()
	return nil
}

// device is the hardware, or nils when it is not held.
func (p *Player) device() (playback, *alsa.Mixer) {
	p.devMu.Lock()
	defer p.devMu.Unlock()
	return p.pb, p.mixer
}

// route applies the output's sequence. The amplifier stays off: it is enabled separately, after
// the codec has settled.
func (p *Player) route() {
	p.pathMu.Lock()
	defer p.pathMu.Unlock()
	// A switch to the jack waiting for the old-gain audio to clear opens the path itself after.
	if p.draining {
		return
	}
	// The codec keeps its registers across a restart of the daemon, so the jack a previous run opened
	// is still open: the speaker closes it, as setOutput does.
	if p.out == OutputSpeaker {
		p.apply(headphoneOff)
	}
	p.apply(p.path(p.out))
}

// path is out's sequence, with the Speaker gain in it where the board writes that with the path
// (gainOnPath). Every place that writes a path takes it from here, so a gain chosen once holds through
// a start, a route change, the jack coming and going, and a path written again (Reopen). Called with
// pathMu held, which is what guards gainDB where gainOnPath is set (setPathGain).
func (p *Player) path(out Output) []kctl {
	if !gainOnPath {
		return pathSequence[out]
	}
	return withSpeakerGain(pathSequence[out], out, p.gainDB)
}

// amp switches the speaker amplifier, where the device has a switch that is safe to touch: see
// AmpSwitch in paths_dot.go and paths_cronos.go. Turning it on also lets go of the amplifier's mute
// (MuteSwitch) once the stream is playing; before then Run lets go of it itself.
//
// A board with an idle gate (idleGate) switches that instead, in its own pop-free order, and on any
// output: switching on there is also waking it from an idle sleep, which a path written again (a
// start, a change of output, Reopen) is the moment for.
func (p *Player) amp(on bool) {
	if p.gate != nil {
		if on {
			p.ampWake()
		} else {
			p.ampSleep()
		}
		return
	}
	if AmpSwitch == "" && MuteSwitch == "" {
		return
	}

	p.pathMu.Lock()
	defer p.pathMu.Unlock()

	if p.out != OutputSpeaker {
		return
	}
	if !on {
		p.apply(ampOff())
		return
	}
	on1, release := ampOn(p.playing.Load())
	p.apply(on1)
	if len(release) > 0 {
		time.Sleep(muteSettle)
		p.apply(release)
	}
}

// unmute lets go of the amplifier's mute once the stream has started, where the speaker is the output
// and its path is in place.
func (p *Player) unmute() {
	if MuteSwitch == "" {
		return
	}
	p.pathMu.Lock()
	defer p.pathMu.Unlock()
	if p.out != OutputSpeaker || p.draining {
		return
	}
	p.apply([]kctl{{name: MuteSwitch, value: "Off"}})
}

// stopped holds the amplifier's mute again as the stream ends, for whatever reason it ends.
func (p *Player) stopped() {
	p.playing.Store(false)
	if MuteSwitch == "" {
		return
	}
	p.pathMu.Lock()
	defer p.pathMu.Unlock()
	p.apply([]kctl{{name: MuteSwitch, value: "On"}})
}

// ampOff silences the speaker's amplifier: its mute first, where the board has one, then its switch.
func ampOff() []kctl {
	var seq []kctl
	if MuteSwitch != "" {
		seq = append(seq, kctl{name: MuteSwitch, value: "On"})
	}
	if AmpSwitch != "" {
		seq = append(seq, kctl{name: AmpSwitch, value: "Off"})
	}
	return seq
}

// ampOn is ampOff the other way round: the switch first (on), and the mute let go last (unmute),
// muteSettle later, and only when release says the stream is running. Without a mute, unmute is
// empty and on is all of it.
func ampOn(release bool) (on, unmute []kctl) {
	if AmpSwitch != "" {
		on = append(on, kctl{name: AmpSwitch, value: "On"})
	}
	if MuteSwitch != "" && release {
		unmute = append(unmute, kctl{name: MuteSwitch, value: "Off"})
	}
	return on, unmute
}

// Output reports which output the player is driving. It never waits on a switch in progress.
func (p *Player) Output() Output {
	out, _ := p.outNow.Load().(Output)
	return out
}

// setOutput moves the codec between the speaker and the headphone jack. The gain is re-derived
// because each output has its own curve. On a switch to the jack pathMu is let go while the audio
// already queued at the speaker's gain drains, with the jack still closed; draining keeps route from
// opening it meanwhile. On the Dot and the Spot the speaker is off then too. On the Show it plays on
// through the drain, which costs nothing there: both outputs have the same curve.
func (p *Player) setOutput(out Output) {
	p.pathMu.Lock()
	if p.out == out {
		p.pathMu.Unlock()
		return
	}
	p.out = out
	p.outNow.Store(out)
	// The tuning's history is of what went out the old way: Headphone to Both starts it again, and
	// it should not start from audio it never processed.
	p.stale.Store(true)
	p.apply(ampOff())
	if out == OutputHeadphone && p.sink.Load() == nil {
		if _, mixer := p.device(); mixer != nil {
			// Keep the jack closed until old-gain samples have cleared the playback ring.
			p.apply(headphoneOff)
			p.draining = true
			p.pathMu.Unlock()
			p.SetVolume(int(p.step.Load()))
			time.Sleep(time.Duration(period*(periods+1)) * time.Second / Rate)
			p.pathMu.Lock()
			p.draining = false
		}
	}
	if out == OutputSpeaker {
		p.apply(headphoneOff)
	}
	p.apply(p.path(out))
	p.pathMu.Unlock()

	p.SetVolume(int(p.step.Load()))
	if _, mixer := p.device(); mixer != nil {
		time.Sleep(codecSettle)
		p.amp(true)
	}

	slog.Info("output changed", "output", out)
}

// desiredOutput resolves the selected mode to the output the codec should use.
func (p *Player) desiredOutput(detected Output) Output {
	switch p.outputMode {
	case OutputModeSpeaker:
		return OutputSpeaker
	case OutputModeHeadphone:
		if detected == OutputHeadphone {
			return OutputHeadphone
		}
		return OutputSpeaker
	case OutputModeBoth:
		if HasBoth && detected == OutputHeadphone {
			return OutputBoth
		}
		return detected
	default:
		return detected
	}
}

// SetOutputMode selects automatic jack detection, the speaker, or headphones when plugged in.
func (p *Player) SetOutputMode(mode OutputMode) {
	p.outputModeMu.Lock()
	defer p.outputModeMu.Unlock()

	p.outputMode = mode
	p.setOutput(p.desiredOutput(DetectOutput()))
}

// Reopen writes the speaker's whole path again, the way Run first opens it: the init sequence, the
// route, and the amplifier once the codec has settled. Choosing Speakers when the speaker is already
// the output changes nothing in setOutput, and a path something has knocked out of place (#142) would
// stay that way until a restart; asking for the speaker by hand is the moment to put it back.
func (p *Player) Reopen() {
	p.pathMu.Lock()
	if p.out != OutputSpeaker || p.draining {
		p.pathMu.Unlock()
		return
	}
	p.apply(initSequence)
	p.apply(headphoneOff)
	p.apply(p.path(OutputSpeaker))
	p.pathMu.Unlock()

	p.SetVolume(int(p.step.Load()))
	if _, mixer := p.device(); mixer != nil {
		time.Sleep(codecSettle)
		p.amp(true)
	}
	slog.Info("speaker path written again")
}

// watchJack tracks the physical headphone jack and updates playback routing.
func (p *Player) watchJack(ctx context.Context) {
	t := time.NewTicker(jackPoll)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case <-t.C:
			detected := DetectOutput()

			p.pathMu.Lock()
			changed := p.jack != detected
			p.jack = detected
			p.pathMu.Unlock()

			if changed {
				p.OnJack.Emit(detected)
			}

			p.outputModeMu.Lock()
			p.setOutput(p.desiredOutput(detected))
			p.outputModeMu.Unlock()
		}
	}
}

// apply writes a mixer sequence in order, logging failures and continuing.
func (p *Player) apply(seq []kctl) { _ = p.applyAll(seq) }

// applyAll is apply that says whether every write was made: false with no mixer held, or when one
// failed (each failure is logged as apply logs it).
func (p *Player) applyAll(seq []kctl) bool {
	_, mixer := p.device()
	if mixer == nil {
		return false
	}
	return applyTo(mixer, seq)
}

// applyTo is apply on a mixer that is not held yet, reporting whether every write was made.
func applyTo(mixer *alsa.Mixer, seq []kctl) bool {
	ok := true
	for _, c := range seq {
		if c.ifPresent {
			if _, err := mixer.Find(c.name); err != nil {
				slog.Debug("mixer control not on this unit", "control", c.name)
				continue
			}
		}
		var err error
		switch {
		case c.value != "":
			err = mixer.SetEnum(c.name, c.value)
		case c.blob != nil:
			err = mixer.SetBytes(c.name, c.blob)
		default:
			err = mixer.SetInt(c.name, uint32(c.level))
		}
		if err != nil {
			slog.Error("mixer write failed", "control", c.name, "err", err)
			ok = false
		}
	}
	return ok
}

// Run feeds the stream until ctx is canceled, writing silence when nothing is queued.
func (p *Player) Run(ctx context.Context) error {
	pb, _ := p.device()
	if pb == nil {
		return errors.New("speaker: the playback device is not held")
	}
	buf := make([]byte, period*Channels*Bits/8)

	// The order the vendor HAL uses on a route change: route with the amplifier off, let the
	// codec sit idle, enable the amplifier, then feed. An amplifier mute (MuteSwitch) is held through
	// all of it and let go once the card is playing, on silence or whatever is queued: after
	// unmuteAfter periods, when the ring has filled and the card has taken from it.
	defer p.stopped()
	out := p.Output()
	p.initializeMixer()
	p.route()
	// Written on every start, even at 0 dB: 110 (0 dB) is the TAS5805M driver's own default (probe
	// sets its vol to 110, and every unmute writes that cached vol to the chip), so at 0 dB this
	// writes what is already there. But the driver keeps the last level written across a restart of
	// this service, so the saved gain, 0 dB included (settings reset, say), is written over it. Where
	// the gain goes with the path (gainOnPath), route has just written it.
	if !gainOnPath && gainOn(p.heldMixer()) {
		if err := p.applySpeakerGain(); err != nil {
			slog.Error("restoring speaker gain failed, continuing playback", "err", err)
		}
	}

	select {
	case <-ctx.Done():
		return nil
	case <-time.After(codecSettle):
	}

	p.amp(true)
	slog.Info("playback path up", "output", out)
	safe.Go("jack watcher", func() { p.watchJack(ctx) })

	silence := make([]byte, len(buf))
	muted := 0 // periods written while an amplifier mute is still held

	// A card that runs faster than Rate is sent each period upsampled, with the board's trim (paths_*.go),
	// and a card sent 32-bit containers each sample widened. Both follow the format the device is open in,
	// which on a board with two (formatFor) changes with the output.
	p.devMu.Lock()
	cur := p.pbFormat
	p.devMu.Unlock()
	var up *audio.Upsampler
	var hw, wide []byte
	upFor := func(f cardFormat) {
		up, hw = nil, nil
		if f.rate != Rate || hwTrimDB != 0 {
			up = audio.NewUpsampler(f.rate/Rate, upsampleTaps, Channels, math.Pow(10, hwTrimDB/20))
			hw = make([]byte, 0, len(buf)*f.rate/Rate)
			slog.Info("playback at the card's own rate", "rate", f.rate, "trim_db", hwTrimDB)
		}
	}
	upFor(cur)

	for {
		if err := ctx.Err(); err != nil {
			return nil
		}

		// The output moved to one the device is opened differently for: it is closed and opened
		// again in the new format, here, where nothing else is writing to it.
		if want := formatFor(p.Output()); !want.same(cur) {
			np, err := p.reformat(ctx, pb, want)
			if err != nil {
				return err
			}
			pb, cur = np, want
			upFor(cur)
		}

		// An amplifier waking from its idle sleep (idle.go) is not ready for what is queued: the queue
		// waits, and the card gets silence, until it is.
		if p.gate != nil && p.idleStep() {
			clear(buf)
		} else {
			p.fill(buf)
		}
		spectrum.Speaker.PushStereo(buf)
		to := buf
		if s := p.sink.Load(); s != nil {
			// The sink gets the audio; the codec keeps its pace on silence.
			s.push(buf)
			to = silence
		}
		sent := to
		if up != nil {
			hw = up.S16LE(to, hw[:0])
			to = hw
		}
		if cur.bits == 32 {
			wide = widenS16(to, wide[:0])
			to = wide
		}
		if err := p.send(ctx, pb, to); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if !p.playing.Load() {
			if muted++; muted >= unmuteAfter {
				p.playing.Store(true)
				p.unmute()
			}
		}
		// What the card holds now is what the period rendered next waits behind, so it is read here,
		// between the write and the render, and published with the count it belongs to: in frames at
		// Rate, which is what everything placing audio counts in.
		d, err := pb.Delay()
		d = d * Rate / cur.rate
		at := time.Now()
		w := p.written.Add(period)
		if err == nil && d >= 0 {
			p.ring.Store(&ringReading{written: w, frames: int64(d), at: at})
			if softRef && echoref.InUse() {
				// The period's last frame is the card's newest: heard once the ring ahead of it plays.
				echoref.Push(sent, Rate, at.Add(time.Duration(d)*time.Second/Rate+OutputExtra))
			}
		} else {
			p.ring.Store(nil)
		}
	}
}

// reformat closes pb and opens the stream again in want, on the card and mixer already held, and puts
// the new stream where the rest of the player finds it. What was still in the old ring is dropped: a
// change of output is a moment of quiet anyway (setOutput). A stream that will not open is tried again a
// few times; after that the error ends Run, and the service restarts the player, which opens the device
// in the format of the output it then has. The format's routes can change what the output's path set
// (the jack's mutes the amplifier), so the path is written again once the stream is open.
func (p *Player) reformat(ctx context.Context, pb playback, want cardFormat) (playback, error) {
	_, mixer := p.device()
	_ = pb.Close()
	// Nothing holds a stream until the new one opens, so a Close meanwhile (a shutdown during a wait
	// below) does not close this one a second time.
	p.devMu.Lock()
	p.pb = nil
	p.devMu.Unlock()
	p.ring.Store(nil)
	var err error
	for try := 0; try < reopenTries; try++ {
		var np playback
		if np, err = reopenPlayback(mixer, want); err == nil {
			p.devMu.Lock()
			p.pb, p.pbFormat = np, want
			p.devMu.Unlock()
			p.route()
			slog.Info("playback opened again for the output", "output", p.Output(), "rate", want.rate, "bits", want.bits)
			return np, nil
		}
		slog.Warn("speaker: opening playback again failed", "rate", want.rate, "bits", want.bits, "err", err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(reopenWait):
		}
	}
	return nil, fmt.Errorf("speaker: opening playback again at %d Hz: %w", want.rate, err)
}

// reopenTries and reopenWait are how often, and how far apart, reformat tries a stream that will not open.
const (
	reopenTries = 3
	reopenWait  = 200 * time.Millisecond
)

// widenS16 appends in, S16_LE samples, to out as S24_LE ones: 24 bits in the low three bytes of a
// 32-bit container, the 16 at the top of the 24 and the fourth byte the sign, so full scale stays full
// scale. For a card that is sent 32-bit containers (cardFormat.bits). S24_LE rather than S32_LE because the Echo
// Dot 3's TAS2770 plays nothing at all of S32_LE, while S24_LE plays (unit 1, 2026-10-23).
func widenS16(in, out []byte) []byte {
	for i := 0; i+1 < len(in); i += 2 {
		sign := byte(0)
		if in[i+1]&0x80 != 0 {
			sign = 0xff
		}
		out = append(out, 0, in[i], in[i+1], sign)
	}
	return out
}

// send writes one period, putting the same buffer back after an underrun rather than refilling. fill
// has already taken these frames off the queue, so starting over would play the period after them and
// lose these.
//
// The driver may also take only part of it: a signal that lands mid-write, and the runtime sends one
// to preempt goroutines, ends the write with however many frames had gone in. The rest follow.
func (p *Player) send(ctx context.Context, to io.Writer, buf []byte) error {
	for {
		n, err := to.Write(buf)
		if err == nil {
			if n >= len(buf) {
				return nil
			}
			if n <= 0 {
				// Nothing taken and nothing wrong would loop here for good.
				return io.ErrShortWrite
			}
			buf = buf[n:]
			continue
		}
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != alsa.ErrUnderrun {
			return err
		}
		if n := p.underruns.Add(1); n == 1 || n%100 == 0 {
			slog.Warn("playback underrun", "times", n)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
}

// fill takes what is queued and pads the rest with silence.
func (p *Player) fill(buf []byte) {
	p.mu.Lock()
	take := min(len(p.pending), period*Channels)
	chunk := p.pending[:take]
	p.pending = p.pending[take:]
	if len(p.pending) == 0 {
		p.pending = nil
	}
	p.overlay = max(0, p.overlay-take)
	bedTake := min(len(p.bed), period*Channels)
	bed := p.bed[:bedTake]
	p.bed = p.bed[bedTake:]
	if len(p.bed) == 0 {
		p.bed = nil
	}
	underTake := min(len(p.under), period*Channels)
	under := p.under[:underTake]
	p.under = p.under[underTake:]
	if len(p.under) == 0 {
		p.under = nil
	}
	rang := min(len(p.bell), period*Channels)
	bell := p.bell[:rang]
	p.bell = p.bell[rang:]
	if len(p.bell) == 0 {
		p.bell = nil
	}
	p.mu.Unlock()

	// The queue emptied part way through this buffer, so silence is spliced into whatever was
	// playing. At the end of a reply that is expected; repeatedly during one means audio is
	// arriving slower than it plays out.
	if take > 0 && take < period*Channels {
		p.splices.Add(1)
	}

	// The queue and a Source place audio differently — one in order, one by frame index — so they are
	// summed rather than one replacing the other, and a reply still sounds over a room stream.
	rendered := p.render()

	// The internal speakers are mixed to mono unless athena has explicitly enabled
	// stereo, which preserves independent driver processing including EQ.
	// The line-out and an external sink always receive stereo and remain untuned.
	sinking := p.sink.Load() != nil
	out := p.Output()
	speaker := (out == OutputSpeaker || out == OutputBoth) && !sinking
	stereo := p.stereo.Load()
	mono := speaker && !stereo

	// The write loop runs whether or not anything is playing, since the amplifier hisses when nothing
	// drives the DAC, and tuning silence costs what tuning music costs. The block after the audio stops
	// still goes through: that is the filter's own length, and it holds the tail.
	fed := take > 0 || bedTake > 0 || underTake > 0 || len(rendered) > 0 || rang > 0
	drain := fed || p.fed
	p.fed = fed

	// The tuning is for the internal drivers, not a line-out or external sink. In Both mode,
	// the shared mixer feeds the same tuned output to the speaker and the headphones.
	tuned := speaker && len(p.chains) > 0 && p.on.Load() && drain

	// There is one gain after the tuning, so two levels are made from it: the output goes at the
	// louder of the two, and whichever lane is quieter is scaled down by the difference before the
	// mix. The music comes out at the media volume either way, and a muted one stays muted under
	// the alarm. On the tuned path the compressor narrows that difference a little, since it works
	// on the sum; the lane that sets the output level is exact.
	//
	// The two are compared at the level they come out at: tuned with a curve in front, that is the
	// curve's, which can rise where the usual one stands still (the Spot's and the Dot's vendor curves
	// repeat values near the top).
	// A panel plays at Android's volume, which a ring raises for as long as it sounds (paths_panel.go);
	// an Echo's volume is all here.
	ringing := rang > 0 || p.ringing.Load()
	p.followRing(ringing, int(p.bellStep.Load()))
	gain, step := p.Volume(), int(p.step.Load())
	mediaK, bellK := float32(1), float32(0)
	if ringing {
		var first *[VolumeSteps + 1]float64
		if tuned {
			first = p.first
		}
		bs := int(p.bellStep.Load())
		mediaK, bellK, gain, step = ringBalance(first, out, gain, step, p.gainFor(bs), bs)
	}

	// Where the tuning has a curve of its own the volume goes in front of it, as the vendor's chain
	// has it, scaled from the gain worked out above so a bell over the music keeps its balance.
	pre, post := float32(1), gain
	if tuned && p.first != nil {
		pre, post = gain*firstRatio(p.first, out, step), 1
	}

	// The vendor tunes by volume: each bucket has a filter of its own, and on the Show the quiet ones
	// carry more bass and treble rather than simply less of everything. Telling the chain where the
	// dial is picks the one it meant.
	if tuned {
		share := float64(step) / VolumeSteps
		for _, chain := range p.chains {
			chain.Volume(share)
		}
		if want, ok := p.tone.Load().(asp.Tone); ok && want != p.applied {
			for _, chain := range p.chains {
				chain.SetTone(want)
			}
			p.applied = want
		}
	}

	for i, j := 0, 0; i < period*Channels; i, j = i+Channels, j+1 {
		var l, r int32
		if i < len(chunk) {
			l = int32(chunk[i])
		}
		if i+1 < len(chunk) {
			r = int32(chunk[i+1])
		}
		if i < len(bed) {
			l += int32(bed[i])
		}
		if i+1 < len(bed) {
			r += int32(bed[i+1])
		}
		if i < len(under) {
			l += int32(under[i])
		}
		if i+1 < len(under) {
			r += int32(under[i+1])
		}
		if i < len(rendered) {
			l += int32(rendered[i])
		}
		if i+1 < len(rendered) {
			r += int32(rendered[i+1])
		}
		if mediaK != 1 || bellK != 0 {
			var bl, br float32
			if i < len(bell) {
				bl = float32(bell[i])
			}
			if i+1 < len(bell) {
				br = float32(bell[i+1])
			}
			l = int32(float32(l)*mediaK + bl*bellK)
			r = int32(float32(r)*mediaK + br*bellK)
		}
		// The real athena speaker wiring reverses the standard PCM L/R order.
		// Apply this before either DSP branch so the same physical mapping is
		// used with tuning enabled or disabled. The software reference taps
		// the post-DSP PCM that is actually sent to the ALSA playback device.
		if speaker && stereo && p.swapLR {
			l, r = r, l
		}
		if mono {
			l = (l + r) / 2
			r = l
		}
		if tuned {
			p.lanes[0][j] = float32(clamp(l)) / full * pre
			if stereo {
				p.lanes[1][j] = float32(clamp(r)) / full * pre
			}
			continue
		}
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(limit(float32(l)*OutputBoost*gain)))
		binary.LittleEndian.PutUint16(buf[(i+1)*2:], uint16(limit(float32(r)*OutputBoost*gain)))
	}
	if !tuned {
		return
	}

	// Resetting belongs to whoever processes, so a toggle only says that the history is no longer
	// about what is playing.
	stale := p.stale.Swap(false)

	// Without a curve of its own, the volume attenuates what the tuning produced: the compressor
	// then works on full-scale music at every volume. Each driver has independent limiter state;
	// in mono the second chain, where there is one, waits, and is reset when stereo comes back.
	for i, chain := range p.chains {
		if stale {
			chain.Reset()
		}
		if i == 0 || stereo {
			chain.Process(p.lanes[i])
		}
	}
	right := p.lanes[0]
	if stereo {
		right = p.lanes[1]
	}
	for i, j := 0, 0; i < period*Channels; i, j = i+Channels, j+1 {
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(clamp16(p.lanes[0][j]*full*post)))
		binary.LittleEndian.PutUint16(buf[(i+1)*2:], uint16(clamp16(right[j]*full*post)))
	}
}

// render asks the Source for this period. Called only from the write loop, so the buffer is reused.
func (p *Player) render() []int16 {
	p.srcMu.Lock()
	src := p.src
	p.srcMu.Unlock()

	if src == nil {
		return nil
	}
	if cap(p.srcBuf) < period*Channels {
		p.srcBuf = make([]int16, period*Channels)
	}
	p.srcBuf = p.srcBuf[:period*Channels]
	clear(p.srcBuf)

	src.Render(p.written.Load(), p.srcBuf)
	return p.srcBuf
}

// Play queues interleaved stereo samples.
//
// Audio offered while the device is not held is dropped rather than queued. Nothing is draining the
// queue then, so it would grow for as long as the speaker stayed away — and a device that cannot play
// should say so in the log rather than in memory.
func (p *Player) Play(samples []int16) {
	if pb, _ := p.device(); pb == nil {
		if n := p.deaf.Add(1); n == 1 || n%100 == 0 {
			slog.Warn("audio dropped, no playback device", "times", n)
		}
		return
	}

	p.mu.Lock()
	p.pending = append(p.pending, samples...)
	p.mu.Unlock()
}

// Take empties the queue and hands back what had not been played, so a sound that yields to another
// can carry on from where it was rather than skipping whatever it had queued.
func (p *Player) Take() []int16 {
	p.mu.Lock()
	defer p.mu.Unlock()

	pending := p.pending
	p.pending = nil
	p.overlay = 0
	return pending
}

// Adjust rewrites the queue in place. Taking it out and putting it back would leave it empty in
// between, and fill pads an empty queue with silence.
func (p *Player) Adjust(rewrite func([]int16)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	rewrite(p.pending)
}

// PlayVoice queues 16 kHz mono audio, which is what Home Assistant's pipeline sends. The codec
// only accepts 48 kHz stereo, so it is interpolated up and duplicated across both channels. The
// resampler carries state between calls, so a reply delivered in chunks is one continuous signal.
func (p *Player) PlayVoice(mono []int16) {
	p.voiceMu.Lock()
	out := p.voice.Run(mono, make([]int16, 0, len(mono)*VoiceUpsample*Channels))
	p.voiceMu.Unlock()

	p.Play(out)
}

// Drain discards anything queued but not yet played, for a barge-in. The resampler's history goes
// with it: whatever comes next is a different utterance.
func (p *Player) Drain() {
	p.mu.Lock()
	p.pending = nil
	p.overlay = 0
	p.mu.Unlock()

	p.voiceMu.Lock()
	p.voice.Reset()
	p.voiceMu.Unlock()
}

// SetResampling picks how voice is stretched to the playback rate and reports what it settled on,
// which is the filter for anything this build does not have. It takes effect on the next reply.
func (p *Player) SetResampling(r config.Resampling) config.Resampling {
	p.voiceMu.Lock()
	defer p.voiceMu.Unlock()

	p.voice, p.resampling = NewResampler(r)
	return p.resampling
}

// CanStereo is whether this board's internal speakers can play stereo: athena's two drivers.
func (p *Player) CanStereo() bool { return p.canStereo }

// SetStereo plays the internal speakers in stereo or mono from the next block on, and reports what it
// settled on, which is mono on a board without two drivers. A change starts the tuning's history
// again, as turning the tuning on does.
func (p *Player) SetStereo(on bool) bool {
	on = on && p.canStereo
	if p.stereo.Swap(on) != on {
		p.stale.Store(true)
	}
	return on
}

// SetASP turns the driver tuning on or off and reports what it settled on, which is off on a device
// whose tuning never loaded.
func (p *Player) SetASP(on bool) bool {
	on = on && len(p.chains) > 0
	if on && !p.on.Load() {
		p.stale.Store(true)
	}
	p.on.Store(on)
	return on
}

// SetTone hands the write loop the listener's own shelves. It is the tuning's own stage, so it does
// nothing on a device playing untuned — there is no chain to put it in.
func (p *Player) SetTone(t asp.Tone) { p.tone.Store(t) }

// Tone is what the shelves are set to.
func (p *Player) Tone() asp.Tone {
	t, _ := p.tone.Load().(asp.Tone)
	return t
}

// ASP reports whether the driver tuning is being applied.
func (p *Player) ASP() bool { return p.on.Load() }

// Resampling is the one in use.
func (p *Player) Resampling() config.Resampling {
	p.voiceMu.Lock()
	defer p.voiceMu.Unlock()
	return p.resampling
}

// Splices counts how many times the queue has run dry part way through a buffer.
func (p *Player) Splices() uint64 { return p.splices.Load() }

func (p *Player) Underruns() uint64 { return p.underruns.Load() }

// Clipped counts voice samples that came out of the resampler past full scale.
func (p *Player) Clipped() uint64 {
	p.voiceMu.Lock()
	defer p.voiceMu.Unlock()
	return p.voice.Clipped()
}

// Queued reports how many frames are waiting, so a caller can tell when playback has finished.
func (p *Player) Queued() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.pending) / Channels
}

// Note is one tone in a chime. A zero frequency is a rest.
type Note struct {
	Freq float64
	Ms   int

	// Clip is a recorded sound played in the note's place (clips.go); Ms is then its length.
	Clip *Clip
}

// Beep queues a tone.
func (p *Player) Beep(freq float64, ms int, level float64) {
	p.Chime(level, Note{Freq: freq, Ms: ms})
}

// Chime sounds notes back to back, each shaped by the same short envelope so the joins do not click.
//
// A tone is feedback, so it mixes into whatever is queued rather than waiting behind it: queued audio
// is a reply's cushion or, when the reply came as a file, all of it, and a beep that waits is a beep
// that arrives after the answer.
func (p *Player) Chime(level float64, notes ...Note) {
	var out []int16
	for _, n := range notes {
		out = append(out, tone(n, level)...)
	}
	p.Overlay(out)
}

// Overlay mixes samples into what is already queued, extending the queue if they outlast it. Sums are
// clamped: two things at once are louder than either, and wrapping would turn that into a crack.
func (p *Player) Overlay(samples []int16) {
	if pb, _ := p.device(); pb == nil {
		p.Play(samples)
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.pending = mix(p.pending, samples)
	p.overlaid(len(samples))
}

// overlaid notes that the first n samples of the queue now carry an overlay. Called with mu held.
func (p *Player) overlaid(n int) { p.overlay = max(p.overlay, n) }

// Overlaying reports whether a sound mixed in by Overlay is still queued. Something that keeps the queue
// short by draining it (a call that has fallen behind) drops its own audio instead while this is so, or
// the chime or the beep would go with it.
func (p *Player) Overlaying() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.overlay > 0
}

// mix sums add into the front of into, extending it if add outlasts it.
func mix(into, add []int16) []int16 {
	for i, s := range add {
		if i >= len(into) {
			return append(into, add[i:]...)
		}
		into[i] = clamp(int32(into[i]) + int32(s))
	}
	return into
}

// limit is a soft clipper for the boosted output: linear to kneeAt, then the remaining headroom is
// approached asymptotically, so a loud passage compresses rather than cracks. Below the knee it is
// exactly what clamp would have produced.
func limit(v float32) int16 {
	const (
		kneeAt   = 24576 // 0.75 of full scale
		headroom = full - kneeAt
	)
	a := v
	if a < 0 {
		a = -a
	}
	if a <= kneeAt {
		return int16(v)
	}
	over := a - kneeAt
	a = kneeAt + headroom*over/(over+headroom)
	if v < 0 {
		return int16(-a)
	}
	return int16(a)
}

func clamp(v int32) int16 {
	switch {
	case v > math.MaxInt16:
		return math.MaxInt16
	case v < math.MinInt16:
		return math.MinInt16
	}
	return int16(v)
}

func tone(n Note, level float64) []int16 {
	if n.Clip != nil {
		return n.Clip.render(level)
	}
	frames := Rate * n.Ms / 1000
	out := make([]int16, frames*Channels)

	for i := range frames {
		if n.Freq == 0 {
			continue
		}
		env := math.Min(1, math.Min(float64(i), float64(frames-i))/float64(Rate/200))
		s := int16(level * env * math.MaxInt16 * math.Sin(2*math.Pi*n.Freq*float64(i)/Rate))
		out[i*Channels] = s
		out[i*Channels+1] = s
	}
	return out
}

// SetVolume sets the playback level as a step on the vendor's curve for the current output.
func (p *Player) SetVolume(step int) {
	step = max(0, min(step, VolumeSteps))
	p.step.Store(int32(step))
	volumeChanged(step)
	if p.sink.Load() != nil {
		p.volume.Store(math.Float32bits(sinkGain(step)))
		return
	}
	p.volume.Store(math.Float32bits(gainForStep(p.Output(), step)))
}

// firstRatio is what turns a step's gain on the usual curve into its gain in front of the tuning.
func firstRatio(first *[VolumeSteps + 1]float64, out Output, step int) float32 {
	step = max(0, min(step, VolumeSteps))
	usual := gainForStep(out, step)
	if usual == 0 || first[step] <= mute {
		return 0
	}
	return float32(math.Pow(10, first[step]/20)) / usual
}

// ringBalance is how a ring and the media share the one gain after the tuning: the gain and step the
// output goes at, the louder of the two, and what each lane is scaled by in front of it. They are
// compared at the level they come out at, which with a curve in front of the tuning (first, nil when
// untuned) is the curve's.
func ringBalance(first *[VolumeSteps + 1]float64, out Output, gain float32, step int, bg float32, bs int) (mediaK, bellK, g float32, s int) {
	level := func(s int, g float32) float32 {
		if first != nil {
			return g * firstRatio(first, out, s)
		}
		return g
	}
	lb, lm := level(bs, bg), level(step, gain)
	if lb > lm {
		return lm / lb, 1, bg, bs
	}
	if lm > 0 {
		return 1, lb / lm, gain, step
	}
	return 1, 0, gain, step
}

// gainFor is the linear gain for a step on whatever the audio is going to now.
func (p *Player) gainFor(step int) float32 {
	step = max(0, min(step, VolumeSteps))
	if p.sink.Load() != nil {
		return sinkGain(step)
	}
	return gainForStep(p.Output(), step)
}

// Bell mixes a ring's chime into the bell's own queue, to go out at step rather than at the media
// volume. Dropped with no device, as Play drops.
func (p *Player) Bell(step int, level float64, notes ...Note) {
	if pb, _ := p.device(); pb == nil {
		return
	}
	var out []int16
	for _, n := range notes {
		out = append(out, tone(n, level)...)
	}
	p.bellStep.Store(int32(max(0, min(step, VolumeSteps))))
	p.mu.Lock()
	p.bell = mix(p.bell, out)
	p.mu.Unlock()
}

// Ringing holds the bell's level for as long as a ring lasts, and ending it drops any chime still
// queued, so a ring stopped mid-chime goes quiet at once.
func (p *Player) Ringing(on bool, step int) {
	p.bellStep.Store(int32(max(0, min(step, VolumeSteps))))
	p.ringing.Store(on)
	if !on {
		p.mu.Lock()
		p.bell = nil
		p.mu.Unlock()
	}
}

// Step is the current volume step.
func (p *Player) Step() int { return int(p.step.Load()) }

// Volume is the current linear gain.
func (p *Player) Volume() float32 { return math.Float32frombits(p.volume.Load()) }

// Close puts the mixer back as it starts (initializeMixer: on Amazon's kernel that mutes the codec and
// turns the amplifier off; on the mainline card it sets the volume to unity) and lets the device go.
// The Player stays usable:
// Start can take it again, which is how a restart works.
func (p *Player) Close() error {
	p.initializeMixer()

	p.devMu.Lock()
	pb, mixer, hold := p.pb, p.mixer, p.hold
	p.pb, p.mixer, p.hold = nil, nil, nil
	p.devMu.Unlock()
	p.ring.Store(nil) // the next device says afresh

	if mixer != nil {
		_ = mixer.Close()
	}
	if pb == nil {
		if hold != nil {
			_ = hold.Close()
		}
		return nil
	}
	err := pb.Close()
	// After the playback device: the driver reads the AFE state at open, not at close, but there is
	// no reason to let go of it first.
	if hold != nil {
		_ = hold.Close()
	}
	return err
}

// Bed is the Player's queue for a track, apart from the one replies and announcements use, so the two
// sound together. It has the queue's own methods, and a track is written as if it had the speaker to
// itself: Queued counts only the track, and draining one leaves the other alone.
type Bed struct{ p *Player }

// Bed is the track's queue.
func (p *Player) Bed() *Bed { return &Bed{p: p} }

// Play queues interleaved stereo samples, dropped as Player.Play drops them with no device.
func (b *Bed) Play(samples []int16) {
	p := b.p
	if pb, _ := p.device(); pb == nil {
		if n := p.deaf.Add(1); n == 1 || n%100 == 0 {
			slog.Warn("audio dropped, no playback device", "times", n)
		}
		return
	}
	p.mu.Lock()
	p.bed = append(p.bed, samples...)
	p.mu.Unlock()
}

// Take empties the track's queue and hands back what had not been played.
func (b *Bed) Take() []int16 {
	b.p.mu.Lock()
	defer b.p.mu.Unlock()
	bed := b.p.bed
	b.p.bed = nil
	return bed
}

// Adjust rewrites the track's queue in place.
func (b *Bed) Adjust(rewrite func([]int16)) {
	b.p.mu.Lock()
	defer b.p.mu.Unlock()
	rewrite(b.p.bed)
}

// Queued is how many frames of the track are waiting.
func (b *Bed) Queued() int {
	b.p.mu.Lock()
	defer b.p.mu.Unlock()
	return len(b.p.bed) / Channels
}

// Drain discards the track's queue.
func (b *Bed) Drain() {
	b.p.mu.Lock()
	b.p.bed = nil
	b.p.mu.Unlock()
}

// Underlay queues interleaved stereo for the underlay lane (under), dropped as Player.Play drops audio
// with no device.
func (p *Player) Underlay(samples []int16) {
	if pb, _ := p.device(); pb == nil {
		if n := p.deaf.Add(1); n == 1 || n%100 == 0 {
			slog.Warn("audio dropped, no playback device", "times", n)
		}
		return
	}
	p.mu.Lock()
	p.under = append(p.under, samples...)
	p.mu.Unlock()
}

// UnderQueued is how many frames of the underlay are waiting.
func (p *Player) UnderQueued() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.under) / Channels
}

// DrainUnder discards the underlay: the call has the speaker back, and plays its far end as itself.
func (p *Player) DrainUnder() {
	p.mu.Lock()
	p.under = nil
	p.mu.Unlock()
}

// initializeMixer keeps vendor amplifier controls off the mainline card.
func (p *Player) initializeMixer() {
	if p.mainline {
		// TAS5805M index 110 is unity; the existing software volume curve controls loudness.
		p.apply([]kctl{{name: "Master Playback Volume", level: 110}})
		return
	}
	p.apply(initSequence)
}
