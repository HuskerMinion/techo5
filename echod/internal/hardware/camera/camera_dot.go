//go:build dot

// Package camera is the front camera; the Dot has none.
package camera

import (
	"context"
	"errors"
	"image"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hook"
)

// Frame is one converted picture.
type Frame struct {
	Seq  uint64
	At   time.Time
	RGBA *image.RGBA
}

// Camera is the device.
type Camera struct {
	Frames hook.Hook[*Frame]
}

const Width, Height = 800, 600

var (
	once   sync.Once
	shared *Camera
)

func Get() *Camera {
	once.Do(func() { shared = &Camera{} })
	return shared
}

func Available() bool { return false }

var errNone = errors.New("no camera on this device")

func (c *Camera) Acquire() (func(), error)                     { return nil, errNone }
func (c *Camera) Snapshot(ctx context.Context) (*Frame, error) { return nil, errNone }
func (c *Camera) Last() *Frame                                 { return nil }
func (c *Camera) Running() bool                                { return false }
func (f *Frame) Full() *image.RGBA                             { return f.RGBA }
func (f *Frame) Image() *image.RGBA                            { return f.RGBA }
