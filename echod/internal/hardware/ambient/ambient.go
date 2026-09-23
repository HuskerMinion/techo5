//go:build !dot

// Package ambient is the Echo Show's light sensor: how bright the room is, for a screen that
// should not glare at night.
//
// The board's sensor sits behind MediaTek's hwmsensor framework rather than IIO: it has to be
// switched on through /sys/class/misc/m_alsps_misc and then reports lux as ABS_X events on the
// input device named m_alsps_input, every alsdelay nanoseconds. (The IIO device on this board is
// the auxadc, which is why metrics.LuxPath finds nothing.)
package ambient

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hook"
	"github.com/HuskerMinion/techo5/echod/internal/lib/input"
	"github.com/HuskerMinion/techo5/echod/internal/service"
)

func init() {
	component.Register(component.Hardware, Get(), component.Order(20),
		component.Supervise(service.Restart(time.Second, 30*time.Second)))
}

const (
	misc       = "/sys/class/misc/m_alsps_misc"
	deviceName = "m_alsps_input"

	// period is how often the driver reports, as alsdelay wants it in nanoseconds. Twice a second
	// follows a light being switched without chasing every flicker.
	period = 500 * time.Millisecond
)

type Sensor struct {
	// Lux fires with every reading. Listeners must not block: this is the reader's goroutine.
	Lux hook.Hook[float64]

	mu   sync.Mutex
	last float64
	at   time.Time

	dev *input.Device
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
	slog.Info("light sensor on", "device", dev.Path, "period", period)
	return nil
}

func (s *Sensor) Close() error {
	_ = os.WriteFile(misc+"/alsactive", []byte("0"), 0o644)
	if s.dev == nil {
		return nil
	}
	err := s.dev.Close()
	s.dev = nil
	return err
}

// Run reads until ctx is canceled. The read blocks in the kernel, so cancellation closes the node
// from the side and lets the read fail.
func (s *Sensor) Run(ctx context.Context) error {
	dev := s.dev
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
		lux := float64(e.Value)
		s.mu.Lock()
		s.last, s.at = lux, time.Now()
		s.mu.Unlock()
		s.Lux.Emit(lux)
	}
}
