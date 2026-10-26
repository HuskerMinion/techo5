//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"image/draw"
	"slices"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/talkback"
)

// The camera view: the latest frame centered on the panel, the camera's name and the time in the
// corners, and a hint that a tap closes it. Below it, the cameras page: a list, like the radio's.
// hasCameraLook is whether the screen offers Camera picture and Camera strips (camera_look.go): the
// Show's camera view draws them.
const hasCameraLook = true

const (
	// cameraVoiceShow is how long a camera asked for by voice stays up; one picked from the list
	// stays for the Camera time setting (cameraScreenTime).
	cameraVoiceShow = 30 * time.Second
)

func (r *renderer) cameraView(s scene, v home.CameraView) {
	if len(v.Grid) > 0 {
		r.cameraGrid(s, v)
		return
	}
	name := v.Name
	if s.demo {
		name = demoCameras[0]
	}
	if v.Frame != nil {
		b := v.Frame.Bounds()
		x := (r.w - b.Dx()) / 2
		y := (r.h - b.Dy()) / 2
		home.ReadFrame(func() { draw.Draw(r.dst, image.Rect(x, y, x+b.Dx(), y+b.Dy()), v.Frame, b.Min, draw.Src) })
	} else {
		msg := fmt.Sprintf(tr("drawer_camera_connect"), name)
		if v.Error != "" {
			msg = name + ": " + v.Error
		}
		r.text(r.small, msg, (r.w-r.width(r.small, msg))/2, r.h/2, dim)
	}
	// A picture a notification sent with no words is the picture alone: no name, no clock and no
	// caption over it, only the strip at the foot that says how to close it.
	wordless := v.Caption != nil && v.Caption.Wordless()
	fade := cameraBandsFade()
	stripShade := cameraStripShade(fade)
	// Where the words go: the name and the clock on the top strip, the hint on the foot's. Fading strips
	// have no edge to sit the words against, so there both lines are in one face and each in the middle
	// of a strip as high as the foot's, the same way from the top and from the foot.
	nameBase, hintBase, hintFace, side := r.s(32), r.h-r.s(11), r.tiny, r.margin
	if fade {
		m := r.small.Metrics()
		mid := (m.Ascent.Ceil() - m.Descent.Ceil()) / 2
		band := r.cameraStrip()
		nameBase, hintBase, hintFace = band/2+mid, r.h-band/2+mid, r.small
		// The sides as far in as the top and the foot: the space above a capital, about seven tenths of
		// the face's ascent high.
		side = max((band-m.Ascent.Ceil()*7/10)/2, r.s(12))
	}
	if !wordless {
		// Corners on a dark strip so they read over any picture; with Camera strips at Fade, a deeper one
		// that fades into it.
		if fade {
			fadeStrip(r.dst, image.Rect(0, 0, r.w, r.s(110)), stripShade, true)
		} else {
			washOver(r.dst, image.Rect(0, 0, r.w, r.s(44)), stripShade)
		}
		r.text(r.small, name, side, nameBase, captionText)
		t := clockHM(s.now)
		r.text(r.small, t, r.w-side-r.width(r.small, t), nameBase, captionDim)
	}
	left := time.Until(v.Until).Round(time.Second)
	hint := tr("drawer_camera_close")
	talking := s.talk.Entity == v.Entity && s.talk.Phase != talkback.Idle // the view is held: no countdown
	// A still a notification left until tapped (twelve hours at most) has no countdown to show, and a
	// view held for a day or more is not counted down either.
	untilTapped := v.Caption != nil && v.Caption.UntilTapped
	if left > 0 && left < 24*time.Hour && !untilTapped && !talking {
		hint = tr("drawer_camera_close") + "  ·  " + left.String()
	}
	if v.Caption != nil && !wordless {
		r.captionBar(*v.Caption)
	}
	if fade {
		fadeStrip(r.dst, image.Rect(0, r.h-max(r.cameraStrip(), r.s(110)), r.w, r.h), stripShade, false)
	} else {
		draw.Draw(r.dst, image.Rect(0, r.h-r.cameraStrip(), r.w, r.h), image.NewUniform(stripShade), image.Point{}, draw.Over)
	}

	// The view's action buttons, in a row above the strip, and above a notification's words when they
	// are up, so neither Talk nor the words are under one.
	foot := r.h - r.cameraStrip()
	if v.Caption != nil && !wordless {
		foot = r.captionRect(*v.Caption).Min.Y
	}
	r.cameraButtons(s.camButtons, foot)

	// Talk, left of the sound's control (or at the end of the strip without one): a tap sends the
	// microphones to the camera's speaker, and another ends it. Why a talk failed takes the hint's place
	// for a few seconds.
	end := r.w - r.margin
	r.setCameraTalkAt(image.Rectangle{})
	if s.talkOffered {
		if s.cameraSound {
			end = r.cameraSoundBox(r.cameraSoundLabel(s)).Min.X - r.s(10)
		}
		label := talkLabel(s.talk, v.Entity)
		b := r.cameraSoundBox(label)
		b = b.Add(image.Pt(end-b.Max.X, 0))
		r.cameraControl(b, label, talking)
		r.setCameraTalkAt(b)
		// A talk goes on only while this is on the screen. The strips and cards drawn over the page
		// afterward (an announcement, a recording, a reminder, a pop-up) cover it, so with one up it
		// is not seen; the volume bar is the person talking turning it, and the talk goes on under it.
		if !s.announceRecording && !s.showAnnouncement && !s.showReminder && !s.showNotification && s.popup == nil {
			talkback.Get().Seen(v.Entity)
		}
		end = b.Min.X - r.s(10)
		if s.talk.Error != "" && s.talk.Entity == v.Entity {
			hint = fmt.Sprintf(tr("drawer_talk_error"), s.talk.Error)
		}
	} else if s.cameraSound {
		end = r.cameraSoundBox(r.cameraSoundLabel(s)).Min.X - r.s(10)
	}
	r.text(hintFace, r.clipTo(hintFace, hint, end-side), side, hintBase, captionDim)

	// The sound's control, at the end of that strip and clear of the hint: a tap on it silences what
	// the camera is saying and leaves the view up. Only while there is a sound to silence, and the
	// rectangle is kept so that the tap can be told from the one that takes the view down.
	if s.cameraSound {
		// The control says what it does: silence the sound, or ask for it.
		label := r.cameraSoundLabel(s)
		b := r.cameraSoundBox(label)
		r.cameraControl(b, label, false)
		r.setCameraSoundAt(b)
		return
	}
	r.setCameraSoundAt(image.Rectangle{})
}

