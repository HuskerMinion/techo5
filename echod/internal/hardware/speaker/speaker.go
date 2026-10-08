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
	"github.com/HuskerMinion/techo5/echod/internal/lib/alsa"
	"github.com/HuskerMinion/techo5/echod/internal/lib/asp"
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
)

// VoiceRate is the rate Home Assistant's pipeline works at, and VoiceUpsample how many playback
// samples each of its samples becomes.
const (
	VoiceRate     = 16000
	VoiceUpsample = Rate / VoiceRate
)

const (
	Card           = 0
	PlaybackDevice = 23
)

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
	pb    *alsa.Playback
	mixer *alsa.Mixer

	// hold is DRAMHold, kept open for as long as pb is: see paths_cronos.go.
	hold *os.File

	// Jack changed: a headphone was physically plugged in or pulled out.
	OnJack hook.Hook[Output]

	// sink, when set, is where the audio goes instead of the codec: see sink.go.
	sink atomic.Pointer[sinkState]

	volume atomic.Uint32 // linear gain, derived from step and the current output's curve
	step   atomic.Int32

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

	// tuning is the vendor's driver tuning, loaded once, and chain is it applied to this stream. Both
	// are nil on a device whose tuning we could not read, which plays untuned rather than not at all.
	// on gates it; the chain is only ever touched by the write loop.
	tuning *asp.Tuning
	chain  *asp.Chain
	// first is the volume curve to apply in front of the tuning, where this tuning has one
	// (firstCurves); nil turns down what the tuning produced instead.
	first *[VolumeSteps + 1]float64
	on    atomic.Bool
	stale atomic.Bool
	mono  []float32

	// tone is the listener's own shelves, waiting to be picked up: the chain belongs to the write
	// loop, so a setting changed from anywhere else is left here and applied there. applied is what
	// the chain is carrying now, and is the write loop's alone.
	tone    atomic.Value // asp.Tone
	applied asp.Tone

	// fed is whether the last block had anything in it, and belongs to the write loop alone.
	fed bool

	// underruns is the card running out while we were away. Write blocks against the whole ring, so
	// each one means the loop was starved for as long as the ring is deep.
	underruns atomic.Uint64

	// deaf counts what was thrown away for want of a device.
	deaf atomic.Uint64

	mu      sync.Mutex
	pending []int16 // interleaved stereo waiting to go out

	// bed is a track's audio, queued apart from pending so an answer or an announcement can sound
	// over it rather than after it: the two are summed as they go out, as a Source is. Same stereo,
	// same media volume; a track ducks its own samples before they are queued.
	bed []int16

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
// (16 ms on a Show 5, 21 on a Dot), so the time since it was taken comes off: without that, the anchor
// sat up to a period early.
//
// A full ring is 3072 frames (64 ms) on a Show 5 and 4096 (about 85 ms) on a Dot, and just after a
// write the card holds close to all of it. Measured on a 1st gen Show 5 (2026-10-02, idle loop feeding
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

// New makes the speaker without taking the hardware, so callers can hold it before there is anything
// to play through. Audio queued before Start waits; Volume and the rest work throughout.
func New() *Player {
	detected := DetectOutput()
	p := &Player{
		out:  detected,
		jack: detected,
	}
	p.outNow.Store(detected)
	p.voice, p.resampling = NewResampler(config.ResampleSinc)
	p.SetVolume(VolumeSteps)
	p.on.Store(config.DefaultASP)

	if !DriverTuning {
		return p
	}
	t, err := asp.Load(asp.VendorDir)
	if err != nil {
		slog.Error("the driver tuning is not available, playing untuned", "err", err)
		return p
	}
	c, err := t.Chain(period)
	if err != nil {
		slog.Error("the driver tuning will not run, playing untuned", "err", err)
		return p
	}
	p.tuning, p.chain, p.mono = t, c, make([]float32, period)
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
	// DAC and toggling it pops.
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
	m, err := alsa.OpenMixer(Card)
	if err != nil {
		return fmt.Errorf("speaker: opening mixer: %w", err)
	}

	// Before the playback device, so its driver sees the AFE in use and takes the DRAM ring.
	var hold *os.File
	if DRAMHold != "" {
		hold, err = os.OpenFile(DRAMHold, os.O_RDWR|syscall.O_NONBLOCK, 0)
		if err != nil {
			_ = m.Close()
			return fmt.Errorf("speaker: holding %s: %w", DRAMHold, err)
		}
	}

	pb, err := alsa.OpenPlayback(Card, PlaybackDevice, alsa.Config{
		Channels:   Channels,
		Rate:       Rate,
		Format:     alsa.FormatS16_LE,
		Bits:       Bits,
		PeriodSize: period,
		Periods:    periods,
	})
	if err != nil {
		_ = m.Close()
		if hold != nil {
			_ = hold.Close()
		}
		return fmt.Errorf("speaker: opening playback: %w", err)
	}

	p.devMu.Lock()
	p.pb, p.mixer, p.hold = pb, m, hold
	p.devMu.Unlock()
	return nil
}

// device is the hardware, or nils when it is not held.
func (p *Player) device() (*alsa.Playback, *alsa.Mixer) {
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
	p.apply(pathSequence[p.out])
}

