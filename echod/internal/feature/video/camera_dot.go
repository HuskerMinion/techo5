//go:build dot

package video

import (
	"context"
	"image"
	"time"
)

// Camera is the Dot's: it has no screen to show a camera on.
type Camera struct{}

func OpenCamera(context.Context, string, int, int) (*Camera, error) { return nil, ErrNotHere }
func (*Camera) Next(time.Duration) (*image.RGBA, error)             { return nil, ErrNotHere }
func (*Camera) Close()                                              {}
