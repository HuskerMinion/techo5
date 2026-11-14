//go:build !dot && !spot

package display

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"log/slog"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/deck"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
	"github.com/HuskerMinion/techo5/echod/internal/lib/fonts"
)

// TECHO5 Deck's page (render_deck.go): opened by a swipe up from the bottom edge of the clock, once a
// deck is set up, or by Tap on the clock set to Deck. It stays until a swipe down, or a tap on Clock at
// its foot, puts it away.

// bottomEdge is the strip along the bottom of the clock a swipe up has to start in to open the deck,
// in the layout's sizes; above it a swipe up is the volume, as it always was. Thinner than the top
// edge's band, so the volume keeps most of the screen.
const bottomEdge = 90

// A press shows on its button for as long as it is going out, and what came of it after: the pressed
// look until the press is answered, which with a computer that is slow or gone can be seconds, and at
// least deckPressMin so one answered at once is still seen; then, failed, the red look for deckFailedFor
// with the reason at the foot of the page. The look used to last a fixed six tenths of a second
// whatever the press was doing, and a failure was six tenths of a second of red with no word why.
const (
	deckPressMin  = 200 * time.Millisecond
	deckFailedFor = 2 * time.Second
)

// deckPress is a press being shown on its button.
type deckPress struct {
	page, button int
	seq          uint64    // which press it is: the answer to an earlier one is not this one's
	going        bool      // sent, and not answered yet
	shown        time.Time // the pressed look stays until then at least
	until        time.Time // failed: the red look and the reason stay until then
	failed       bool
	reason       string // failed: why, for the foot of the page
}

// pressed and failedAt are the two looks at now.
func (p deckPress) pressed(now time.Time) bool  { return p.going || (!p.failed && now.Before(p.shown)) }
func (p deckPress) failedAt(now time.Time) bool { return !p.going && p.failed && now.Before(p.until) }

// deckDo sends a press; a variable for the tests.
var deckDo = func(page, i int) error { return deck.Get().Press(page, i) }

// deckReason is why a press of b failed, in a line for the foot of the page: the kind of failure and
// whose it was, never the error's own words.
func deckReason(b config.DeckButton, err error) string {
	switch deck.Why(b, err) {
	case deck.FailedOBSAway:
		return tr("deck_obs_away")
	case deck.FailedOBSRefused:
		return tr("deck_obs_refused")
	case deck.FailedOBSSilent:
		return tr("deck_obs_silent")
	case deck.FailedComputerSilent:
		return fmt.Sprintf(tr("deck_pc_silent"), b.Computer)
	case deck.FailedComputerUnpaired:
		return fmt.Sprintf(tr("deck_pc_unpaired"), b.Computer)
	case deck.FailedComputerKey:
		return fmt.Sprintf(tr("deck_pc_key"), b.Computer)
	}
	return tr("deck_failed")
}

// fromBottomEdge is whether a swipe up starting at y starts in the deck's strip.
func (d *Display) fromBottomEdge(y int) bool {
	return d.r != nil && y >= d.r.h-d.r.s(bottomEdge)
}

// openDeck puts the deck up, if there is one.
func (d *Display) openDeck() bool {
	if !deck.Get().Set() {
		return false
	}
	d.mu.Lock()
	// On the screen from now, not from the next frame: the swipe that opened it is still sending its
	// steps up, and they are the deck's to ignore, not the volume's.
	// The count of presses goes on: one still going out from before must not be taken for the next.
	d.deckUp, d.deckOnScreen, d.deckPress = true, true, deckPress{seq: d.deckPress.seq}
	d.closeAlert()
	d.calUntil, d.weatherUntil = time.Time{}, time.Time{}
	d.sheet, d.dash, d.drawer = false, false, false
	pages := len(config.Get().Deck.Pages)
	if d.deckPage >= pages {
		d.deckPage = 0
	}
	d.mu.Unlock()
	slog.Info("deck up")
	d.wake()
	return true
}

func (d *Display) closeDeck() {
	d.mu.Lock()
	d.deckUp = false
	d.mu.Unlock()
	d.wake()
}

// deckShowing is whether the deck page is on the screen now: up, and drawn in the last frame rather
// than under something else.
func (d *Display) deckShowing() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.deckUp && d.deckOnScreen && pageIdle(d.view.Phase)
}

// onClock is whether the clock itself is the page: not now playing, not the weather.
func (d *Display) onClock() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return !d.showingPlaying && !time.Now().Before(d.weatherUntil)
}

// deckFrameKey is what a frame of the deck shows, or "" when the frame isn't just the deck: two frames
// with the same key look the same.
func deckFrameKey(s scene, ringing, calling bool) string {
	// The update dialog is drawn over the deck, and its progress changes every frame, so a frame with
	// it up is not the unchanged deck: otherwise the dialog is skipped while the deck is still there
	// to take the taps that dismiss it, and the screen looks normal while ignoring every touch.
	if !s.showDeck || s.showVideo || s.showVideoAsk || s.showVolume || ringing || calling || s.setupAsking || s.showAnnouncement ||
		s.showReminder || s.showNotification || s.popup != nil || s.bt.Pairing || s.pin.open || s.redClock || s.announceRecording || s.updateActive ||
		s.cue != "" {
		return ""
	}
	// The colors too: the deck is drawn in the theme's, and a change from the setup page (a custom
	// palette's included) should show. So should a change of font, of the owner's own fonts, or of
	// the text size.
	return fmt.Sprintf("%+v|%v|%s|%v|%s|%d|%d", s.deck, s.muted, s.linkDown, [5]color.RGBA{walnut, amber, cream, dim, ember},
		drawnFont(), fonts.Generation(), textSize())
}

