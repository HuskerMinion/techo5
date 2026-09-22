//go:build !dot

// Package lenscover is the physical shutter over the camera, which only the Echo Show 8 has.
//
// It is not the microphone mute and should not be confused with it: the mute is a latch the daemon
// can engage and a red light reports, while this is a piece of plastic somebody slides across the
// lens. Nothing in software can open or close it, and nothing in software should pretend to. All
// this package does is say which way it is, so the camera can decline rather than stream a picture
// of the inside of a shutter, and so Home Assistant can show it.
//
// The kernel reports it as SW_CAMERA_LENS_COVER on an EV_SW device — gpio-499 behind gpio-keys on a
// Show 8. A switch is not a button: it holds a position, and the kernel sends nothing until it
// moves, so the state has to be read once at start with EVIOCGSW and then followed.
package lenscover

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

// swCameraLensCover is SW_CAMERA_LENS_COVER from the kernel's input codes. One means the lens is
// covered.
const swCameraLensCover = 0x09

type Cover struct {
	// Changed fires when the shutter moves, with true meaning covered. Listeners must not block:
	// this is the reader's goroutine.
	Changed hook.Hook[bool]

	mu      sync.Mutex
	present bool
	covered bool

	dev *input.Device
}

var (
	once   sync.Once
	shared *Cover
)

func Get() *Cover {
	once.Do(func() { shared = &Cover{} })
	return shared
}

func (c *Cover) Name() string { return "lens cover" }

// Present reports whether this device has a shutter at all. Everything but the Show 8 says no, and
// a caller that gates on Covered must check this first: a device with no shutter is not covered, but
// neither is it protected by one.
func (c *Cover) Present() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.present
}

// Covered reports whether the shutter is across the lens. False on a device that has none.
func (c *Cover) Covered() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.present && c.covered
}

// Start finds the shutter and reads which way it is now.
func (c *Cover) Start(context.Context) error {
	dev, err := find()
	if err != nil {
		// Not a failure: only one device in the family has a shutter, and the rest carry on without.
		slog.Info("no camera lens cover on this device")
		return nil
	}
	covered, err := dev.Switch(swCameraLensCover)
	if err != nil {
		_ = dev.Close()
		return fmt.Errorf("lenscover: reading %s: %w", dev.Path, err)
	}
	c.mu.Lock()
	c.dev, c.present, c.covered = dev, true, covered
	c.mu.Unlock()
	slog.Info("camera lens cover", "device", dev.Path, "covered", covered)
	return nil
}

func (c *Cover) Close() error {
	c.mu.Lock()
	dev := c.dev
	c.dev = nil
	c.mu.Unlock()
	if dev == nil {
		return nil
	}
	return dev.Close()
}

// Run follows the shutter until ctx is canceled. The read blocks in the kernel, so cancellation
// closes the node from the side and lets the read fail, as the light sensor does.
func (c *Cover) Run(ctx context.Context) error {
	c.mu.Lock()
	dev := c.dev
	c.mu.Unlock()
	if dev == nil {
		// No shutter here. Wait to be told to stop rather than exiting, so the supervisor does not
		// read a missing sensor as a service that keeps falling over.
		<-ctx.Done()
		return nil
	}

	stop := context.AfterFunc(ctx, func() { _ = dev.Close() })
	defer stop()

	for {
		e, err := dev.Read()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("lenscover: reading %s: %w", dev.Path, err)
		}
		if e.Type != input.EvSw || e.Code != swCameraLensCover {
			continue
		}
		covered := e.Value != 0
		c.mu.Lock()
		changed := covered != c.covered
		c.covered = covered
		c.mu.Unlock()
		if !changed {
			continue
		}
		slog.Info("camera lens cover", "covered", covered)
		c.Changed.Emit(covered)
	}
}

// find opens the one input device that reports a camera lens cover. Which device that is belongs to
// the board — a Show 8 has it on gpio-keys — so it is found by capability rather than by name.
func find() (*input.Device, error) {
	devs, err := input.List()
	if err != nil {
		return nil, err
	}
	var found *input.Device
	for _, d := range devs {
		if found == nil && hasLensCover(d.Path) {
			found = d
			continue
		}
		_ = d.Close()
	}
	if found == nil {
		return nil, fmt.Errorf("lenscover: no input device reports SW_CAMERA_LENS_COVER")
	}
	return found, nil
}

// hasLensCover reads the switches an event node reports, from the same sysfs file `evtest` reads.
func hasLensCover(path string) bool {
	b, err := os.ReadFile("/sys/class/input/" + filepath.Base(path) + "/device/capabilities/sw")
	if err != nil {
		return false
	}
	return capsHaveLensCover(string(b))
}

// capsHaveLensCover picks one switch out of an input device's capability bitmap. The kernel writes
// these as hex words separated by spaces, most significant word first, so the last word holds the
// lowest codes — which is where a lens cover's bit 9 lives on every device seen so far. A device
// with no switches at all writes a single "0".
func capsHaveLensCover(caps string) bool {
	words := strings.Fields(caps)
	// Count back from the end to reach the word this code falls in.
	const bitsPerWord = 64
	word := len(words) - 1 - swCameraLensCover/bitsPerWord
	if word < 0 {
		return false
	}
	v, err := strconv.ParseUint(words[word], 16, 64)
	if err != nil {
		return false
	}
	return v&(1<<(swCameraLensCover%bitsPerWord)) != 0
}
