// Package babymon is the baby monitor: the room's sound, live, in a browser on the home network.
//
// It is a new use of the microphones, so it has a switch of its own, Baby monitor, under Privacy &
// Security on the screen and in Home Assistant (feature/security), off on a new device, read-only
// through the remote API, and never brought in by a settings file or settings sharing.
//
// While the switch is on, the device's web port serves a page at /baby and the audio at /baby/audio, to
// a browser that has the link: its address with a long random key in it, shown on the setup page. The
// setup page's own session is not used, since that closes after an hour and a monitor runs all night.
// With the switch off, the pages are not there, and the port is not open for them.
//
// The audio is what the wake word hears (mic.Listen: mixed, echo-cancelled and leveled), 16 kHz mono,
// sent as 16-bit samples in a WebSocket and played by the page with the Web Audio API, which works in
// Safari without a plugin. It is one more listener on the microphones, so voice keeps working.
//
// Anyone listening is shown: a Show or a Spot draws a mark on the screen, a Dot pulses its ring, and Home
// Assistant gets a Listening now sensor. The mute stops the audio, and a new listener is refused while the
// microphones are muted.
package babymon

import (
	"crypto/subtle"
	"errors"
	"log/slog"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/mute"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/mic"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hook"
	"github.com/HuskerMinion/techo5/echod/internal/lib/micsilent"
)

func init() {
	component.Register(component.Device, Get(), component.Order(84))
}

const (
	// Max is how many browsers may listen at once.
	Max = 3

	// batch is how many microphone frames (20 ms each) go in one message: 100 ms, so the delay it adds
	// is a tenth of a second, in few enough messages for a phone on weak Wi-Fi.
	batch = 5

	// check is how often a listener looks at the switch and the mute when no sound is arriving.
	check = 250 * time.Millisecond
)

// What stops or refuses a listener. The messages are shown on the page.
var (
	ErrOff    = errors.New("The baby monitor is off")
	ErrMuted  = errors.New("The microphones are muted")
	ErrFull   = errors.New("Too many are listening already")
	ErrNewKey = errors.New("The link was replaced")

	errUnknownLight = errors.New("Not a Baby monitor light choice")
)

// device is everything the monitor asks of the rest of the device, so a test can stand in for it.
type device struct {
	listen     func(name string) (<-chan []int16, func())
	switchedOn func() bool
	muted      func() bool
	key        func() string
}

var thisDevice = device{
	listen:     func(name string) (<-chan []int16, func()) { return mic.Get().Listen(name) },
	switchedOn: func() bool { return layout.HasMicrophone() && config.Get().Security.Baby },
	muted: func() bool {
		m, err := mute.Get().Muted()
		return m || err != nil || micsilent.Cut() || micsilent.Silent()
	},
	key: func() string { return config.Get().Security.BabyKey },
}

// Feature is the monitor: who is listening, and the sensor that says so.
type Feature struct {
	dev    device
	sensor *esphome.BinarySensor
	light  *esphome.Select // Baby monitor light (light.go)

	mu sync.Mutex
	n  int

	// Changed fires when somebody starts or stops listening, and when the light choice changes.
	// Listeners must not block.
	Changed hook.Hook[struct{}]
}

var (
	once   sync.Once
	shared *Feature
)

func Get() *Feature {
	once.Do(func() {
		shared = build(thisDevice)
		shared.handle()
	})
	return shared
}

func build(d device) *Feature {
	f := &Feature{dev: d, sensor: &esphome.BinarySensor{
		Base: esphome.Base{ObjectID: "baby_monitor_listening", Name: "Baby monitor listening now", Icon: "mdi:ear-hearing"},
	}}
	f.light = newLightSelect(func(v string) {
		if err := f.SetLight(v); err != nil {
			slog.Error("baby monitor light", "err", err)
		}
	})
	return f
}

func (f *Feature) Name() string { return "baby monitor" }

func (f *Feature) Entities() []esphome.Entity {
	if !layout.HasMicrophone() {
		return nil
	}
	return []esphome.Entity{f.sensor, f.light}
}

func (f *Feature) Restore(c config.Config) {
	f.sensor.Set(false)
	f.light.Set(LightName(c.Security.BabyLight))
}

// Listening is whether anybody is listening now, for the screen and the ring.
func Listening() bool { return Get().Count() > 0 }

// Count is how many are listening now.
func (f *Feature) Count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.n
}

// KeyOK is whether a key is the link's.
func (f *Feature) KeyOK(got string) bool {
	want := f.dev.key()
	return want != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// refused is why nobody may listen now, or nil.
func (f *Feature) refused() error {
	switch {
	case !f.dev.switchedOn():
		return ErrOff
	case f.dev.muted():
		return ErrMuted
	}
	return nil
}

// join adds a listener, or says why not. The microphones are asked for only by one that is let in.
func (f *Feature) join() (<-chan []int16, func(), error) {
	if err := f.refused(); err != nil {
		return nil, nil, err
	}
	f.mu.Lock()
	if f.n >= Max {
		f.mu.Unlock()
		return nil, nil, ErrFull
	}
	f.n++
	f.mu.Unlock()
	f.changed()
	frames, stop := f.dev.listen("baby monitor")
	var once sync.Once
	return frames, func() {
		once.Do(func() {
			stop()
			f.mu.Lock()
			f.n--
			f.mu.Unlock()
			f.changed()
		})
	}, nil
}

func (f *Feature) changed() {
	f.sensor.Set(f.Count() > 0)
	f.Changed.Emit(struct{}{})
}

// Serve sends the room's sound to one listener, in packets (see packet) through send, until done is
// closed, send fails, or the switch is turned off or the microphones are muted, and says which. send is
// finished with a packet when it returns.
func (f *Feature) Serve(done <-chan struct{}, send func([]byte) error) error {
	key := f.dev.key()
	frames, leave, err := f.join()
	if err != nil {
		return err
	}
	defer leave()
	refused := func() error {
		if f.dev.key() != key {
			return ErrNewKey
		}
		return f.refused()
	}

	var pcm []int16
	tick := time.NewTicker(check)
	defer tick.Stop()
	for {
		select {
		case <-done:
			return nil
		case <-tick.C:
			if err := refused(); err != nil {
				return err
			}
		case fr, ok := <-frames:
			if !ok {
				return errors.New("The microphones stopped")
			}
			if pcm = append(pcm, fr...); len(pcm) < batch*mic.FrameSamples {
				continue
			}
			// Looked at before every send: a mute is heard by nobody after it.
			if err := refused(); err != nil {
				return err
			}
			if err := send(packet(pcm)); err != nil {
				return err
			}
			pcm = pcm[:0]
		}
	}
}

// packet is the framing of one message to the browser: the samples as 16-bit signed integers,
// little-endian, mono, at mic.Rate, one after another and nothing else. Its length is always even.
func packet(pcm []int16) []byte {
	b := make([]byte, 2*len(pcm))
	for i, s := range pcm {
		b[2*i], b[2*i+1] = byte(s), byte(s>>8)
	}
	return b
}
