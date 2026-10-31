//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"image/color"
	"log/slog"
	"math"
	"slices"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/alarm"
	"github.com/HuskerMinion/techo5/echod/internal/feature/notify"
	"github.com/HuskerMinion/techo5/echod/internal/lib/locale"
)

// Leaving today's occurrence out, from the light's own page: the engine's rules are in
// feature/alarm/skip.go, and this file is only the way to reach them. Only normal repeating alarms
// are offered (skippableAlarm refuses light-only ones, and a lamp's ring is not ours to leave out).
//
// A finger acts on what it saw: the tap leaves out the occurrence the frame drew, kept beside the
// button's place. A press the engine refuses is still the button's — it must not fall through to the
// clock below and open the microphone — and the device's own note says so.

// skipOffer is one frame's offer to leave an occurrence out: the alarm feature's token exactly as it
// was offered for that frame, and the label of the alarm it names.
type skipOffer struct {
	token alarm.SkipToken
	label string
}

// skippable and skipOccurrence are the alarm feature's, here so that a test can stand in for the
// engine, as sunriseProgress and sunriseHold stand in for the light itself.
var (
	skippable      = func(now time.Time) (alarm.SkipToken, bool) { return alarm.Get().Skippable(now) }
	skipOccurrence = func(id string, at time.Time) error { return alarm.Get().SkipOccurrence(id, at) }
)

// sunriseSkip is what the light's page offers to leave out, or nil: not while the light is held at
// full after an alarm (that occurrence has happened), and only when the light on the screen is that
// very occurrence's ramp — the engine offers the occurrence furthest along, whose ramp another alarm
// or a helper may be showing — so the button never names an alarm this sunrise is not.
func sunriseSkip(now time.Time) *skipOffer {
	if _, _, held := sunriseHold(now); held {
		return nil
	}
	token, ok := skippable(now)
	if !ok {
		return nil
	}
	c := config.Get().Alarms
	i := slices.IndexFunc(c.List, func(al config.Alarm) bool { return al.ID == token.ID })
	if i < 0 {
		return nil
	}
	al := c.List[i]
	window := time.Duration(c.SunriseFor(al)) * time.Minute
	left := token.At.Sub(now)
	if window <= 0 || left <= 0 || left > window {
		return nil
	}
	if p := sunriseProgress(now); math.Abs(p-(1-float64(left)/float64(window))) > 1e-6 {
		return nil
	}
	return &skipOffer{token: token, label: al.Label}
}

// sunriseSkipTap is a tap on the light's page at p: whether it landed on the button that leaves
// today's alarm out. It answers whether the tap was the button's, whatever the alarm feature said
// about the occurrence, so that a press is answered here rather than by the clock's turn under it.
func (d *Display) sunriseSkipTap(p image.Point, now time.Time) bool {
	if d.r == nil {
		return false
	}
	offer, ok := d.r.skipTapped(p)
	if !ok {
		return false
	}
	err := skipOccurrence(offer.token.ID, offer.token.At)
	// The name is the heading (the existing forty bytes of clean title, which cannot swallow the
	// result) and one whole localized sentence carries the result and the moment. No day is claimed:
	// the occurrence offered can be tomorrow's when the light comes up before midnight.
	title := offer.label
	if title == "" {
		title = locale.Alarm(screenLang(), false)
	}
	message := fmt.Sprintf(skipFormat(err == nil), clockText(offer.token.At))
	notify.Get().Note(title, message)
	// At night a card waits for a touch. This press is that touch: only the card it just made (own,
	// with this very sentence) is remembered as seen, so a notification Home Assistant sent that took
	// its place is neither released nor marked seen.
	if up, showing := notify.Get().Showing(); showing && up.Own && up.Message == message {
		d.noticeOut(up.ID, false)
	}
	if err != nil {
		slog.Warn("screen: today's alarm was not left out", "alarm", offer.label, "err", err)
	} else {
		slog.Info("screen: today's alarm left out", "alarm", offer.label,
			"at", offer.token.At.Format(time.RFC3339))
	}
	d.wake()
	return true
}

// skipAction is what the button says a press does, in the screen's catalog's words (lib/locale). The
// action stays short: the alarm's name and hour on the line under it identify the occurrence.
func skipAction() string { return tr("sunrise_skip_action") }

// skipFormat is the screen's sentence for an answer: what was left out, or that it was not, with the
// moment as its one argument. The sentence is the language's own whole, so a language whose word order
// or punctuation is not the English one can say it its own way; English is what a language without its
// own entry falls back to, as the rest of the screen's text does (lib/locale).
func skipFormat(skipped bool) string {
	if skipped {
		return tr("sunrise_skip_done")
	}
	return tr("sunrise_skip_refused")
}

// setSkipDrawn is the frame in hand drawing the button in b for offer, for the frame to publish.
func (r *renderer) setSkipDrawn(b image.Rectangle, offer skipOffer) {
	r.skipDrawn, r.skipDrawnOffer = b, offer
}

// skipOccluded is whether something else is over the light's page where the button goes: a card, a
// strip, the drawer, the volume bar or the update dialog. What cannot be seen cannot be tapped, and
// whoever dismisses a card after the frame was drawn must not find a button under it.
func skipOccluded(s scene) bool {
	return s.showNotification || s.showReminder || s.popup != nil || s.showAnnouncement || s.announceRecording ||
		s.showDrawer || s.showVolume || s.updateActive
}

