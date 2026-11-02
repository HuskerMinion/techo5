package wifiquiet

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// world is what the component sees, set by the test.
type world struct {
	now             time.Time
	pauses, resumes int
	failPause       bool
	failResume      bool
	saved           []config.WifiQuiet
	offNetwork      bool // what the supplicant was last told
}

func (w *world) quiet(c config.WifiQuiet) *Quiet {
	q := New()
	q.now = func() time.Time { return w.now }
	q.available = func() bool { return true }
	q.pause = func(context.Context) error {
		if w.failPause {
			return errors.New("wpa_cli disconnect: no supplicant")
		}
		w.pauses++
		w.offNetwork = true
		return nil
	}
	q.resume = func(context.Context) error {
		if w.failResume {
			return errors.New("wpa_cli reconnect: timed out")
		}
		w.resumes++
		w.offNetwork = false
		return nil
	}
	q.save = func(c config.WifiQuiet) error { w.saved = append(w.saved, c); return nil }
	q.Restore(config.Config{WifiQuiet: c})
	return q
}

func at(day int, hh, mm int) time.Time {
	return time.Date(2026, time.November, day, hh, mm, 0, 0, time.Local)
}

var night = config.WifiQuiet{On: true, Hours: "01:00-07:00"}

// The night as it goes: off the network at the start, back at the end, and only once each.
func TestOffAtTheStartBackAtTheEnd(t *testing.T) {
	w := &world{now: at(2, 22, 0)}
	q := w.quiet(night)
	ctx := context.Background()

	q.look(ctx) // the first look after a start, outside the hours: let on, which changes nothing
	if w.pauses != 0 || w.resumes != 1 {
		t.Fatalf("first look outside the hours: pauses %d resumes %d, want 0 1", w.pauses, w.resumes)
	}
	for _, tm := range []time.Time{at(3, 0, 59), at(3, 1, 0), at(3, 3, 0), at(3, 6, 59)} {
		w.now = tm
		q.look(ctx)
	}
	if w.pauses != 1 || !w.offNetwork {
		t.Fatalf("in the hours: pauses %d off %v, want 1 true", w.pauses, w.offNetwork)
	}
	w.now = at(3, 7, 0)
	q.look(ctx)
	w.now = at(3, 7, 30)
	q.look(ctx)
	if w.resumes != 2 || w.offNetwork {
		t.Fatalf("after the hours: resumes %d off %v, want 2 false", w.resumes, w.offNetwork)
	}
}

// Hours that do not cross midnight are kept just the same.
func TestHoursWithinTheDay(t *testing.T) {
	w := &world{now: at(2, 12, 0)}
	q := w.quiet(config.WifiQuiet{On: true, Hours: "13:30-14:00"})
	ctx := context.Background()
	q.look(ctx)
	w.now = at(2, 13, 45)
	q.look(ctx)
	if !w.offNetwork {
		t.Fatal("13:45 is in 13:30-14:00")
	}
	w.now = at(2, 14, 0)
	q.look(ctx)
	if w.offNetwork {
		t.Fatal("14:00 is after 13:30-14:00")
	}
}

// Somebody who joins a network by hand in the hours keeps it: the device is not taken off again.
func TestAJoinByHandIsKept(t *testing.T) {
	w := &world{now: at(3, 1, 0)}
	q := w.quiet(night)
	ctx := context.Background()
	q.look(ctx)
	if w.pauses != 1 {
		t.Fatalf("a start in the hours: pauses %d, want 1", w.pauses)
	}
	w.offNetwork = false // the Wi-Fi page joined a network
	for m := 1; m < 60; m++ {
		w.now = at(3, 2, m)
		q.look(ctx)
	}
	if w.pauses != 1 || w.offNetwork {
		t.Fatalf("after a join by hand: pauses %d off %v, want 1 false", w.pauses, w.offNetwork)
	}
}

// Off, the device is never taken off the network; turned off in the hours, it is let back on at once.
func TestOffLeavesTheWifiAlone(t *testing.T) {
	w := &world{now: at(3, 2, 0)}
	q := w.quiet(config.WifiQuiet{Hours: "01:00-07:00"})
	ctx := context.Background()
	q.look(ctx)
	if w.pauses != 0 {
		t.Fatalf("off: pauses %d, want 0", w.pauses)
	}

	q = w.quiet(night)
	q.look(ctx)
	if !w.offNetwork {
		t.Fatal("on, at 02:00: want off the network")
	}
	q.on.OnCommand(false)
	q.look(ctx) // what the kick does in Run
	if w.offNetwork {
		t.Fatal("turned off in the hours: want back on the network at once")
	}
	if n := len(w.saved); n == 0 || w.saved[n-1].On {
		t.Fatalf("turning it off was not saved: %+v", w.saved)
	}
}

// A clock that has not been set yet keeps the device on: the hours are only kept by one that knows
// the time.
func TestUnsetClockKeepsItOn(t *testing.T) {
	w := &world{now: time.Date(2010, time.January, 1, 3, 0, 0, 0, time.Local)}
	q := w.quiet(night)
	q.look(context.Background())
	if w.pauses != 0 || w.offNetwork {
		t.Fatalf("2010 at 03:00: pauses %d off %v, want 0 false", w.pauses, w.offNetwork)
	}
}

// A pause that fails is tried again at the next look rather than given up for the night.
func TestAFailedPauseIsTriedAgain(t *testing.T) {
	w := &world{now: at(3, 1, 0), failPause: true}
	q := w.quiet(night)
	ctx := context.Background()
	q.look(ctx)
	w.failPause = false
	w.now = at(3, 1, 1)
	q.look(ctx)
	if w.pauses != 1 || !w.offNetwork {
		t.Fatalf("after a failed pause: pauses %d off %v, want 1 true", w.pauses, w.offNetwork)
	}
}

// A failed resume at the first look after a start, out of the hours, is tried again: a device stopped in
// the night and started after it must not stay off the network.
func TestAFailedFirstResumeIsTriedAgain(t *testing.T) {
	w := &world{now: at(3, 8, 0), failResume: true, offNetwork: true}
	q := w.quiet(night)
	ctx := context.Background()
	q.look(ctx)
	w.failResume = false
	w.now = at(3, 8, 1)
	q.look(ctx)
	if w.resumes != 1 || w.offNetwork {
		t.Fatalf("after a failed first resume: resumes %d off %v, want 1 false", w.resumes, w.offNetwork)
	}
}

// The start and the end are set to the quarter hour; the same time for both is refused.
func TestSettingTheHours(t *testing.T) {
	w := &world{now: at(2, 12, 0)}
	q := w.quiet(night)
	q.from.OnCommand("23:30")
	q.to.OnCommand("06:15")
	if got := w.saved[len(w.saved)-1].Hours; got != "23:30-06:15" {
		t.Fatalf("hours saved %q, want 23:30-06:15", got)
	}
	before := len(w.saved)
	q.to.OnCommand("23:30")
	if len(w.saved) != before {
		t.Fatal("a start and end at the same time was saved")
	}
	if q.to.Get() != "06:15" {
		t.Fatalf("the end shows %q after a refused change, want 06:15", q.to.Get())
	}
}

// Nothing set yet: the default hours.
func TestDefaultHours(t *testing.T) {
	from, to := config.WifiQuiet{}.Window()
	if quarterText(from) != "01:00" || quarterText(to) != "07:00" {
		t.Fatalf("default %s-%s, want 01:00-07:00", quarterText(from), quarterText(to))
	}
}
