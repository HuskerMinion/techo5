//go:build dot && cannoli

package ultrasound

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/mic"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

func init() {
	component.Register(component.Device, Get(), component.Order(83))
}

// candidates are the tones probe tries, every one a whole number of cycles in a short loop at 96 kHz.
// Above 26 kHz, where nobody hears it and the music's own upsampling leaves nothing; under 40, where
// the speaker and the microphones are still some use.
var candidates = []int{28000, 30000, 32000, 34000, 36000, 38000, 40000}

const (
	// settle is how long a new tone, or a tone at a new level, is given before what is heard counts:
	// the fade, and the analyzer's still part finding its way (dcCo), which would otherwise read as
	// movement.
	settle = 2500 * time.Millisecond

	// listenFor is how long probe listens to each candidate once it has settled.
	listenFor = 600 * time.Millisecond

	// reprobe is how soon the tones are tried again after none could be heard, or after the one in
	// use has not been heard for deafFor.
	reprobe = 5 * time.Minute
	deafFor = time.Minute
)

// heard is one reading, for the tone it was taken with; or the microphones cut, which is no reading.
type heard struct {
	hz  int
	r   reading
	cut bool
}

type Feature struct {
	sw      *esphome.Switch
	sensNum *esphome.Number
	sensor  *esphome.BinarySensor

	wake chan struct{}

	// hz is the tone the capture side listens for (the tap's), 0 for none.
	hz atomic.Int32
	in chan heard

	mu  sync.Mutex
	det detector
}

var (
	once   sync.Once
	shared *Feature
)

func Get() *Feature {
	once.Do(func() {
		f := &Feature{wake: make(chan struct{}, 1), in: make(chan heard, 16)}
		// The camera's entities' IDs, so an automation written for one Echo is written for this one.
		f.sw = &esphome.Switch{
			Base:      esphome.Base{ObjectID: "presence_detection", Name: "Presence detection", Icon: "mdi:motion-sensor", Category: esphome.CategoryConfig},
			OnCommand: f.SetOn,
		}
		f.sensNum = &esphome.Number{
			Base: esphome.Base{ObjectID: "presence_sensitivity", Name: "Presence sensitivity",
				Icon: "mdi:tune-vertical", Category: esphome.CategoryConfig},
			Min: 1, Max: 100, Step: 1,
			Mode: esphome.NumberSlider,
		}
		f.sensNum.OnCommand = func(v float32) { f.SetSensitivity(int(v)) }
		f.sensor = &esphome.BinarySensor{
			Base:        esphome.Base{ObjectID: "presence", Name: "Motion", Icon: "mdi:motion-sensor"},
			DeviceClass: "motion",
		}
		shared = f
	})
	return shared
}

func (f *Feature) Name() string { return "ultrasound" }

func (f *Feature) Entities() []esphome.Entity {
	return []esphome.Entity{f.sw, f.sensNum, f.sensor}
}

func (f *Feature) Restore(c config.Config) {
	f.sw.Set(c.Presence.On)
	f.sensNum.Set(float32(c.Presence.PresenceSensitivity()))
	f.sensor.Set(false)
	f.mu.Lock()
	f.det.threshold = thresholdFor(c.Presence.PresenceSensitivity())
	f.mu.Unlock()
}

// SetOn is the switch.
func (f *Feature) SetOn(on bool) {
	if err := config.Set().Presence().On(on); err != nil {
		slog.Error("saving a setting failed", "setting", "presence", "err", err)
		f.sw.Set(!on)
		return
	}
	f.sw.Set(on)
	slog.Info("setting changed", "setting", "presence", "using", on)
	f.poke()
}

// SetSensitivity is 1 to 100: higher takes smaller movements, and ones further off.
func (f *Feature) SetSensitivity(v int) {
	v = min(max(v, 1), 100)
	if err := config.Set().Presence().Sensitivity(v); err != nil {
		slog.Error("saving a setting failed", "setting", "presence sensitivity", "err", err)
		return
	}
	f.sensNum.Set(float32(v))
	f.mu.Lock()
	f.det.threshold = thresholdFor(v)
	f.mu.Unlock()
}

func (f *Feature) poke() {
	select {
	case f.wake <- struct{}{}:
	default:
	}
}

// Run plays the tone and listens while the switch is on.
func (f *Feature) Run(ctx context.Context) error {
	for {
		if config.Get().Presence.On {
			f.watch(ctx)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-f.wake:
		}
	}
}

// tap is the capture side: from the capture loop, the raw 96 kHz microphones into readings for the
// tone asked for. Its listener is the capture loop's alone.
func (f *Feature) tap() func() {
	var l *listener
	send := func(h heard) {
		select {
		case f.in <- h:
		default: // the watch is busy: a reading is 64 ms, and the next one does as well
		}
	}
	return mic.TapRaw(func(b mic.RawBlock) {
		hz := int(f.hz.Load())
		if b.Cut {
			l = nil
			send(heard{hz: hz, cut: true})
			return
		}
		if hz == 0 {
			l = nil
			return
		}
		if l == nil || l.hz != hz || l.rate != b.Rate || len(l.mics) != b.Mics {
			if l = newListener(hz, b.Rate, b.Mics); l == nil {
				return
			}
		}
		l.feed(b.PCM, b.Channels, b.Width, b.Full, func(r reading) { send(heard{hz: hz, r: r}) })
	})
}

