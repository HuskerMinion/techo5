//go:build !dot && !spot

package display

import (
	"image"
	"image/draw"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// The camera view: the latest frame centered on the panel, the camera's name and the time in the
// corners, and a hint that a tap closes it. Below it, the cameras page: a list, like the radio's.
const (
	// camListShow is how long a camera picked from the list stays up; cameraVoiceShow one asked
	// for by voice.
	camListShow     = 60 * time.Second
	cameraVoiceShow = 30 * time.Second
)

func (r *renderer) cameraView(s scene, v home.CameraView) {
	name := v.Name
	if s.demo {
		name = demoCameras[0]
	}
	if v.Frame != nil {
		b := v.Frame.Bounds()
		x := (r.w - b.Dx()) / 2
		y := (r.h - b.Dy()) / 2
		draw.Draw(r.dst, image.Rect(x, y, x+b.Dx(), y+b.Dy()), v.Frame, b.Min, draw.Src)
	} else {
		msg := "Connecting to " + name + "…"
		if v.Error != "" {
			msg = name + ": " + v.Error
		}
		r.text(r.small, msg, (r.w-r.width(r.small, msg))/2, r.h/2, dim)
	}
	// Corners on a dark strip so they read over any picture.
	draw.Draw(r.dst, image.Rect(0, 0, r.w, 44), image.NewUniform(shade), image.Point{}, draw.Over)
	r.text(r.small, name, r.margin, 32, cream)
	t := clockHM(s.now)
	r.text(r.small, t, r.w-r.margin-r.width(r.small, t), 32, dim)
	left := time.Until(v.Until).Round(time.Second)
	hint := "tap to close"
	if left > 0 {
		hint = "tap to close  ·  " + left.String()
	}
	draw.Draw(r.dst, image.Rect(0, r.h-36, r.w, r.h), image.NewUniform(shade), image.Point{}, draw.Over)
	r.text(r.tiny, hint, r.margin, r.h-11, dim)
}
