//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"log/slog"
	"math"
	"slices"
	"time"

	"golang.org/x/image/font"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/alarm"
	"github.com/HuskerMinion/techo5/echod/internal/lib/locale"
)

// Leaving today's ring out, from the light's own page.
//
// The page that wakes somebody is the one moment they know which alarm they would rather not hear:
// the light is coming up before it and the alarm is still ahead. So that page offers to leave that
// one occurrence out — the alarm stays on with the same days, and its next morning is its usual one
// (feature/alarm/skip.go, the engine and its rules; this file is only the way to reach it).
//
// The offer is the alarm feature's: the occurrence whose window is furthest along and may be left
// out — an alarm of the device's that repeats, is on and has a wake light — a normal alarm, or a
// light-only one, whose light is all it is, and whose ramp is as much a morning to leave out as any
// other. Two things are asked of it here, because the page says what the sunrise is for. Its window
// has to be open now — an alarm later in the morning, whose light has not begun, is not what somebody
// looking at this sunrise is being woken for — and the light on the screen has to be that very
// occurrence's ramp, since another alarm's window, or a helper's, may be the one further along. Where
// either cannot be said, the page offers nothing rather than a button about an alarm this sunrise is
// not.
//
// A finger acts on what it saw. The occurrence drawn is the one carried by the frame and kept beside
// the button's place, and a tap leaves out exactly that one; if the alarm has been moved or turned
// off since the frame was drawn, the alarm feature refuses it — both are checked again there — and the
// foot of the page says it was not left out. Either answer, the tap is the button's: a press refused
// by an alarm that changed is still a press, and must not fall through to the clock below and open
// the microphone. That answer is drawn dark on pale at the foot, as the button is, since the sky it is
// drawn over ends in a gold that swallows the foot's amber (render_sunrise.go).

// skipOffer is one frame's offer to leave an occurrence out: the alarm feature's token exactly as it
// was offered for that frame, and the label of the alarm it names, for somebody to know which alarm
// and which moment the button is about.
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

// skipNoteFor is how long the foot of the page says what came of a tap on the button.
const skipNoteFor = 8 * time.Second

// skipAnswer is what the foot of the page says a press on the button above it came to: the alarm it
// named, the moment, and whether that occurrence was left out. It is kept as the answer was given and
// not as a finished line: the foot measures the line when it draws it, where the face and the room it
// has are known, and only the alarm's own name — the one part with no length limit — is fitted
// (skipLine).
type skipAnswer struct {
	label   string
	at      time.Time
	skipped bool
}

// exists is whether the page has an answer to show: an answer is about a moment, and a moment is never
// the zero one.
func (a skipAnswer) exists() bool { return !a.at.IsZero() }

// name is the alarm's own label, or the word for one where the alarm has none.
func (a skipAnswer) name() string {
	if a.label == "" {
		return locale.Alarm(screenLang(), false)
	}
	return a.label
}

// sunriseSkip is what the light's page offers to leave out, or nil. Only while the light is coming up
// before the alarm: once it is held at full after one, the occurrence ahead is not what is being
// waited for, and leaving it out from this page would read as leaving out the ring that just happened.
//
// The light on the page has to be that alarm's own ramp. The alarm feature offers the occurrence whose
// window is furthest along, which is not necessarily one whose ramp is the whole of the screen —
// another alarm's may be further along still, or a helper's — and a button about an alarm the sunrise
// is not would be a trap. So the light the screen is showing is compared with the ramp this occurrence
// would make at this moment, and where they are not the same the page offers nothing.
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
	d.mu.Lock()
	d.skipNote = skipAnswer{label: offer.label, at: offer.token.At, skipped: err == nil}
	d.skipNoteUntil = now.Add(skipNoteFor)
	d.mu.Unlock()
	if err != nil {
		slog.Warn("screen: today's alarm was not left out", "alarm", offer.label, "err", err)
	} else {
		slog.Info("screen: today's alarm left out", "alarm", offer.label,
			"at", offer.token.At.Format(time.RFC3339))
	}
	d.wake()
	return true
}

// skipAction is what the button says a press does, in the screen's catalog's words (lib/locale): the
// three messages are whole, with the alarm and the hour as their two arguments, so that a language
// whose word order or punctuation is not the English one can say it its own way rather than build a
// line out of English fragments. English is what a language without its own entry falls back to, as
// the rest of the screen's text does.
//
// The action stays short: the alarm's name and hour on the line under it identify the occurrence.
// That occurrence can be tomorrow's when the light comes up before midnight; the action claims no day.
func skipAction() string { return tr("sunrise_skip_action") }

