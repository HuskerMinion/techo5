//go:build !linux

package jpegenc

import (
	"errors"
	"image"
)

// Available is false off Linux: there is no encoder to find.
func Available() bool { return false }

// Fits is the encoder's sizes, as on Linux.
func Fits(r image.Rectangle) bool {
	return r.Dx() > 0 && r.Dy() > 0 && r.Dx()%32 == 0 && r.Dy()%8 == 0
}

var errNone = errors.New("jpegenc: no hardware encoder")

// Encode always fails off Linux.
func Encode(*image.RGBA, int) ([]byte, error) { return nil, errNone }

// EncodeYUYV always fails off Linux.
func EncodeYUYV(int, int, int, func([]byte)) ([]byte, error) { return nil, errNone }

// ErrBackingOff is Linux's; off Linux the encoder is never there to back off from.
var ErrBackingOff = errors.New("jpegenc: backing off after a failure")
