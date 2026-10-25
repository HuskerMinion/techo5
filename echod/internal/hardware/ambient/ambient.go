//go:build !dot

// Package ambient is the Echo Show's light sensor: how bright the room is, for a screen that
// should not glare at night.
//
// The board's sensor sits behind MediaTek's hwmsensor framework rather than IIO: it has to be
// switched on through /sys/class/misc/m_alsps_misc and then reports lux as ABS_X events on the
// input device named m_alsps_input. The driver samples every alsdelay nanoseconds, but the input
// layer drops a value equal to the last, so a reading arrives only when the light changes. (The
// IIO device on this board is the auxadc, which is why metrics.LuxPath finds nothing.)
//
// The 2nd gen Show 8 on TECHO5's mainline kernel is different: its TCS3400 is an IIO device with raw
// color counts and no lux, so it is polled and converted here (iio.go). So is the Skylight Calendar's
// EM3071X, which has no driver at all and is read over I2C (em3071x.go). The Echo Show 5 on TECHO5's
// mainline kernel has no misc node either: its JSA1214 is an IIO device that reports lux with its own
// scale, so it is polled through metrics (metricsLux).
package ambient

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/metrics"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hook"
	"github.com/HuskerMinion/techo5/echod/internal/lib/input"
	"github.com/HuskerMinion/techo5/echod/internal/service"
)

func init() {
	if Here {
		component.Register(component.Hardware, Get(), component.Order(20),
			component.Supervise(service.Restart(time.Second, 30*time.Second)))
	}
}

const (
	misc       = "/sys/class/misc/m_alsps_misc"
	deviceName = "m_alsps_input"

	// period is how often the driver samples, as alsdelay wants it in nanoseconds. Twice a second
	// follows a light being switched without chasing every flicker. A steady room sends nothing.
	period = 500 * time.Millisecond
)

type Sensor struct {
	// Lux fires with every reading. Listeners must not block: this is the reader's goroutine.
	Lux hook.Hook[float64]

	mu   sync.Mutex
	last float64
	at   time.Time

	dev  *input.Device
	poll poller // athena on mainline and the Skylight instead of dev

	// pollStop ends the Run polling poll, and pollDone is closed once it has: Close waits for that
	// before it closes the sensor, so a reading under way never meets a closed bus.
	pollStop context.CancelFunc
	pollDone chan struct{}
}

// poller is a sensor read every period rather than one that reports by itself.
type poller interface {
	sample() (lux float64, ok bool, err error)
	close() error
}

var (
	once   sync.Once
	shared *Sensor
)

func Get() *Sensor {
	once.Do(func() { shared = &Sensor{} })
	return shared
}

func (s *Sensor) Name() string { return "light sensor" }

// Current is the latest reading and when it arrived; ok is false before the first one.
func (s *Sensor) Current() (lux float64, at time.Time, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last, s.at, !s.at.IsZero()
}

// Start switches the sensor on and opens its input node.
func (s *Sensor) Start(context.Context) error {
	if layout.Skylight() {
		return s.startEM3071X()
	}
	if layout.AthenaMainline() {
		return s.startIIO()
	}
	if _, err := os.Stat(misc); os.IsNotExist(err) {
		path := metrics.Reader{}.LuxPath()
		if path == "" {
			return fmt.Errorf("ambient: no supported light sensor")
		}
		s.mu.Lock()
		s.poll = metricsLux(path)
		s.mu.Unlock()
		slog.Info("light sensor on", "device", path, "period", period)
		return nil
	} else if err != nil {
		return fmt.Errorf("ambient: finding the sensor: %w", err)
	}
	if err := os.WriteFile(misc+"/alsdelay", []byte(fmt.Sprint(period.Nanoseconds())), 0o644); err != nil {
		return fmt.Errorf("ambient: setting the report period: %w", err)
	}
	if err := os.WriteFile(misc+"/alsactive", []byte("1"), 0o644); err != nil {
		return fmt.Errorf("ambient: enabling the sensor: %w", err)
	}
	dev, err := input.Find(deviceName)
	if err != nil {
		return fmt.Errorf("ambient: %w", err)
	}
	s.dev = dev
	if lux, ok := s.seed(dev.Abs); ok {
		slog.Info("light sensor on", "device", dev.Path, "period", period, "lux", lux)
	} else {
		slog.Info("light sensor on", "device", dev.Path, "period", period)
	}
	return nil
}