// rainbowCovering is whether Rainbow is drawing over the page in this frame: a turn's entry, its light,
// its card, or the fade going out after it. The page's own button is left out while it is, and comes
// back on the frame after it is gone. Chosen as the turn style with no turn in hand, Rainbow draws
// nothing and covers nothing.
func (r *renderer) rainbowCovering(s scene) bool {
	return rainbowHere(s) && (r.rainbow.Active() || r.rainbow.Busy())
}

// skipCacheable is whether a frame of the page may be kept under Rainbow: not the light before an
// alarm, and nothing laid over it (skipOccluded). A kept copy of one of those carries a button or a
// card from a frame that is no longer on the screen (rainbowKeyOf, rainbowTurn). The scene is enough:
// a card, the device's own note included, is showNotification.
func skipCacheable(s scene) bool {
	return s.sunrise == 0 && !skipOccluded(s)
}

// publishSkip makes the finished frame what a finger acts on: the button the page drew, with the
// occurrence it drew it for. Registered at the start of draw so it runs after the overlays' own
// deferred draws: a frame whose overlays own that place publishes no button.
func (r *renderer) publishSkip(s scene) {
	r.skipMu.Lock()
	defer r.skipMu.Unlock()
	if skipOccluded(s) || s.sunrise == 0 || r.rainbowCovering(s) {
		r.skipAt, r.skipShown = image.Rectangle{}, skipOffer{}
		return
	}
	r.skipAt, r.skipShown = r.skipDrawn, r.skipDrawnOffer
}

// skipTapped is the offer whose button was drawn at p in the frame last drawn, and whether one was
// there. A finger a little outside the button still counts: it is the only control on the page.
func (r *renderer) skipTapped(p image.Point) (skipOffer, bool) {
	r.skipMu.Lock()
	defer r.skipMu.Unlock()
	if r.skipAt.Empty() || r.skipShown.token.ID == "" || !p.In(r.skipAt.Inset(-r.s(8))) {
		return skipOffer{}, false
	}
	return r.skipShown, true
}

// sunriseSkipButton draws what a press does, and the alarm and moment it would leave out: a dark pill
// between the date and the foot, drawn for a sky that goes from dark red to pale gold. Where that room
// cannot hold both lines (the largest hand-edited text sizes), it draws nothing rather than a button
// over the clock or the foot, so nothing is published and nothing is tappable.
func (r *renderer) sunriseSkipButton(offer skipOffer, p float64) {
	label := offer.label
	if label == "" {
		label = locale.Alarm(screenLang(), false)
	}
	// A pill around both lines, measured from the faces in hand: a screen font of the owner's is not
	// the same height as Go's, and a line that overflows the button it is on reads as a mistake.
	padX, padY, gap := r.s(48), r.s(12), r.s(6)
	am, sm := r.title.Metrics(), r.small.Metrics()
	wide := r.w - 2*r.margin - 2*padX
	if wide <= 0 {
		return
	}
	action := r.clipTo(r.title, skipAction(), wide)
	what := r.skipWhat(label, clockText(offer.token.At), wide)
	w := max(r.width(r.title, action), r.width(r.small, what)) + 2*padX
	tall := am.Ascent.Ceil() + am.Descent.Ceil() + gap + sm.Ascent.Ceil() + sm.Descent.Ceil() + 2*padY
	bottom := r.h - r.s(64)
	// The date under the clock stays clear of the top, and the foot (and a cue's captions) of the bottom.
	if bottom-tall < r.h/2-r.s(56)+r.s(64)+sm.Descent.Ceil()+r.s(4) {
		return
	}
	b := image.Rect((r.w-w)/2, bottom-tall, (r.w+w)/2, bottom)
	rad := min(r.sf(30), float64(b.Dy())/2)
	r.roundFill(b, rad, color.RGBA{0x2b, 0x13, 0x0b, 0xff}, color.RGBA{0x17, 0x09, 0x05, 0xff})
	// The edge is warm against the dark end of the sky and deepens as the sky pales.
	r.roundStroke(b, rad, r.sf(2), lerpRGB(color.RGBA{0xf0, 0xa9, 0x3f, 0xff}, color.RGBA{0x7a, 0x33, 0x12, 0xff}, p))
	titleY := b.Min.Y + padY + am.Ascent.Ceil()
	whatY := titleY + am.Descent.Ceil() + gap + sm.Ascent.Ceil()
	r.text(r.title, action, b.Min.X+(b.Dx()-r.width(r.title, action))/2, titleY, color.RGBA{0xff, 0xf6, 0xe8, 0xff})
	r.text(r.small, what, b.Min.X+(b.Dx()-r.width(r.small, what))/2, whatY, color.RGBA{0xf3, 0xc3, 0x8a, 0xff})
	// A cue's captions go above the page's controls (footAt, turn_cue.go), from the top of the finger's
	// area rather than the pill's pixels: a tap just outside it is still the button's (skipTapped).
	r.footControls(b.Min.Y - r.s(8))
	r.setSkipDrawn(b, offer)
}

// skipWhat is the button's second line: the alarm and the moment. A name has no length limit, so the
// name is clipped to the room beside the moment; the moment is never cut, since it says which
// occurrence a press is about.
func (r *renderer) skipWhat(label, at string, wide int) string {
	moment := " · " + at
	room := wide - r.width(r.small, moment)
	if room <= r.width(r.small, "…") {
		// If even an elided name will not fit, the identifying time takes priority.
		return r.clipTo(r.small, at, wide)
	}
	return r.clipTo(r.small, label, room) + moment
}
