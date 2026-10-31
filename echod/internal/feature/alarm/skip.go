package alarm

import (
	"errors"
	"fmt"
	"log/slog"
	"math/bits"
	"slices"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Leaving one occurrence out. A recurring alarm with a wake light sometimes should not wake anybody
// once — a day off, a night away — without being turned off or moved: what is left out is that one
// moment, by the alarm's id and the instant it would have rung. The alarm stays on with the same
// days, and its next occurrence after the marked one is its usual one. The mark is saved before the
// request is answered, so a restart, a clock set or a write that failed cannot quietly bring the
// occurrence back.
//
// It is only while the light is coming up before that occurrence that it can be left out: that is the
// moment somebody knows which alarm they would rather not hear, and it is the window the screen offers
// the choice in (feature/display). Past the alarm's own time the occurrence has begun and cannot be
// left out any more.
//
// A mark is kept until its alarm is deleted, when nothing is left for it to leave out. It is not
// dropped once its moment has gone by: a look, or the time the device was away, may still ask about
// that moment afterwards, and a mark dropped between the occurrence and that look would ring it and
// call it missed. The list is one small entry, an id and an instant, per occurrence somebody chose
// not to be woken by; only a request writes to it, so it grows with those choices and nothing else.

// SkipToken names the occurrence to leave out: the alarm's id and the moment it would ring, as shown
// to whoever asks. Both are checked again when it is used, so a token from a list that has changed
// since does not leave out another morning.
type SkipToken struct {
	ID string    `json:"id"`
	At time.Time `json:"at"`
}

// errAlreadySkipped says the same occurrence is already left out, and carries no new change: the
// store should not be written again for a request that already holds.
var errAlreadySkipped = errors.New("alarms: occurrence already left out")

// Skippable is the occurrence that may be left out now: an alarm of the device's that repeats, is on,
// is not a reminder or silent, has a wake light, and whose own window is open at now — the light is
// already coming up before it. A light-only alarm is that light and no more, so its own ramp is a
// morning somebody may leave out as well: what is left out then is that morning's light, and its time
// does not end the night either. An alarm whose light has not begun, whose time has come, or one
// merely beside another alarm's window, is not offered: what is left out is the occurrence the room is
// being lit for. Helpers followed from Home Assistant and snoozes are not candidates: neither is an
// alarm set on the device.
//
// With more than one of those windows open it is the ramp furthest along that is offered — the one the
// room is mostly lit by, the least of its own window left — so that the occurrence offered is the
// light on the screen as somebody sees it (feature/display compares the two). Windows of different
// lengths do not compare by their start: a short light well under way is further along than a long one
// that has only just begun. Two evenly far along are told apart by the earlier occurrence, and two at
// the same moment by the lower id, so the answer does not flicker between them or with the order of
// the list.
func (a *Alarms) Skippable(now time.Time) (SkipToken, bool) {
	if !clockSet(now) {
		return SkipToken{}, false
	}
	c := config.Get().Alarms
	var best ramp
	found := false
	for _, al := range c.List {
		at, ok := skippable(c, al, now)
		if !ok {
			continue
		}
		r := ramp{left: at.Sub(now), window: time.Duration(c.SunriseFor(al)) * time.Minute, at: at, id: al.ID}
		if !found || r.dominant(best) {
			best, found = r, true
		}
	}
	return SkipToken{ID: best.id, At: best.at}, found
}

// ramp is one occurrence whose light is coming up now, as the choice between them sees it: how much of
// its own window is left, the window's length, and the occurrence it belongs to.
type ramp struct {
	left, window time.Duration
	at           time.Time
	id           string
}

// dominant is whether r is the occurrence to offer rather than best: the window further along, which
// is the lesser part of it left, and at an exact tie the earlier occurrence, then the lower id.
func (r ramp) dominant(best ramp) bool {
	if leftIsLess(r.left, r.window, best.left, best.window) {
		return true
	}
	if leftIsLess(best.left, best.window, r.left, r.window) {
		return false
	}
	if !r.at.Equal(best.at) {
		return r.at.Before(best.at)
	}
	return r.id < best.id
}

// leftIsLess is whether l1 of w1 is a smaller part of its window than l2 of w2, exactly: the left parts
// and the windows are nanosecond counts whose products are wider than an int64, so they are compared
// as 128 bits rather than through a float. A window with nothing left is the furthest along.
func leftIsLess(l1, w1, l2, w2 time.Duration) bool {
	hi1, lo1 := bits.Mul64(uint64(l1), uint64(w2))
	hi2, lo2 := bits.Mul64(uint64(l2), uint64(w1))
	return hi1 < hi2 || (hi1 == hi2 && lo1 < lo2)
}

// skippableAlarm is whether al may have an occurrence left out at all: the kind of alarm, and a wake
// light that is on, not that its window is open yet. Alarms off (Alarms.AllOff) makes every one of them
// not offered and not accepted: none of them rings until they are on again, so there is no occurrence
// to leave out, and a mark from a screen that has not caught up with the switch is refused as stale.
func skippableAlarm(c config.Alarms, al config.Alarm) bool {
	return !c.AllOff && al.On && al.Days != config.DaysOnce && !al.Remind && !al.Silent && c.SunriseFor(al) > 0
}

// skippable is when al is next to ring, and whether that occurrence may be left out now: the alarm
// is the kind that may have one left out, and its own wake light is coming up for that very
// occurrence. Its existing marks are stepped over, so the time is the occurrence that would actually
// ring.
func skippable(c config.Alarms, al config.Alarm, now time.Time) (time.Time, bool) {
	at, ok := nextSkippable(c, al, now)
	return at, ok && inWindow(c.SunriseFor(al), at, now)
}

// nextSkippable is the occurrence of al that would ring from now on, existing marks stepped over,
// and whether al is the kind of alarm an occurrence may be left out of at all. Its window is not
// asked about: that is what skippable adds.
func nextSkippable(c config.Alarms, al config.Alarm, now time.Time) (time.Time, bool) {
	if !skippableAlarm(c, al) {
		return time.Time{}, false
	}
	return localSource(c, al).next(now)
}

// inWindow is whether now is inside the light before at: after the alarm's own window opened and
// before its time. The first moment of the window counts; the moment it rings does not, because the
// occurrence has begun then and the scheduler's gate is what an offer made a moment earlier is
// checked at.
func inWindow(sunrise int, at, now time.Time) bool {
	if sunrise <= 0 {
		return false
	}
	left := at.Sub(now)
	return left > 0 && left <= time.Duration(sunrise)*time.Minute
}

// SkipOccurrence leaves out the one occurrence of alarm id at at, as offered by Skippable. Both are
// checked against the alarm as it is now — still the same kind of alarm, and still its next
// occurrence — and the mark is written down before this reports success: an occurrence somebody was
// told would not ring must not ring because the mark could only be kept in memory.
func (a *Alarms) SkipOccurrence(id string, at time.Time) error {
	return a.leaveOut(SkipToken{ID: id, At: at}, time.Now)
}

// skipOccurrence is SkipOccurrence with the moment it is asked at, for tests and for a caller that
// already has one.
func (a *Alarms) skipOccurrence(token SkipToken, now time.Time) error {
	return a.leaveOut(token, func() time.Time { return now })
}

// leaveOut is SkipOccurrence with the clock read once the scheduler's gate is held: a request that
// waited for a ring to finish must be checked against the time it is processed, not the time it
// arrived, or it would report a morning that has just rung as left out.
func (a *Alarms) leaveOut(token SkipToken, now func() time.Time) error {
	a.editMu.Lock()
	at := now()
	if !clockSet(at) {
		a.editMu.Unlock()
		return errors.New("alarms: clock is not set; cannot leave out an occurrence")
	}

	err := config.Set().Alarms().ChangeStrict(func(c *config.Alarms) error {
		i := slices.IndexFunc(c.List, func(x config.Alarm) bool { return x.ID == token.ID })
		if i < 0 {
			return fmt.Errorf("alarms: no alarm %.64q to leave out", token.ID)
		}
		al := c.List[i]
		if !skippableAlarm(*c, al) {
			return fmt.Errorf("alarms: %q has no occurrence that can be left out", al.Label)
		}
		// The same request already holds. Answer it as done, without a second mark or another write:
		// a double tap or a retry after the file was saved must not become an error.
		if hasSkip(*c, token.ID, token.At) {
			return errAlreadySkipped
		}
		// The token is checked against the alarm and against the light as they are at the moment the
		// gate is held, not against what was shown: an offer whose alarm has been moved, turned off
		// or has begun to come up since must not leave another occurrence out.
		next, ok := nextSkippable(*c, al, at)
		if !ok || !next.Equal(token.At) {
			shown := "never"
			if ok {
				shown = next.Format(time.RFC3339)
			}
			return fmt.Errorf("alarms: %q next rings at %s, not at the time shown (%s); ask again",
				al.Label, shown, token.At.Format(time.RFC3339))
		}
		if !inWindow(c.SunriseFor(al), next, at) {
			return fmt.Errorf("alarms: %q is not coming up now, so %s cannot be left out; ask again",
				al.Label, next.Format(time.RFC3339))
		}
		// Kept until the alarm goes, never dropped as its moment passes: a look or an absence may
		// still ask about that moment afterwards, and a mark taken away in between would let the
		// occurrence ring and be called missed.
		c.Skipped = append(c.Skipped, config.Skip{ID: token.ID, At: next})
		return nil
	})
	if errors.Is(err, errAlreadySkipped) {
		err = nil
	}
	if err != nil {
		a.editMu.Unlock()
		return err
	}

	// A light already held for this very occurrence must not wait for it now: a lit ring that ended
	// while its window was open left the light up, and leaving the occurrence out lets it go.
	a.mu.Lock()
	letGo := a.bridge.key == token.ID && a.bridge.at.Equal(token.At)
	if letGo {
		a.bridge = bridge{}
	}
	a.mu.Unlock()
	if letGo {
		a.saveBridge()
		slog.Info("wake light held for an occurrence no longer coming let go", "id", token.ID)
	}
	a.editMu.Unlock()

	slog.Info("alarm occurrence left out", "id", token.ID, "at", token.At.Format(time.RFC3339))
	a.poke()
	a.Changed.Emit(struct{}{})
	return nil
}

// hasSkip is whether that exact occurrence is already left out.
func hasSkip(c config.Alarms, id string, at time.Time) bool {
	for _, s := range c.Skipped {
		if s.ID == id && s.At.Equal(at) {
			return true
		}
	}
	return false
}