// seed starts from the value the input device keeps for ABS_X. A steady room sends nothing, so after
// echod restarted the room read as unknown until the light changed, and auto-brightness had nothing
// to go on. The kernel holds the last value the driver reported. Before the driver's first sample
// after a boot that is zero, a dark room, and the first sample of a lit one differs from it, so it is
// sent and replaces it.
func (s *Sensor) seed(abs func(code uint16) (input.AbsInfo, error)) (float64, bool) {
	info, err := abs(0)
	if err != nil {
		slog.Debug("light sensor has no value to start from", "err", err)
		return 0, false
	}
	lux := float64(info.Value)
	s.mu.Lock()
	s.last, s.at = lux, time.Now()
	s.mu.Unlock()
	return lux, true
}

func (s *Sensor) Close() error {
	s.mu.Lock()
	p, dev := s.poll, s.dev
	stop, done := s.pollStop, s.pollDone
	s.poll, s.dev, s.pollStop, s.pollDone = nil, nil, nil, nil
	s.mu.Unlock()
	if p != nil {
		if stop != nil {
			stop()
			<-done
		}
		return p.close()
	}
	_ = os.WriteFile(misc+"/alsactive", []byte("0"), 0o644)
	if dev == nil {
		return nil
	}
	return dev.Close()
}

// errNotOpen is Run's answer when Close came first.
var errNotOpen = errors.New("ambient: the sensor is not open")

// Run reads until ctx is canceled. The read blocks in the kernel, so cancellation closes the node
// from the side and lets the read fail. What it reads is taken once, at the start: a Close that comes
// first, or while it runs, leaves it nothing to read rather than a nil sensor; a polled sensor is only
// closed once the poll has stopped.
func (s *Sensor) Run(ctx context.Context) error {
	s.mu.Lock()
	p, dev := s.poll, s.dev
	var done chan struct{}
	if p != nil {
		// Under the same lock as p is taken: a Close from now on stops this poll before closing p.
		var stop context.CancelFunc
		ctx, stop = context.WithCancel(ctx)
		defer stop()
		done = make(chan struct{})
		s.pollStop, s.pollDone = stop, done
	}
	s.mu.Unlock()
	if p != nil {
		defer close(done)
		return s.runPoll(ctx, p)
	}
	if dev == nil {
		return errNotOpen
	}
	stop := context.AfterFunc(ctx, func() { _ = dev.Close() })
	defer stop()

	for {
		e, err := dev.Read()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("ambient: reading %s: %w", dev.Path, err)
		}
		if e.Type != input.EvAbs || e.Code != 0 { // ABS_X carries the lux
			continue
		}
		s.emit(float64(e.Value))
	}
}

func (s *Sensor) emit(lux float64) {
	s.mu.Lock()
	s.last, s.at = lux, time.Now()
	s.mu.Unlock()
	s.Lux.Emit(lux)
}

// metricsLux is an IIO light sensor that reports lux itself, read through metrics: the 2nd gen Show 5's
// JSA1214 on TECHO5's mainline kernel.
type metricsLux string

func (p metricsLux) sample() (float64, bool, error) {
	r := (metrics.Reader{}).Lux(string(p))
	if !r.Known {
		return 0, false, fmt.Errorf("ambient: reading %s", string(p))
	}
	return r.Value, true, nil
}

func (metricsLux) close() error { return nil }