// cameraButtons draws the camera page's action buttons in a row ending above foot, from the left: each as
// wide as what it says and as tall as a finger needs, and all cut short alike when the row would run
// off the page, the widest first. Where they went is kept for a tap (cameraButtonTapped).
func (r *renderer) cameraButtons(faces []camButtonFace, foot int) {
	if len(faces) == 0 {
		return
	}
	faces = append([]camButtonFace(nil), faces...) // the words are cut in this frame's copy, not the scene's
	face := r.faces().button
	pad, gap := r.s(18), r.s(14)
	h := max(r.s(44), face.Metrics().Height.Ceil()+r.s(16))
	bottom := foot - r.s(10)
	room := r.w - 2*r.margin - (len(faces)-1)*gap
	widths := make([]int, len(faces))
	total := 0
	for i, f := range faces {
		widths[i] = max(r.width(face, f.label)+2*pad, r.s(80))
		total += widths[i]
	}
	if total > room {
		// Too wide for the page: the widest are cut to what the row has left once the narrower ones
		// have theirs, so an armed button's question is cut no more than it must be.
		most := fairWidth(widths, room)
		for i := range faces {
			if widths[i] > most {
				faces[i].label = r.clipTo(face, faces[i].label, most-2*pad)
				widths[i] = most
			}
		}
	}
	at := make([]image.Rectangle, len(faces))
	x := r.margin
	for i, f := range faces {
		b := image.Rect(x, bottom-h, x+widths[i], bottom)
		r.cameraButton(b, f)
		at[i] = b
		x = b.Max.X + gap
	}
	r.drawnButtons = at
}

// fairWidth is the most any one of widths may have for them all to fit in room: the narrow ones keep
// theirs, and the rest share what is left alike.
func fairWidth(widths []int, room int) int {
	sorted := slices.Sorted(slices.Values(widths))
	for i, w := range sorted {
		if share := room / (len(sorted) - i); w > share {
			return share
		}
		room -= w
	}
	return room
}

// cameraSoundLabel is what the sound's control says: what a tap on it does.
func (r *renderer) cameraSoundLabel(s scene) string {
	if s.cameraSoundLive {
		return tr("drawer_mute")
	}
	return tr("drawer_unmute")
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
