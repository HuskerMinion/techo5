package timer

import (
	"cmp"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"
)

// next is the timer Home Assistant can count down to: when it ends, as a timestamp a dashboard can
// count down to and an automation can trigger on, and which timer that is. One pair rather than one
// per timer, since one at a time is the usual case; Timers and alarms_list cover several at once.
//
// A stopgap: Home Assistant's timer_list domain (home-assistant/architecture#1407) is one entity
// holding any number of timers, and once ESPHome can carry it, it may replace these.
type next struct {
	ends  *esphome.TextSensor
	label *esphome.TextSensor
}

func newNext() next {
	n := next{
		// A text sensor, as Next scheduled alarm is: a sensor's float32 holds a date only to the nearest
		// minutes.
		ends: &esphome.TextSensor{
			Base:        esphome.Base{ObjectID: "next_timer_ends", Name: "Next timer ends", Icon: "mdi:timer-sand"},
			DeviceClass: "timestamp",
		},
		label: &esphome.TextSensor{
			Base: esphome.Base{ObjectID: "next_timer", Name: "Next timer", Icon: "mdi:timer-outline"},
		},
	}
	// None, rather than unknown, until a timer is set.
	n.ends.Set("")
	n.label.Set("")
	return n
}

// nextText is what the pair says: when the next timer ends and what it is called.
type nextText struct{ ends, label string }

// nextTimer is the running timer that ends soonest. With none running, it is the paused one with the
// least left, which has no moment it ends at: its end is empty and its label says what it has left.
// Silent timers are left out: what they show is a wait that is Home Assistant's own. So is a running
// timer already at zero, one of Home Assistant's whose finishing event has yet to arrive: its end has
// gone by, and is not one to count down to.
//
// The timers are read under the lock: a pause or a change from Home Assistant writes to them under it
// while this runs. It takes the moment rather than reading the clock, so what it says can be tested
// without waiting.
func (t *Timers) nextTimer(now time.Time) nextText {
	t.mu.Lock()
	var pick *timer
	for _, c := range t.held {
		if c.silent || c.active && c.remaining(now) <= 0 {
			continue
		}
		if pick == nil || sooner(c, pick, now) {
			pick = c
		}
	}
	var c timer
	if pick != nil {
		c = *pick
	}
	t.mu.Unlock()

	if pick == nil {
		return nextText{}
	}
	name := cmp.Or(c.name, "Timer")
	if !c.active {
		return nextText{label: name + " (paused, " + LeftText(c.left) + " left)"}
	}
	// To the second: Home Assistant's own timers are re-anchored on every event it sends, and a moment
	// that moved by a fraction of a second is not news to send it.
	return nextText{ends: c.due().Round(time.Second).UTC().Format(time.RFC3339), label: name}
}

// sooner reports whether a comes before b as the next timer: a running timer before a paused one, then
// the one with less left, then by name, so that two alike do not swap places between publishes.
func sooner(a, b *timer, now time.Time) bool {
	if a.active != b.active {
		return a.active
	}
	return cmp.Or(cmp.Compare(a.remaining(now), b.remaining(now)), cmp.Compare(a.name, b.name)) < 0
}

// publishNext sends Home Assistant the next timer, and nothing when it says what it said already.
func (t *Timers) publishNext(now time.Time) {
	n := t.nextTimer(now)
	setChanged(t.next.ends, n.ends)
	setChanged(t.next.label, n.label)
}

func setChanged(s *esphome.TextSensor, v string) {
	if s.Get() != v {
		s.Set(v)
	}
}