// use sets the tone on both sides: the speaker's and the one listened for.
func (f *Feature) use(hz int) {
	speaker.Get().SetUltrasound(hz)
	f.hz.Store(int32(hz))
}

// watch runs until the switch goes off or ctx ends, and leaves the tone off and Motion clear.
func (f *Feature) watch(ctx context.Context) {
	stop := f.tap()
	defer func() {
		stop()
		f.use(0)
		f.sensor.Set(false)
		f.mu.Lock()
		f.det.reset()
		f.mu.Unlock()
		slog.Info("ultrasound presence detection off")
	}()
	on := func() bool { return ctx.Err() == nil && config.Get().Presence.On }

	for on() {
		hz := f.probe(ctx, on)
		if hz == 0 {
			slog.Warn("ultrasound: no tone was heard back; trying again later", "after", reprobe)
			if !f.pause(ctx, reprobe, on) {
				return
			}
			continue
		}
		f.detect(ctx, hz, on)
	}
}

// pause waits d, or less if the switch changes; false when it is time to stop.
func (f *Feature) pause(ctx context.Context, d time.Duration, on func() bool) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-f.wake:
	case <-t.C:
	}
	return on()
}

// probe plays each candidate in turn and keeps the one the microphones hear best: 0 if none is heard
// at all (minCarrier).
func (f *Feature) probe(ctx context.Context, on func() bool) int {
	best, bestDB := 0, minCarrier
	var got []string
	for _, hz := range candidates {
		if !on() {
			return 0
		}
		db, ok := f.listen(ctx, hz, on)
		if !ok {
			continue
		}
		got = append(got, fmt.Sprintf("%d Hz %.0f dB", hz, db))
		if db > bestDB {
			best, bestDB = hz, db
		}
	}
	slog.Info("ultrasound: tones heard", "carrier_db", got, "using_hz", best)
	return best
}

// listen plays hz and reports how well it is heard once settled: the median of what listenFor gives.
func (f *Feature) listen(ctx context.Context, hz int, on func() bool) (float64, bool) {
	f.use(hz)
	from := time.Now().Add(settle)
	until := from.Add(listenFor)
	var dbs []float64
	t := time.NewTimer(time.Until(until) + time.Second)
	defer t.Stop()
	for time.Now().Before(until) || len(dbs) == 0 {
		select {
		case <-ctx.Done():
			return 0, false
		case <-f.wake:
			if !on() {
				return 0, false
			}
		case <-t.C:
			return 0, false // nothing came: the capture is not running
		case h := <-f.in:
			if h.cut || h.hz != hz || time.Now().Before(from) {
				continue
			}
			dbs = append(dbs, h.r.carrier)
		}
	}
	slices.Sort(dbs)
	return dbs[len(dbs)/2], true
}

// detect follows the room on hz until the switch goes off, or the tone has not been heard for
// deafFor, when it is time to probe again.
func (f *Feature) detect(ctx context.Context, hz int, on func() bool) {
	f.use(hz)
	p := speaker.Get()
	step := p.Step()
	quietUntil := time.Now().Add(settle)
	heardAt := time.Now()
	shown := false
	show := func(m bool) {
		if m != shown {
			shown = m
			f.sensor.Set(m)
			slog.Debug("ultrasound: motion", "on", m)
		}
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	slog.Info("ultrasound presence detection on", "hz", hz)
	for {
		select {
		case <-ctx.Done():
			return
		case <-f.wake:
			if !on() {
				return
			}
		case now := <-tick.C:
			// Motion lets go on its own once the hold is over, readings or none (the mute).
			f.mu.Lock()
			m := f.det.motion(now)
			f.mu.Unlock()
			show(m)
			if now.Sub(heardAt) > deafFor {
				slog.Warn("ultrasound: the tone is no longer heard; trying the tones again", "hz", hz)
				return
			}
		case h := <-f.in:
			now := time.Now()
			// The tone follows the volume (the limiters' headroom), and a step in its level reads as
			// movement until the analyzer has followed it: those readings are let go.
			if s := p.Step(); s != step {
				step, quietUntil = s, now.Add(settle)
			}
			if h.cut {
				heardAt, quietUntil = now, now.Add(settle)
				continue
			}
			if h.hz != hz || now.Before(quietUntil) {
				continue
			}
			if h.r.carrier >= minCarrier {
				heardAt = now
			}
			f.mu.Lock()
			m := f.det.take(h.r, now)
			f.mu.Unlock()
			show(m)
		}
	}
}
