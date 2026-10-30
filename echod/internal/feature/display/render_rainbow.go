//go:build !dot && !spot

package display

import (
	"image"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/rainbow"
)

// The Rainbow turn screen (lib/rainbow): a cue, so the page stays up through the turn, with a glow of
// pinks, violets, blues and oranges round the whole edge of the screen, moving with the voice heard
// and the answer spoken and along the top and bottom alone while it thinks, and what was heard and the
// answer on a card of frosted glass a little above the middle. A turn opens with a gold globe at the
// right edge and a wave of pink crossing the screen, frosting the page as it goes. Under the same
// conditions as the cue light, and the words under the same as the captions (cueOver); a tap on the
// card puts it away, as a tap on the captions does.

// rainbowFrame is how often Rainbow is drawn again while it moves, from the start of one frame to the
// next: the other turn screens' pace (eqFrame). A frame that takes longer is followed after
// rainbowBreath, which leaves the slowest Shows' processor to the voice and the reply between frames.
const (
	rainbowFrame  = eqFrame
	rainbowBreath = 20 * time.Millisecond
)

// rainbowHere is whether a turn is drawn as Rainbow in this scene: chosen, and not at night, when its
// colors would light the room; the cue (the night clock's red band, or the plain light) is drawn
// instead.
func rainbowHere(s scene) bool { return rainbowOn() && !s.redClockFace() && !nightNow(s.now) }

// rainbowBegin moves the turn on to the frame's moment before the page is drawn, so the frame knows
// whether the page kept under it will do (rainbowKept); rainbowTurn draws it over the page.
func (r *renderer) rainbowBegin(s scene) {
	r.rainbow.Prepare(r.w, r.h)
	light, words := cueOver(s)
	var t rainbow.Turn
	if light {
		t = rainbow.Turn{Phase: s.cue, Heard: s.heard, Reply: s.reply}
		if !words {
			t.Heard, t.Reply = "", ""
		}
		if t.Phase == "listening" || t.Phase == "replying" {
			t.Level = turnEnergy(eqFor(t.Phase, false, s.now))
		}
	}
	r.rainbow.Begin(r.w, r.h, s.now, t)
}

// The page under Rainbow is kept from one frame to the next and drawn again once a second, or when
// what it shows changes: at Rainbow's pace, drawing it again each frame is a sixth of a frame's cost on
// the Show for a page that, under the light and the frosted card, does not move. Not a page that
// moves on its own (a camera, the rain map, a pop-up, the volume going up or down), nor one the light
// before an alarm, an answer to a press on that page's button, or a card lays anything over: those come
// and go by the second, and a kept copy of one carries a button, a line or a word from a frame that is
// no longer on the screen.
type rainbowPageKey struct {
	second                                                     int64
	dash, sheet, weather, drawer, calendar, deck, alert, strip bool
	playing, paused                                            bool
	tess                                                       int
}

func rainbowKeyOf(s scene) (rainbowPageKey, bool) {
	quiet := !s.showCamera && !s.showRadar && !s.showVolume && !skipOccluded(s) && s.sunrise == 0 && !s.skipNote.exists()
	return rainbowPageKey{
		second: s.now.Unix(), dash: s.showDash, sheet: s.showSheet, weather: s.showWeather, drawer: s.showDrawer,
		calendar: s.showCalendar, deck: s.showDeck, alert: s.showAlert, strip: s.strip, tess: s.tessPage,
		playing: s.playing, paused: s.paused, // the foot's word for the music changes with them
	}, quiet
}

// rainbowKept puts the page kept under Rainbow back on the screen, if it is still the page s shows,
// and what drawing it said about itself (draw clears it first), so the frame is skinned as it was.
func (r *renderer) rainbowKept(s scene) bool {
	k, quiet := rainbowKeyOf(s)
	r.rainbowReused = false // the frame's answer, whether or not the page is kept
	if !quiet || !r.rainbowPageOK || k != r.rainbowKey || !r.rainbow.Active() || r.rainbowPage == nil || r.rainbowPage.Rect != r.dst.Rect {
		return false
	}
	copy(r.dst.Pix, r.rainbowPage.Pix)
	r.marked, r.footAt, r.lcarsDrawn = r.rainbowDrew.marked, r.rainbowDrew.footAt, r.rainbowDrew.lcars
	r.musicDrawn = r.rainbowDrew.music // the kept page's word for the music, drawn again with it
	r.rainbowReused = true
	return true
}

// rainbowDrew is what drawing the kept page left in draw's flags.
type rainbowDrew struct {
	marked, lcars, music bool
	footAt               int
}

// rainbowTurn draws the turn over the page the frame drew.
func (r *renderer) rainbowTurn(s scene) {
	k, quiet := rainbowKeyOf(s)
	if !r.rainbowReused && r.rainbow.Active() {
		if !quiet {
			// Nothing is kept of a page a card, a strip, the light before an alarm or a skip answer is
			// over: the copy would outlive what was laid over it (rainbowKeyOf).
			r.rainbowPageOK = false
		} else {
			// The page as drawn, to put back while it stays the same (rainbowKept).
			if r.rainbowPage == nil || r.rainbowPage.Rect != r.dst.Rect {
				r.rainbowPage = image.NewRGBA(r.dst.Rect)
			}
			copy(r.rainbowPage.Pix, r.dst.Pix)
			r.rainbowKey = k
			r.rainbowDrew = rainbowDrew{marked: r.marked, lcars: r.lcarsDrawn, footAt: r.footAt, music: r.musicDrawn}
			r.rainbowPageOK = true
		}
	}
	r.rainbowReused = false
	at := r.rainbow.Finish(r.dst, rainbow.Faces{Heard: r.tiny, Reply: r.small})
	r.setCaptionsAt(at)
	if !r.rainbow.Busy() && !r.rainbow.Active() {
		r.rainbowPageOK = false
	}
}

// rainbowCovering is whether Rainbow is drawing over the page in this frame: a turn's entry, its light,
// its card, or the fade going out after it. The page's own button is left out while it is, and comes
// back on the frame after it is gone. Chosen as the turn style with no turn in hand, Rainbow draws
// nothing and covers nothing.
func (r *renderer) rainbowCovering(s scene) bool {
	return rainbowHere(s) && (r.rainbow.Active() || r.rainbow.Busy())
}
