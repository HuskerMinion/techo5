//go:build spot

package display

import (
	"fmt"
	"image/color"
	"math"
	"time"

	"golang.org/x/image/font"

	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
)

// The call face: over everything while a call rings, is placed or is up. The rim pulses green; a tap
// answers or hangs up (the same as anywhere else on the face, through the action), and a sideways
// swipe declines a call that is ringing.

var colCall = color.RGBA{46, 204, 113, 255}

// callGesture is a finger on the call face, and reports whether it was one.
func (d *Display) callGesture(g touch.Gesture) bool {
	st := phone.Get().State()
	if st.Phase == phone.Idle {
		return false
	}
	switch g.Kind {
	case touch.Tap:
		phone.Get().Button()
	case touch.SwipeLeft, touch.SwipeRight:
		if st.Phase == phone.Ringing {
			phone.Get().Hangup()
		}
	default:
		return false // volume swipes carry on as usual
	}
	d.wake()
	return true
}

// callLights brings a dark panel up for a call that rings: the face is how it is answered.
func (d *Display) callLights(st phone.State) {
	if st.Phase == phone.Ringing {
		d.mu.Lock()
		on := d.on
		d.mu.Unlock()
		if !on {
			d.apply(true, d.ceilingOrDefault(), false)
		}
	}
	d.wake()
}

func (r *roundRenderer) callFace(s roundScene) {
	st := s.call
	pulse := 0.45 + 0.55*math.Abs(math.Sin(float64(s.now.UnixMilli())/400))
	if st.Phase == phone.Talking {
		pulse = 1
	}
	r.arc(rimIn, rimOut, 0, 2*math.Pi, fade(colCall, pulse))

	title := "CALLING"
	switch st.Phase {
	case phone.Ringing:
		title = "INCOMING CALL"
	case phone.Talking:
		title = "ON A CALL"
	}
	r.centered(r.label, title, 150, colCall)

	who := st.Peer
	if who == "" {
		who = "Unknown"
	}
	face := r.small
	for _, f := range []font.Face{r.title, r.body} {
		if r.width(f, who) <= 360 {
			face = f
			break
		}
	}
	r.centered(face, who, 240, colText)

	switch st.Phase {
	case phone.Talking:
		d := s.now.Sub(st.Since).Round(time.Second)
		r.centered(r.body, fmt.Sprintf("%d:%02d", int(d.Minutes()), int(d.Seconds())%60), 290, colDim)
		r.centered(r.small, "Tap to hang up", 370, colDim)
	case phone.Ringing:
		r.centered(r.small, "Tap to answer", 350, colText)
		r.centered(r.small, "Swipe to decline", 385, colDim)
	default:
		r.centered(r.small, "Tap to hang up", 370, colDim)
	}
}

// The contact list: big rows, five at a time, so a finger lands on the one it means. A swipe up or down
// scrolls, and arrows above and below say there is more.
const (
	contactRows   = 5
	contactRowH   = 58
	contactFirstY = 158 // the first visible row's baseline
)

// contactRowAt is which contact a tap at y is on, with top the first one shown, or -1.
func contactRowAt(y, top, n int) int {
	i := (y - (contactFirstY - 38)) / contactRowH
	if y < contactFirstY-38 || i < 0 || i >= contactRows || top+i >= n {
		return -1
	}
	return top + i
}

// contactTopFor keeps a scroll position inside the list.
func contactTopFor(top, n int) int { return min(max(top, 0), max(n-contactRows, 0)) }

// contactList is who the Call item offers: a tap calls them.
func (r *roundRenderer) contactList(s roundScene) {
	r.clear()
	r.centered(r.label, "CALL", 84, colCall)
	if len(s.contacts) == 0 {
		msg := "No contacts yet: Home Assistant's phone_contacts action sets them"
		if !s.phoneReady {
			msg = "The phone is not set up"
		}
		r.paragraph(r.body, msg, 230, colDim, 3)
		return
	}
	top := contactTopFor(s.contactTop, len(s.contacts))
	for i := 0; i < contactRows && top+i < len(s.contacts); i++ {
		y := contactFirstY + i*contactRowH
		w := 330
		if i == 0 || i == contactRows-1 {
			w = 280 // the circle is narrower at the top and bottom rows
		}
		r.line(float64(center-w/2), float64(y-12), float64(center+w/2), float64(y-12), float64(contactRowH-10), color.RGBA{28, 34, 42, 255})
		r.centered(r.title, clip(r.title, r, s.contacts[top+i].Name, w-30), y, colText)
	}
	if top > 0 {
		r.triangle(center-16, 112, center+16, 112, center, 96, colCall)
	}
	if top+contactRows < len(s.contacts) {
		r.triangle(center-16, 438, center+16, 438, center, 454, colCall)
	}
	if len(s.contacts) > contactRows {
		r.centered(r.tiny, "swipe for more", 426, colDim)
	}
}
