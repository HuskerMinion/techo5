//go:build !linux || !arm || !cgo

package camera

import "image"

// Other architectures retain the original pure-Go, portable encoder.
func encodeNativeTurbo(img *image.RGBA, quality int) ([]byte, error) {
	return encodeStripes(img, quality)
}
