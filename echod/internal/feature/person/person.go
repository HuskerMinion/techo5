//go:build !dot

// Package person tells a person from anything else that moves: a curtain in a draft, a pet, the light
// changing. While presence detection (feature/presence) has seen movement, a small model looks at the
// camera's picture about twice a second, and a Person sensor says whether it saw somebody.
//
// Off until it is turned on, and only with presence detection on: it reads the frames presence already
// has the camera make, and starts no camera of its own. Nothing the camera sees is kept or sent; the
// picture is read as 96 by 96 shades of gray, run through the model, and let go. The model is its own,
// on its own goroutine and buffers, apart from the wake word engine's.
//
// The model is TensorFlow's person detection example (MobileNet v1, int8, 96x96 grayscale): small
// enough for these processors, and a modest judge. A person far from the camera, or half out of the
// picture, is often missed, which is why the sensor holds a sighting for a while. On the 2nd gen
// Show 8 it runs on the VPU (vpu.go), and on the cores wherever there is none or it fails.
package person

import (
	"context"
	_ "embed"
	"errors"
	"log/slog"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/presence"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/camera"
	"github.com/HuskerMinion/techo5/echod/internal/lib/tflite"
)

//go:embed assets/person_detect.tflite
var model []byte

func init() {
	if camera.Available() {
		component.Register(component.Device, Get(), component.Order(84))
	}
}

const (
	// side is the model's picture, square and gray.
	side = 96

	// every is how often a frame is looked at while somebody is near.
	every = 500 * time.Millisecond
	// everySlack is how much sooner than every a frame may come and still be looked at.
	everySlack = 50 * time.Millisecond

	// hold is how long a sighting stands: the model misses a person often enough that one look without
	// them is no news.
	hold = 60 * time.Second
)

type Feature struct {
	sw     *esphome.Switch
	sensor *esphome.BinarySensor

	wake chan struct{}

	// score runs the model on a picture: the chance it shows a person. Replaced in tests.
	score func(gray []uint8) (float32, error)
	// idle is told the time every second while looking, and lets the VPU go when it has not been
	// used for a while. Replaced in tests.
	idle func(now time.Time)

	mu    sync.Mutex
	judge judge

	// failedAt is when the model last failed to load, so it is not tried every few seconds; only Run's.
	failedAt time.Time
}

var (
	once   sync.Once
	shared *Feature
)

func Get() *Feature {
	once.Do(func() {
		f := &Feature{wake: make(chan struct{}, 1)}
		f.sw = &esphome.Switch{
			Base: esphome.Base{ObjectID: "person_detection", Name: "Person detection", Icon: "mdi:account-search",
				Category: esphome.CategoryConfig},
			OnCommand: f.SetOn,
		}
		f.sensor = &esphome.BinarySensor{
			Base:        esphome.Base{ObjectID: "person", Name: "Person", Icon: "mdi:account"},
			DeviceClass: "occupancy",
		}
		shared = f
	})
	return shared
}

func (f *Feature) Name() string { return "person" }

func (f *Feature) Entities() []esphome.Entity { return []esphome.Entity{f.sw, f.sensor} }

func (f *Feature) Restore(c config.Config) {
	f.sw.Set(c.Presence.Person)
	f.sensor.Set(false)
}

// SetOn is the switch.
func (f *Feature) SetOn(on bool) {
	if err := config.Set().Presence().Person(on); err != nil {
		slog.Error("saving a setting failed", "setting", "person detection", "err", err)
		f.sw.Set(!on)
		return
	}
	f.sw.Set(on)
	slog.Info("setting changed", "setting", "person detection", "using", on)
	select {
	case f.wake <- struct{}{}:
	default:
	}
}

// wanted is whether to look: the switch on, and presence detection with it.
func wanted() bool {
	p := config.Get().Presence
	return p.Person && p.On && presence.On()
}

// Run looks while the switch is on, and loads the model only then.
func (f *Feature) Run(ctx context.Context) error {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		if wanted() {
			f.look(ctx)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-f.wake:
		case <-tick.C: // presence turned on or off without a word to this package
		}
	}
}

// loadAgain is how long after the model failed to load it is tried again.
const loadAgain = time.Hour

