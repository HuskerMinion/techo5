//go:build dot

package camstream

// The Dot has no camera, so no encoder either (h264.go).

func hasEncoder() bool { return false }

// CropOffered is false: no camera.
func CropOffered() bool { return false }

// Described is the stream as it is sent (h264.go).
type Described struct {
	Codec              string
	W, H, FPS, Bitrate int
}

// Stream is nothing: no camera.
func Stream() Described {
	w, h := size()
	return Described{"JPEG", w, h, maxFPS, 0}
}

func (f *Feature) pumpH264(stop <-chan struct{}, release func()) { release() }

func (f *Feature) sdpH264() string { return "" }
