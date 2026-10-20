//go:build !dot && !panel

package touch

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/input"
)

// Start opens the controller's node and reads its coordinate ranges.
func (s *Screen) Start(context.Context) error {
	find := input.Find
	name := deviceName
	if layout.AthenaMainline() {
		// TECHO5's mainline kernel: edt-ft5x06 names the same FocalTech "generic ft5x06 (<module id>)"
		find, name = input.FindPrefix, "generic ft5x06"
	}
	dev, err := find(name)
	if err != nil && layout.AthenaMainline() {
		// The driver names module ids it knows by the panel instead: 0x43 is "EP0430M09"
		dev, err = input.Find("EP0430M09")
	}
	if err != nil && layout.Board == "cronos" {
		// The 2nd gen Show 5 on TECHO5's mainline kernel: the Goodix driver's own name.
		dev, err = input.Find("Goodix Capacitive TouchScreen")
	}
	if err != nil {
		return fmt.Errorf("touch: %w", err)
	}
	x, errX := dev.Abs(absMTPositionX)
	y, errY := dev.Abs(absMTPositionY)
	if errX != nil || errY != nil || x.Max <= x.Min || y.Max <= y.Min {
		// The panel's raw axes, from the device's hardware notes, when the controller will not say.
		x.Min, x.Max, y.Min, y.Max = 0, int32(rawFallbackW-1), 0, int32(rawFallbackH-1)
		slog.Warn("touch: using the documented ranges", "errX", errX, "errY", errY)
	}
	s.dev = dev
	s.rawW = int(x.Max-x.Min) + 1
	s.rawH = int(y.Max-y.Min) + 1
	slog.Info("touchscreen open", "device", dev.Path, "raw", fmt.Sprintf("%dx%d", s.rawW, s.rawH))
	return nil
}

func (s *Screen) Close() error {
	if s.dev == nil {
		return nil
	}
	err := s.dev.Close()
	s.dev = nil
	return err
}

// Run reads until ctx is canceled; the node is closed from the side to end the blocking read.
func (s *Screen) Run(ctx context.Context) error {
	dev := s.dev
	stop := context.AfterFunc(ctx, func() { _ = dev.Close() })
	defer stop()
	return s.track(ctx, dev.Path, dev.Read)
}
