//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
	"log/slog"
	"strings"

	"golang.org/x/image/vector"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// Turning a camera from its view. A round button sits in the bottom right corner, above the strip
// that says a tap closes the view; it opens a pad of four arrows and closes it again. The pad stays
// away until asked for, because four arrows always on a touch screen get pressed by whatever leans
// on it, and a tap anywhere else still closes the view. The button is drawn only for a camera Home
// Assistant has been told how to turn (home_camera_ptz): every other view looks as it always has.
//
// Turning goes through Home Assistant rather than speaking ONVIF here: the picture already comes
// from Home Assistant, and going direct would mean holding every camera's address and password. The
// target named for a camera is either an ONVIF camera, turned with onvif.ptz's continuous move for a
// moment, or a script, called with direction set to up, down, left or right.
//
// The move is the continuous one because it is the one ONVIF's Profile S requires of a camera that
// turns: the relative and absolute moves are optional, and a camera may accept a relative move and do
// nothing with it, which leaves arrows that look dead. The script is for a camera onvif.ptz cannot
// turn at all — one that refuses Home Assistant's continuous move because it carries a zoom speed
// the camera has no zoom for, one that wants its own API — and turns it however that camera needs.

const (
	// ptzFor is how long one tap turns the camera, in seconds, and ptzSpeed how fast, of its fastest:
	// half a second at half speed is a visible step on a camera watching a room, small enough to aim
	// with.
	ptzFor   = 0.5
	ptzSpeed = 0.5
)

// ptzDirections are the arrows in the order the pad is drawn, as a script is told them.
var ptzDirections = [4]string{"up", "down", "left", "right"}

// ptzMoves is what each arrow asks of onvif.ptz, in the same order.
var ptzMoves = [4]map[string]any{
	{"tilt": "UP"},
	{"tilt": "DOWN"},
	{"pan": "LEFT"},
	{"pan": "RIGHT"},
}

// ptzCall is the Home Assistant call that turns target one step the i'th way.
func ptzCall(target string, i int) (domain, service string, data map[string]any) {
	if strings.HasPrefix(target, "script.") {
		return "script", "turn_on", map[string]any{
			"entity_id": target,
			"variables": map[string]any{"direction": ptzDirections[i]},
		}
	}
	data = map[string]any{
		"entity_id":           target,
		"move_mode":           "ContinuousMove",
		"continuous_duration": ptzFor,
		"speed":               ptzSpeed,
	}
	for k, v := range ptzMoves[i] {
		data[k] = v
	}
	return "onvif", "ptz", data
}

// cameraPTZHit answers a tap on the camera view: true when it landed on the turning button or an
// arrow, which then did what it says, false when it landed on the picture and the view should close.
func (d *Display) cameraPTZHit(p image.Point) bool {
	r := d.r
	if r == nil {
		return false
	}
	r.ptzMu.Lock()
	button, pad, open, entity := r.ptzAt, r.ptzPadAt, r.ptzOpen, r.ptzEntity
	r.ptzMu.Unlock()

	if !button.Empty() && p.In(button) {
		r.ptzMu.Lock()
		r.ptzOpen = !open
		r.ptzMu.Unlock()
		return true
	}
	if !open {
		return false
	}
	target := home.PTZFor(entity)
	for i, box := range pad {
		if box.Empty() || !p.In(box) {
			continue
		}
		if target == "" {
			return true
		}
		domain, service, data := ptzCall(target, i)
		safe.Go("camera ptz", func() {
			if err := hass.Get().Call(domain, service, data); err != nil {
				slog.Error("turning the camera failed", "entity", target, "err", err)
			}
		})
		return true
	}
	return false
}

// The buttons' look: a dark disc that reads over any picture, the mark in the screen's cream.
var (
	ptzButton   = color.RGBA{0x00, 0x00, 0x00, 0x99}
	ptzButtonOn = color.RGBA{0x3a, 0x2c, 0x22, 0xdd}
	ptzPadBack  = color.RGBA{0x00, 0x00, 0x00, 0x55}
	ptzMark     = color.RGBA{0xe8, 0xdc, 0xc8, 0xff}
)

// cameraPTZ draws the turning button over the view, and the arrows when they are out, and notes
// where they are, for a tap there.
func (r *renderer) cameraPTZ(v home.CameraView) {
	r.ptzMu.Lock()
	defer r.ptzMu.Unlock()
	if r.ptzEntity != v.Entity { // another camera, or a new view: it opens with the arrows away
		r.ptzEntity, r.ptzOpen = v.Entity, false
	}
	r.ptzAt, r.ptzPadAt = image.Rectangle{}, [4]image.Rectangle{}
	if home.PTZFor(v.Entity) == "" {
		return
	}
	rad, gap := r.s(30), r.s(18)
	cx, cy := r.w-gap-rad, r.h-36-gap-rad // above the strip along the bottom
	back := ptzButton
	if r.ptzOpen {
		back = ptzButtonOn
	}
	r.disc(cx, cy, rad, back)
	a := r.s(7)
	for i, off := range [4]image.Point{{0, -r.s(13)}, {0, r.s(13)}, {-r.s(13), 0}, {r.s(13), 0}} {
		c := image.Pt(cx+off.X, cy+off.Y)
		r.ptzArrow(image.Rect(c.X-a, c.Y-a, c.X+a, c.Y+a), i)
	}
	r.ptzAt = image.Rect(cx-rad, cy-rad, cx+rad, cy+rad)
	if !r.ptzOpen {
		return
	}
	// The pad: four buttons in a cross above the one, on a faint disc that says they belong together.
	br, reach := r.s(34), r.s(74)
	pcx, pcy := r.w-gap-reach-br, cy-rad-gap-reach-br
	r.disc(pcx, pcy, reach+br+r.s(6), ptzPadBack)
	a = r.s(13)
	for i, off := range [4]image.Point{{0, -reach}, {0, reach}, {-reach, 0}, {reach, 0}} {
		c := image.Pt(pcx+off.X, pcy+off.Y)
		r.disc(c.X, c.Y, br, ptzButton)
		r.ptzArrow(image.Rect(c.X-a, c.Y-a, c.X+a, c.Y+a), i)
		r.ptzPadAt[i] = image.Rect(c.X-br, c.Y-br, c.X+br, c.Y+br)
	}
}

// ptzArrow is a solid triangle pointing up, down, left or right (0 to 3) inside b.
func (r *renderer) ptzArrow(b image.Rectangle, dir int) {
	r.fillShape(b, ptzMark, func(z *vector.Rasterizer, w, h float32) {
		switch dir {
		case 0:
			z.MoveTo(w/2, 0)
			z.LineTo(w, h)
			z.LineTo(0, h)
		case 1:
			z.MoveTo(0, 0)
			z.LineTo(w, 0)
			z.LineTo(w/2, h)
		case 2:
			z.MoveTo(0, h/2)
			z.LineTo(w, 0)
			z.LineTo(w, h)
		default:
			z.MoveTo(0, 0)
			z.LineTo(w, h/2)
			z.LineTo(0, h)
		}
	})
}
