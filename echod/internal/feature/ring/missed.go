package ring

import (
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hook"
)

// A ring that should have sounded while the device could not - off, restarting, or with its clock
// jumping past it - is not rung late: a device that screams whenever it comes back would be worse.
// It is written down instead, and shown, because one that vanishes without a word reads as the
// device having ignored somebody. "Your timer finished at 14:03" is the trace; silence is not.

const (
	// missedFor is how long the screen keeps saying what was missed: long enough to be seen after
	// a night away, short enough that it is not still there the next evening.
	missedFor = 12 * time.Hour

	// missedKept is how many are kept; the screen says the latest and how many more.
	missedKept = 5
)

// MissedRing is a ring that fell due and never sounded.
type MissedRing struct {
	What  string // "alarm", "timer", "snooze"
	Label string
	Due   time.Time
}

// Says is how the ring is named to people: its label if it has one, what it was if not.
func (m MissedRing) Says() string {
	if m.Label != "" {
		return fmt.Sprintf("%s %q", m.What, m.Label)
	}
	return m.What
}

var missed struct {
	mu    sync.Mutex
	list  []MissedRing
	until time.Time
	now   func() time.Time
}

// MissedChanged fires when a missed ring is written down or the list is cleared.
var MissedChanged hook.Hook[struct{}]

// missedSensor is the latest missed ring in Home Assistant: on a Dot it is the only trace there is.
var missedSensor = &esphome.TextSensor{
	Base: esphome.Base{ObjectID: "missed_ring", Name: "Missed ring", Icon: "mdi:bell-alert-outline"},
}

type missedFeature struct{}

func (missedFeature) Name() string               { return "missed rings" }
func (missedFeature) Entities() []esphome.Entity { return []esphome.Entity{missedSensor} }

func init() {
	missed.now = time.Now
	component.Register(component.Device, missedFeature{}, component.Order(33))
}

// Missed writes down a ring that fell due at due and never sounded.
func Missed(what, label string, due time.Time) {
	m := MissedRing{What: what, Label: label, Due: due}
	slog.Info("a ring was missed", "what", what, "label", label, "was due", due.Format(time.RFC3339))

	missed.mu.Lock()
	missed.list = append(missed.list, m)
	slices.SortFunc(missed.list, func(a, b MissedRing) int { return a.Due.Compare(b.Due) })
	if len(missed.list) > missedKept {
		missed.list = missed.list[len(missed.list)-missedKept:]
	}
	latest := missed.list[len(missed.list)-1]
	missed.until = missed.now().Add(missedFor)
	missed.mu.Unlock()

	missedSensor.Set(fmt.Sprintf("%s, due %s", latest.Says(), latest.Due.Format("Mon Jan 2 15:04")))
	MissedChanged.Emit(struct{}{})
}

// Missing is what was missed, oldest first, for as long as it is still worth saying.
func Missing() []MissedRing {
	missed.mu.Lock()
	defer missed.mu.Unlock()
	if !missed.now().Before(missed.until) {
		return nil
	}
	return slices.Clone(missed.list)
}

// ClearMissed forgets what was missed, once somebody has seen it.
func ClearMissed() {
	missed.mu.Lock()
	had := len(missed.list) > 0
	missed.list, missed.until = nil, time.Time{}
	missed.mu.Unlock()
	if had {
		MissedChanged.Emit(struct{}{})
	}
}
