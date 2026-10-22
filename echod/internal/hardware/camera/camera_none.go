//go:build dot || panel

// Package camera is the front camera. The Dot has none, and neither has a panel: a tablet's camera is
// Android's, and the app hands none to the daemon. A panel leaves the Echo camera out altogether rather
// than asking at run time, because a tablet on the Echo Show 5's MediaTek chip (the 8th gen Fire HD 8)
// has the same /dev/camera-isp and /dev/kd_camera_hw nodes, and the daemon took it for a Show's camera
// and failed to open it.
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

// ErrNeedsReboot is never returned here; it is the Shows' (camera_common.go).
var ErrNeedsReboot = errors.New("the camera is held off")

// Unwedged never fires here.
var Unwedged hook.Hook[struct{}]

func (c *Camera) Acquire() (func(), error)                     { return nil, errNone }
func (c *Camera) AcquireSlow() (func(), error)                 { return nil, errNone }
func (c *Camera) SetSlowEvery(time.Duration)                   {}
func (c *Camera) Unwedge()                                     {}
func SetGestureExposure(bool)                                  {}
func (c *Camera) Snapshot(ctx context.Context) (*Frame, error) { return nil, errNone }
func (c *Camera) Last() *Frame                                 { return nil }
func (c *Camera) Running() bool                                { return false }
func (f *Frame) Full() *image.RGBA                             { return f.RGBA }
func (f *Frame) Image() *image.RGBA                            { return f.RGBA }
func (f *Frame) Luma(w, h int) []uint8                         { return nil }
func (f *Frame) Blank() bool                                   { return false }
func (c *Camera) ListenLatest(chan *Frame) (cancel func())     { return func() {} }

// Dims is nothing here: no camera.
func (f *Frame) Dims() (w, h int) { return 0, 0 }

// Wedged is never anything here: no camera.
func (c *Camera) Wedged() error { return nil }