// amp switches the speaker amplifier, where the device has a switch that is safe to touch: see
// AmpSwitch in paths_dot.go and paths_cronos.go.
func (p *Player) amp(on bool) {
	if AmpSwitch == "" {
		return
	}

	p.pathMu.Lock()
	defer p.pathMu.Unlock()

	if p.out != OutputSpeaker {
		return
	}
	value := "Off"
	if on {
		value = "On"
	}
	p.apply([]kctl{{name: AmpSwitch, value: value}})
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
	if AmpSwitch != "" {
		p.apply([]kctl{{name: AmpSwitch, value: "Off"}})
	}
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
	p.apply(pathSequence[out])
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
func (p *Player) apply(seq []kctl) {
	_, mixer := p.device()
	if mixer == nil {
		return
	}

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
		}
	}
}

// Run feeds the stream until ctx is canceled, writing silence when nothing is queued.
func (p *Player) Run(ctx context.Context) error {
	pb, _ := p.device()
	if pb == nil {
		return errors.New("speaker: the playback device is not held")
	}
	buf := make([]byte, period*Channels*Bits/8)

	// The order the vendor HAL uses on a route change: route with the amplifier off, let the
	// codec sit idle, enable the amplifier, then feed.
	out := p.Output()
	p.apply(initSequence)
	p.route()

	select {
	case <-ctx.Done():
		return nil
	case <-time.After(codecSettle):
	}

	p.amp(true)
	slog.Info("playback path up", "output", out)
	safe.Go("jack watcher", func() { p.watchJack(ctx) })

	silence := make([]byte, len(buf))
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}

		p.fill(buf)
		spectrum.Speaker.PushStereo(buf)
		to := buf
		if s := p.sink.Load(); s != nil {
			// The sink gets the audio; the codec keeps its pace on silence.
			s.push(buf)
			to = silence
		}
		if err := p.send(ctx, pb, to); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		// What the card holds now is what the period rendered next waits behind, so it is read here,
		// between the write and the render, and published with the count it belongs to.
		d, err := pb.Delay()
		at := time.Now()
		w := p.written.Add(period)
		if err == nil && d >= 0 {
			p.ring.Store(&ringReading{written: w, frames: int64(d), at: at})
		} else {
			p.ring.Store(nil)
		}
	}
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
	bedTake := min(len(p.bed), period*Channels)
	bed := p.bed[:bedTake]
	p.bed = p.bed[bedTake:]
	if len(p.bed) == 0 {
		p.bed = nil
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

	// The internal speaker is one driver wired to the right channel, so a stereo track would lose
	// everything panned left. The line-out is not: it gets both channels as they came. Neither is a
	// sink, which is stereo and untuned: it has its own driver and its own tuning.
	sinking := p.sink.Load() != nil
	out := p.Output()
	mono := (out == OutputSpeaker || out == OutputBoth) && !sinking

	// The write loop runs whether or not anything is playing, since the amplifier hisses when nothing
	// drives the DAC, and tuning silence costs what tuning music costs. The block after the audio stops
	// still goes through: that is the filter's own length, and it holds the tail.
	fed := take > 0 || bedTake > 0 || len(rendered) > 0 || rang > 0
	drain := fed || p.fed
	p.fed = fed

	// The tuning is for the driver, so the line-out is left with what it was sent. Both is the
	// exception: one mixer feeds the speaker and the jack, so the headphones get the speaker's mono
	// and its tuning too.
	tuned := mono && p.chain != nil && p.on.Load() && drain

	// There is one gain after the tuning, so two levels are made from it: the output goes at the
	// louder of the two, and whichever lane is quieter is scaled down by the difference before the
	// mix. The music comes out at the media volume either way, and a muted one stays muted under
	// the alarm. On the tuned path the compressor narrows that difference a little, since it works
	// on the sum; the lane that sets the output level is exact.
	gain, step := p.Volume(), int(p.step.Load())
	mediaK, bellK := float32(1), float32(0)
	if rang > 0 || p.ringing.Load() {
		bs := int(p.bellStep.Load())
		bg := p.gainFor(bs)
		if bg > gain {
			mediaK, bellK, gain, step = gain/bg, 1, bg, bs
		} else if gain > 0 {
			bellK = bg / gain
		}
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
		p.chain.Volume(float64(step) / VolumeSteps)
		if want, ok := p.tone.Load().(asp.Tone); ok && want != p.applied {
			p.chain.SetTone(want)
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
		if mono {
			l = (l + r) / 2
			r = l
		}
		if tuned {
			p.mono[j] = float32(clamp(l)) / full * pre
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
	if p.stale.Swap(false) {
		p.chain.Reset()
	}

	// Without a curve of its own, the volume attenuates what the tuning produced: the compressor then
	// works on full-scale music at every volume, which is what firstCurves is for.
	p.chain.Process(p.mono)
	for i, j := 0, 0; i < period*Channels; i, j = i+Channels, j+1 {
		s := int16(p.mono[j] * full * post)
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(s))
		binary.LittleEndian.PutUint16(buf[(i+1)*2:], uint16(s))
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

// SetASP turns the driver tuning on or off and reports what it settled on, which is off on a device
// whose tuning never loaded.
func (p *Player) SetASP(on bool) bool {
	on = on && p.chain != nil
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

// Close mutes the codec, turns the amplifier off and closes the stream.
// Close mutes the codec, turns the amplifier off and lets the device go. The Player stays usable:
// Start can take it again, which is how a restart works.
func (p *Player) Close() error {
	p.apply(initSequence)

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