// skipLineFor is one whole line from the screen's catalog: the language's own sentence for what came
// of a press, with the alarm and the moment as its two arguments. The sentence is the language's own
// whole and may put its arguments in its own order (%[2]s … %[1]s), so it is filled in rather than
// built out of fragments; English is what a language without its own entry falls back to, as the rest
// of the screen's text does (lib/locale). Nothing here fits anything: that is skipLine's, where the
// face and the room are known.
//
// The line says nothing about the light, which may stay a sunrise for another alarm whose window is
// open or may end with this one: neither is what was promised. Nor does it say which day the moment is
// on: the occurrence offered is the next one, which is not always today's, and a line that said so
// would be wrong in the small hours.
//
// It is filled in from the catalog's whole sentence and its two arguments, in the order the sentence
// names them, so a language whose word order or punctuation is not the English one can say it its own
// way rather than have a line built out of English fragments.
func skipLineIn(format, label string, at time.Time) string {
	return fmt.Sprintf(format, label, clockText(at))
}

// skipFormat is the screen's sentence for an answer: what was left out, or that it was not.
func skipFormat(skipped bool) string {
	if skipped {
		return tr("sunrise_skip_done")
	}
	return tr("sunrise_skip_refused")
}

// skipWhole is the foot's answer as wide as it needs, with the alarm named: the line is shortened to
// the foot it is drawn in by skipLine, and a test that reads the words reads them here.
func skipWhole(a skipAnswer) string { return skipLineIn(skipFormat(a.skipped), a.name(), a.at) }

// skipLine is the foot's answer fitted to the face it is drawn in and to wide.
func (r *renderer) skipLine(a skipAnswer, face font.Face, wide int) string {
	return r.skipFitted(skipFormat(a.skipped), a.name(), a.at, face, wide)
}

// skipFitted is a whole catalog sentence with its label fitted to wide in face: the alarm's name made
// shorter where the name is what does not fit. The result and the hour are never cut: they say what
// happened and when, and the name is the part of a line with no length limit. A name is never walked a
// letter at a time — a Home Assistant name has no length limit — and the longest that fits is found by
// halving (clipRunes). Where even a bare ellipsis for the name does not fit, the sentence itself is
// wider than the room it is drawn in; the screen's own catalogs are not that wide on the panels the
// foot is drawn on, and the whole line is a better answer than an hour cut off it.
func (r *renderer) skipFitted(format, label string, at time.Time, face font.Face, wide int) string {
	line := skipLineIn(format, label, at)
	if r.width(face, line) <= wide {
		return line
	}
	if r.width(face, skipLineIn(format, "…", at)) > wide {
		return line
	}
	name := clipRunes(label, 0, func(name string) bool {
		return r.width(face, skipLineIn(format, name, at)) <= wide
	})
	return skipLineIn(format, name, at)
}

// skipNoteNow is the foot of the page's answer to the last tap, while it lasts.
func (d *Display) skipNoteNow(now time.Time) skipAnswer {
	d.mu.Lock()
	defer d.mu.Unlock()
	if now.After(d.skipNoteUntil) {
		return skipAnswer{}
	}
	return d.skipNote
}

// setSkipDrawn is the frame in hand drawing the button in b for offer, for the frame to publish.
func (r *renderer) setSkipDrawn(b image.Rectangle, offer skipOffer) {
	r.skipDrawn, r.skipDrawnOffer = b, offer
}

// skipOccluded is whether this frame has something else over the light's page where the button goes: a
// notification, a reminder or an event's pop-up card, an announcement or the strip that says this
// microphone is open, the drawer, the volume bar or the update dialog. What cannot be seen cannot be
// tapped, so the page leaves the button out and the frame publishes none — a frame going away is not
// the page's to keep a press for, either: whoever dismisses a card in the middle of a frame that has
// already been drawn must not find a button under it that they never saw.
func skipOccluded(s scene) bool {
	return s.showNotification || s.showReminder || s.popup != nil || s.showAnnouncement || s.announceRecording ||
		s.showDrawer || s.showVolume || s.updateActive
}

// footOccluded is whether something in this frame covers the foot the footer's words are drawn on: the
// strips along the bottom, the drawer, the volume bar or the update dialog.
func footOccluded(s scene) bool {
	return s.showAnnouncement || s.announceRecording || s.showDrawer || s.showVolume || s.updateActive
}

// publishFrame makes the finished frame what a finger acts on: the page's button with the occurrence it
// drew, and whether the footer drew the word for the music. It is registered at the start of draw,
// before the overlays' own deferred draws, so it runs after them and sees them: a frame whose overlays
// own the foot and the place of the button publishes neither, whatever the page under them drew, and a
// button or a word left out of the frame is not one the touch handler can find.
func (r *renderer) publishFrame(s scene) {
	r.skipMu.Lock()
	defer r.skipMu.Unlock()
	if skipOccluded(s) {
		r.skipAt, r.skipShown = image.Rectangle{}, skipOffer{}
	} else {
		r.skipAt, r.skipShown = r.skipDrawn, r.skipDrawnOffer
	}
	r.musicShown = r.musicDrawn && !footOccluded(s)
}

// musicWordShown is whether the frame last drawn put the footer's word for the music on the screen,
// which is the only time that corner is the way back to the page.
func (r *renderer) musicWordShown() bool {
	r.skipMu.Lock()
	defer r.skipMu.Unlock()
	return r.musicShown
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
