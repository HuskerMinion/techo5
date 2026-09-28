//go:build spot

package display

import (
	"image"
	"image/color"
	"math"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// Cameras on the round screen: the Spot's own (Camera on the dial, "show this spot") and Home
// Assistant's ("show the front door", the home_show_camera action). The picture fills the circle,
// cropped from the middle; the Spot's own is mirrored, as a mirror would show it. A tap takes it down,
// a swipe sideways steps to the next camera on the list, and a long press opens the list of cameras as
// a ring to turn: a tap shows the one in the middle. While the sensor runs, whatever is on the screen,
// a green dot at the top of the rim says so.

const (
	// cameraShow is how long a camera stays up when asked for; one stepped to from the screen stays
	// for the Camera time setting (cameraScreenTime).
	cameraShow = 30 * time.Second

	// cameraListIdle is how long the camera list stays up untouched.
	cameraListIdle = 15 * time.Second
)

var colCameraOn = color.RGBA{60, 203, 127, 255}

// stepCamera shows the camera after (or before) the one up, round the list.
func stepCamera(current string, by int) {
	cams := home.Get().Cameras()
	if len(cams) == 0 {
		return
	}
	i := 0
	for k, c := range cams {
		if c.Entity == current {
			i = k
		}
	}
	i = ((i+by)%len(cams) + len(cams)) % len(cams)
	home.Get().ShowCamera(cams[i].Entity, cameraScreenTime())
}

// cameraView fills the circle with the camera's latest frame.
func (r *roundRenderer) cameraView(s roundScene) {
	r.clear()
	v := s.camera
	if f := v.Frame; f != nil {
		r.coverCircle(f, v.Entity == home.LocalCamera)
	} else {
		msg := "Connecting…"
		if v.Error != "" {
			msg = "No picture"
		}
		r.centered(r.title, msg, 250, colDim)
		if v.Error != "" {
			r.paragraph(r.small, v.Error, 290, colDim, 2)
		}
	}
	// The name on a dark band near the top, readable over any picture.
	if v.Name != "" {
		w := r.width(r.label, v.Name)
		r.line(float64(center-w/2-10), 62, float64(center+w/2+10), 62, 32, color.RGBA{0, 0, 0, 150})
		r.centered(r.label, v.Name, 69, colText)
	}
	// The sound's control at the bottom of the face, where a circle is widest and nothing else is
	// drawn. A circle has no corner to put one in, so it is the same bar of words as the name above it:
	// a tap silences what the camera is saying and leaves the view up.
	if s.cameraSound {
		label := "Unmute"
		if s.cameraSoundLive {
			label = "Mute"
		}
		b := cameraSoundBox(r.width(r.label, label) + 24)
		r.line(float64(b.Min.X), float64(b.Min.Y+b.Dy()/2), float64(b.Max.X), float64(b.Min.Y+b.Dy()/2), float64(b.Dy()), color.RGBA{0, 0, 0, 150})
		r.centered(r.label, label, b.Min.Y+b.Dy()/2+8, colText)
		r.setCameraSoundAt(b)
	}
}

// cameraSoundBox is where the round camera page's sound control is drawn, and so where a tap on it has to
// land: a bar across the bottom of the face, inside the rim, and as wide as what it says.
func cameraSoundBox(w int) image.Rectangle {
	const barH, barMin = 34, 96
	const bottom = side - 44
	if w < barMin {
		w = barMin
	}
	return image.Rect(center-w/2, bottom-barH, center+w/2, bottom)
}

func (r *roundRenderer) setCameraSoundAt(b image.Rectangle) {
	r.zmu.Lock()
	r.cameraSoundAt = b
	r.zmu.Unlock()
}

// clearCameraSoundTap forgets where the control was: a face that does not draw it must not leave it
// tappable. Called at the start of every frame, as clearAlertTaps is.
func (r *roundRenderer) clearCameraSoundTap() {
	r.setCameraSoundAt(image.Rectangle{})
}

// cameraSoundTapped reports whether a tap at x, y is on the sound control drawn in the frame last
// drawn. The box is grown a little, as the alert pill's is: it is small and a finger is not.
func (r *roundRenderer) cameraSoundTapped(x, y int) bool {
	r.zmu.Lock()
	defer r.zmu.Unlock()
	return !r.cameraSoundAt.Empty() && image.Pt(x, y).In(r.cameraSoundAt.Inset(-10))
}

// coverCircle draws img scaled to cover the panel, cropped from the middle, inside the rim.
func (r *roundRenderer) coverCircle(img *image.RGBA, mirror bool) {
	sw, sh := img.Bounds().Dx(), img.Bounds().Dy()
	if sw == 0 || sh == 0 {
		return
	}
	scale := math.Max(float64(side)/float64(sw), float64(side)/float64(sh))
	offX := (float64(sw) - float64(side)/scale) / 2
	offY := (float64(sh) - float64(side)/scale) / 2
	inv := 1 / scale
	rr := float64(rimIn) * float64(rimIn)
	stride := img.Stride
	for y := 0; y < side; y++ {
		dy := float64(y) + 0.5 - center
		sy := int(offY + (float64(y)+0.5)*inv)
		if sy >= sh {
			sy = sh - 1
		}
		row := img.Pix[sy*stride:]
		for x := 0; x < side; x++ {
			dx := float64(x) + 0.5 - center
			if dx*dx+dy*dy > rr {
				continue
			}
			sx := int(offX + (float64(x)+0.5)*inv)
			if mirror {
				sx = sw - 1 - sx
			}
			if sx < 0 {
				sx = 0
			} else if sx >= sw {
				sx = sw - 1
			}
			j := (y*side + x) * 4
			k := sx * 4
			r.dst.Pix[j], r.dst.Pix[j+1], r.dst.Pix[j+2], r.dst.Pix[j+3] = row[k], row[k+1], row[k+2], 255
		}
	}
}

// cameraDot is the camera-in-use mark at the top of the rim.
func (r *roundRenderer) cameraDot() {
	r.discAt(center, float64(center-(rimIn+rimOut)/2), 9, colIconGround)
	r.discAt(center, float64(center-(rimIn+rimOut)/2), 6, colCameraOn)
}

// cameraIndex is where entity is on the camera list, or 0.
func cameraIndex(entity string) int {
	for i, c := range home.Get().Cameras() {
		if c.Entity == entity {
			return i
		}
	}
	return 0
}

// pickCamera is a tap on the camera list.
func pickCamera(sel int) {
	cams := home.Get().Cameras()
	if sel < 0 || sel >= len(cams) {
		return
	}
	home.Get().ShowCamera(cams[sel].Entity, cameraScreenTime())
}

// discImage draws img inside a circle of radius rad at (cx, cy), scaled to cover it.
func (r *roundRenderer) discImage(img *image.RGBA, cx, cy, rad float64) {
	sw, sh := img.Bounds().Dx(), img.Bounds().Dy()
	if sw == 0 || sh == 0 {
		return
	}
	d := 2 * rad
	scale := math.Max(d/float64(sw), d/float64(sh))
	inv := 1 / scale
	offX := (float64(sw) - d*inv) / 2
	offY := (float64(sh) - d*inv) / 2
	for y := int(cy - rad); y < int(cy+rad); y++ {
		if y < 0 || y >= side {
			continue
		}
		dy := float64(y) + 0.5 - cy
		sy := min(max(int(offY+(float64(y)+0.5-(cy-rad))*inv), 0), sh-1)
		for x := int(cx - rad); x < int(cx+rad); x++ {
			if x < 0 || x >= side {
				continue
			}
			dx := float64(x) + 0.5 - cx
			if dx*dx+dy*dy > rad*rad {
				continue
			}
			sx := min(max(int(offX+(float64(x)+0.5-(cx-rad))*inv), 0), sw-1)
			k := sy*img.Stride + sx*4
			j := (y*side + x) * 4
			// Over the ground (premultiplied): a logo's transparent corners keep the face's color.
			a := uint32(img.Pix[k+3])
			for c := 0; c < 3; c++ {
				r.dst.Pix[j+c] = uint8(min(uint32(img.Pix[k+c])+uint32(r.dst.Pix[j+c])*(255-a)/255, 255))
			}
		}
	}
}
