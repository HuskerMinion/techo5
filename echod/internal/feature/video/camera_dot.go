//go:build dot

package video

import (
	"context"
	"image"
	"io"
	"time"
)

// Camera is the Dot's: it has no screen to show a camera on.
type Camera struct{}

func OpenCamera(context.Context, string, int, int) (*Camera, error) { return nil, ErrNotHere }
func (*Camera) Next(time.Duration) (*image.RGBA, error)             { return nil, ErrNotHere }
func (*Camera) Close()                                              {}

func OpenCameraSound(context.Context, string) (io.ReadCloser, error) { return nil, ErrNotHere }
func CameraLiveChanged()                                             {}
func CanRTSP() bool                                                  { return false }
func OpenCameraSoundRTSP(context.Context, string, string, string) (io.ReadCloser, error) {
	return nil, ErrNotHere
}
func OpenCameraRTSP(context.Context, string, string, string, int, int) (*Camera, error) {
	return nil, ErrNotHere
}