// deckScene fills in what the deck page shows, when it is up.
func (d *Display) deckScene(s *scene, now time.Time) {
	// The pages asked for while the deck is up are drawn over it, and have the touches.
	over := s.showSheet || s.showDrawer || s.showCamera || s.showWifi || s.showAlert || s.showCalendar ||
		s.showWeather || s.showRadar || s.bt.Pairing
	d.mu.Lock()
	up := d.deckUp && s.phase == "idle" && !over
	d.deckOnScreen = up
	press := d.deckPress
	d.mu.Unlock()
	if !up {
		return
	}
	cfg := config.Get().Deck
	// Pages taken away on the setup page while the deck was up on one of them.
	pages := max(len(cfg.Pages), 1)
	d.mu.Lock()
	if d.deckPage >= pages {
		d.deckPage = pages - 1
	}
	page := d.deckPage
	d.mu.Unlock()
	if !cfg.Set() {
		// Every button taken off it from the setup page while it was up.
		d.closeDeck()
		return
	}
	cols, rows := cfg.Grid()
	st := deck.Get().OBS()
	pcs := deck.Get().Computers()
	v := deckView{cols: cols, rows: rows, page: page, pages: pages,
		connected: st.Connected, problem: st.Problem, obsSet: cfg.OBS.Addr != "", away: deck.Away(cfg, page, pcs)}
	if press.failedAt(now) {
		v.reason = press.reason
	}
	for i := range cols * rows {
		b := cfg.Button(page, i)
		lit, known := deck.Lit(b, st, pcs)
		bv := deckButtonView{empty: b.Action == config.DeckNone, label: deck.Label(b), icon: deck.Icon(b, lit),
			color: b.Color, lit: lit, known: known}
		if press.page == page && press.button == i {
			bv.pressed, bv.failed = press.pressed(now), press.failedAt(now)
		}
		v.buttons = append(v.buttons, bv)
	}
	s.showDeck, s.deck = true, v
}

// deckGesture is a finger on the deck page: a tap presses a button, a swipe left or right turns the
// page, a swipe down puts the deck away. Swipes up do nothing: the swipe that opened it keeps
// reporting notches until the finger lifts.
func (d *Display) deckGesture(g touch.Gesture) {
	switch g.Kind {
	case touch.Tap:
		if d.r == nil {
			return
		}
		if d.r.deckCloseHit(image.Pt(g.X, g.Y)) {
			d.closeDeck()
			return
		}
		i, ok := d.r.deckHit(image.Pt(g.X, g.Y))
		if !ok {
			return
		}
		d.pressDeck(i)
	case touch.SwipeLeft, touch.SwipeRight:
		pages := max(len(config.Get().Deck.Pages), 1)
		d.mu.Lock()
		if g.Kind == touch.SwipeLeft {
			d.deckPage = min(d.deckPage+1, pages-1)
		} else {
			d.deckPage = max(d.deckPage-1, 0)
		}
		d.mu.Unlock()
	case touch.SwipeDown:
		d.closeDeck()
	}
}

// pressDeck sends a press of button i on the page that is up, and shows it on the button until it is
// answered. A button whose press is still going out takes no second one: it would be sent behind the
// first, and a toggle pressed twice by an impatient finger ends where it began.
func (d *Display) pressDeck(i int) {
	now := time.Now()
	d.mu.Lock()
	page := d.deckPage
	if p := d.deckPress; p.going && p.page == page && p.button == i {
		d.mu.Unlock()
		return
	}
	seq := d.deckPress.seq + 1
	d.deckPress = deckPress{page: page, button: i, seq: seq, going: true, shown: now.Add(deckPressMin)}
	d.mu.Unlock()
	d.wake()
	go func() {
		err := deckDo(page, i)
		failed := err != nil && !errors.Is(err, deck.ErrEmpty)
		var reason string
		if failed {
			reason = deckReason(config.Get().Deck.Button(page, i), err)
		}
		answered := time.Now()
		d.mu.Lock()
		// Only the press the button is showing: one tapped since has the button's look now.
		ours := d.deckPress.seq == seq
		if ours {
			d.deckPress.going = false
			if failed {
				d.deckPress.failed, d.deckPress.until, d.deckPress.reason = true, answered.Add(deckFailedFor), reason
			}
		}
		left := d.deckPress.shown.Sub(answered)
		d.mu.Unlock()
		if !ours {
			return
		}
		d.wake()
		// And once more when the look is over, so the button is drawn plain again.
		switch {
		case failed:
			time.AfterFunc(deckFailedFor+50*time.Millisecond, d.wake)
		case left > 0:
			time.AfterFunc(left+50*time.Millisecond, d.wake)
		}
	}()
}

// deckChanged redraws the page when OBS's state changes under it.
func (d *Display) deckChanged(struct{}) {
	if d.deckShowing() {
		d.wake()
	}
}
