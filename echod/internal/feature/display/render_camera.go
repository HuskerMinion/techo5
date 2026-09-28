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
	// cameraVoiceShow is how long a camera asked for by voice stays up; one picked from the list
	// stays for the Camera time setting (cameraScreenTime).
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
	if left > 0 && left < 24*time.Hour { // "until tapped" is a year: no countdown for that
		hint = "tap to close  ·  " + left.String()
	}
	draw.Draw(r.dst, image.Rect(0, r.h-36, r.w, r.h), image.NewUniform(shade), image.Point{}, draw.Over)
	r.text(r.tiny, hint, r.margin, r.h-11, dim)

	// The sound's control, at the end of that strip and clear of the hint: a tap on it silences what
	// the camera is saying and leaves the view up. Only while there is a sound to silence, and the
	// rectangle is kept so that the tap can be told from the one that takes the view down.
	if s.cameraSound {
		// The control says what it does: silence the sound, or ask for it.
		label := "Unmute"
		if s.cameraSoundLive {
			label = "Mute"
		}
		b := r.cameraSoundBox(label)
		r.bevel(b, shift(ember, 16), true)
		m := r.tiny.Metrics()
		mid := b.Min.Y + (b.Dy()+m.Ascent.Ceil()-m.Descent.Ceil())/2
		r.text(r.tiny, label, b.Min.X+(b.Dx()-r.width(r.tiny, label))/2, mid, cream)
		r.setCameraSoundAt(b)
		return
	}
	r.setCameraSoundAt(image.Rectangle{})
}

// cameraSoundBox is where the camera page's sound control is drawn: the right end of the strip along the
// bottom, which is where the hint is not. It is measured from the words it carries and the face they are
// drawn in rather than being a fixed size, so it grows with a wider panel the way the rest of the page
// does: "Unmute" has to fit inside it, and a tap has to be able to land on it.
func (r *renderer) cameraSoundBox(label string) image.Rectangle {
	pad := r.s(14)
	w := r.width(r.tiny, label) + 2*pad
	h := r.tiny.Metrics().Height.Ceil() + r.s(10)
	bottom := r.h - r.s(6)
	return image.Rect(r.w-r.margin-w, bottom-h, r.w-r.margin, bottom)
}