// look runs the model on the camera's frames while it is wanted, and lets the model go after.
func (f *Feature) look(ctx context.Context) {
	score, idle := f.score, f.idle
	if score == nil {
		if !f.failedAt.IsZero() && time.Since(f.failedAt) < loadAgain {
			return // said once, and tried again later rather than every few seconds
		}
		m, err := load()
		if err != nil {
			f.failedAt = time.Now()
			slog.Error("person detection: the model could not be loaded; tried again in an hour", "err", err)
			return
		}
		f.failedAt = time.Time{}
		defer m.close()
		score, idle = m.score, m.idle
	}
	frames := make(chan *camera.Frame, 1)
	stop := camera.Get().Frames.Listen(func(fr *camera.Frame) {
		select {
		case frames <- fr:
		default: // still looking at the last one
		}
	})
	defer stop()
	slog.Info("person detection: looking")
	defer func() {
		f.mu.Lock()
		f.judge = judge{}
		f.mu.Unlock()
		f.sensor.Set(false)
		slog.Info("person detection: not looking")
	}()

	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	var last time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-f.wake:
			if !wanted() {
				return
			}
		case now := <-tick.C:
			if !wanted() {
				return
			}
			f.update(now)
			if idle != nil {
				idle(now)
			}
		case fr := <-frames:
			now := time.Now()
			// A frame a little early is taken: frames come about every so often, and one just short
			// of every skipped would make it a look every other frame.
			if now.Sub(last) < every-everySlack || !presence.Present() {
				continue
			}
			last = now
			gray := fr.Luma(side, side)
			if gray == nil {
				continue // held off by the mute
			}
			p, err := score(gray)
			if err != nil {
				slog.Warn("person detection: the model failed", "err", err)
				continue
			}
			f.mu.Lock()
			f.judge.add(p, now)
			f.mu.Unlock()
			f.update(now)
		}
	}
}

// update sets the sensor from the judge.
func (f *Feature) update(now time.Time) {
	f.mu.Lock()
	seen := f.judge.seen(now)
	f.mu.Unlock()
	if f.sensor.Get() != seen {
		slog.Info("person detection", "person", seen)
	}
	f.sensor.Set(seen)
}

// judge turns the model's scores into a sighting: the mean of the last few looks over threshold, held
// for hold.
type judge struct {
	recent []float32
	until  time.Time
}

const (
	looks     = 3
	threshold = 0.6
)

func (j *judge) add(p float32, now time.Time) {
	j.recent = append(j.recent, p)
	if len(j.recent) > looks {
		j.recent = j.recent[len(j.recent)-looks:]
	}
	if len(j.recent) < looks {
		return
	}
	var sum float32
	for _, v := range j.recent {
		sum += v
	}
	if sum/float32(len(j.recent)) >= threshold {
		j.until = now.Add(hold)
	}
}

func (j *judge) seen(now time.Time) bool { return now.Before(j.until) }

var errSize = errors.New("the picture or the model's answer is not the size expected")

// detector is the model, loaded, with the buffers it runs in.
type detector struct {
	in *tflite.Interpreter
	// vpu is the model on the VPU, nil where there is none or once it failed.
	vpu *accel
}

func load() (*detector, error) {
	m, err := tflite.Parse(model)
	if err != nil {
		return nil, err
	}
	// The picture goes in as the int8 the model was quantized for (score): a model that takes anything
	// else is not this one.
	if g := m.Subgraphs; len(g) == 0 || len(g[0].Inputs) == 0 || g[0].Tensors[g[0].Inputs[0]].Type != tflite.Int8 {
		return nil, errors.New("person detection: the model does not take an int8 picture")
	}
	in, err := tflite.New(m)
	if err != nil {
		return nil, err
	}
	return &detector{in: in, vpu: newAccel(m, in)}, nil
}

// score is the chance a 96x96 gray picture shows a person: on the VPU if it can, else on the
// cores. The model takes each pixel as it was quantized for: the byte less 128, at the input's scale.
func (d *detector) score(gray []uint8) (float32, error) {
	x := d.in.Input(0)
	if len(gray) != len(x.F32) {
		return 0, errSize
	}
	if d.vpu != nil {
		if p, ok := d.vpu.score(gray, time.Now()); ok {
			return p, nil
		}
		d.vpu = nil // failed, said, and not tried again
	}
	for i, p := range gray {
		x.F32[i] = float32(int64(p)-128-x.ZeroPoint) * x.Scale
	}
	if err := d.in.Invoke(); err != nil {
		return 0, err
	}
	out := d.in.Output(0).F32
	if len(out) != 2 {
		return 0, errSize
	}
	return out[1], nil // no person, person
}

// idle lets the VPU go when it has not been used for a while.
func (d *detector) idle(now time.Time) {
	if d.vpu != nil {
		d.vpu.idle(now)
	}
}

// close lets the VPU go.
func (d *detector) close() {
	if d.vpu != nil {
		d.vpu.close()
	}
}
