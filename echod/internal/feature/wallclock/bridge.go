// The clock's side of the bridge: the few directives an Echo sends, encoded with the Alexa Gadgets
// protocol and written to a live link.

package wallclock

import (
	"cmp"
	"fmt"
	"hash/fnv"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/agt"
)

// dueTolerance is how far a timer's end may drift before the clock is told again. The device reports
// whole seconds left, so each look lands within a second of the last; anything under this is the
// arithmetic, not a change.
const dueTolerance = 3 * time.Second

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// TimeLayout is the timestamp form the clock accepts: local time, seconds precision, explicit UTC
// offset (the clock shows the digits and ignores the offset, so the digits must already be local).
const TimeLayout = "2006-01-02T15:04:05-07:00"

// FormatTime renders a timestamp the way a real Echo does.
func FormatTime(t time.Time, loc *time.Location) string {
	if loc != nil {
		t = t.In(loc)
	}
	return t.Truncate(time.Second).Format(TimeLayout)
}

// finishedWithin is how close to its end a timer that vanished must have been for its disappearance
// to count as a finish rather than a cancellation. The device publishes only what is still held, so a
// timer that ends is simply gone; one canceled a minute early is gone the same way, and only the time
// it was due at tells them apart.
const finishedWithin = 2 * time.Second

// lateFinish bounds the other side of that window. If the link was down, a timer can vanish long
// after it ended; by then the moment to ring has passed, and pulsing would announce nothing. Such a
// timer is treated as gone and its alert is simply removed.
const lateFinish = 2 * time.Minute

// Timer is one timer as this device holds it: an id, a name, and when it ends.
type Timer struct {
	ID   string
	Name string
	Due  time.Time
}

// Output is the clock side: one method per directive the bridge sends.
type Output interface {
	Timeinfo(at time.Time) error
	ClearIndicator() error
	SetAlert(token string, end time.Time) error
	DeleteAlert(token string) error
	TimersActive() error
	TimersCleared() error
}

// Config tunes the bridge.
type Config struct {
	TimeinfoInterval time.Duration // a real Echo re-sends timeinfo every 30 min
	PulseClearDelay  time.Duration // let the finish pulse finish before clearing
	Timezone         *time.Location
	Log              *slog.Logger
}

func (c Config) withDefaults() Config {
	if c.TimeinfoInterval <= 0 {
		c.TimeinfoInterval = 30 * time.Minute
	}
	if c.PulseClearDelay <= 0 {
		c.PulseClearDelay = 11 * time.Second
	}
	if c.Timezone == nil {
		c.Timezone = time.Local
	}
	if c.Log == nil {
		c.Log = slog.Default()
	}
	return c
}

// alert is one timer as the clock was told about it.
type alert struct {
	end  time.Time
	name string
}

// Bridge holds the state that must survive a step: what the clock currently believes, and whether a
// finish pulse is still running.
type Bridge struct {
	cfg Config
	out Output

	held map[string]alert // token -> what the clock was last told

	// pulsing is when a finish pulse may be cleared, and finished is the tokens whose alerts are
	// deleted then: deleting one while it pulses would cut the pulse short, so it waits.
	pulsing  time.Time
	finished []string

	timeinfoAt time.Time
}

// New builds a bridge.
func New(cfg Config, out Output) *Bridge {
	return &Bridge{cfg: cfg.withDefaults(), out: out, held: map[string]alert{}}
}

// OnConnect primes a fresh link the way a real Echo does: current time, then a cleared notification
// LED. Any previous view of the clock's state is dropped so the next step re-sends every alert.
func (b *Bridge) OnConnect(now time.Time) error {
	b.Disconnected()
	if err := b.out.Timeinfo(now); err != nil {
		return err
	}
	b.timeinfoAt = now
	return b.out.ClearIndicator()
}

// Disconnected forgets what the clock was told; a reconnected clock starts empty (a dropped link
// clears its displayed timers).
func (b *Bridge) Disconnected() {
	b.held = map[string]alert{}
	b.pulsing = time.Time{}
	b.finished = nil
}

// Step reads the timer list once and brings the clock up to date.
func (b *Bridge) Step(timers []Timer, now time.Time) error {
	if b.timeinfoAt.IsZero() || now.Sub(b.timeinfoAt) >= b.cfg.TimeinfoInterval {
		if err := b.out.Timeinfo(now); err != nil {
			return err
		}
		b.timeinfoAt = now
	}
	return b.reconcile(timers, now)
}

func (b *Bridge) reconcile(timers []Timer, now time.Time) error {
	desired := make(map[string]Timer, len(timers))
	for _, t := range timers {
		if !t.Due.IsZero() {
			desired[Token(t.ID)] = t
		}
	}

	if err := b.dropGone(desired, now); err != nil {
		return err
	}
	if err := b.addOrMove(desired); err != nil {
		return err
	}
	return b.clearPulse(now)
}

// dropGone removes the alerts of timers that are no longer running. A timer that reached its end
// rang; one that went before its end was canceled or paused, and a real Echo deletes its alert
// without a sound.
func (b *Bridge) dropGone(desired map[string]Timer, now time.Time) error {
	rang := 0
	for token, a := range b.held {
		if _, ok := desired[token]; ok {
			continue
		}
		switch {
		case !now.Before(a.end.Add(-finishedWithin)) && now.Before(a.end.Add(lateFinish)):
			// It rang. The pulse is started below, once per step.
			b.finished = append(b.finished, token)
			rang++
			b.cfg.Log.Info("wall clock: timer finished", "timer", named(a.name, "Timer"))
		case !now.Before(a.end.Add(-finishedWithin)):
			// It ended, but too long ago to mean anything now: the link was away. Clear its alert
			// without a sound.
			if err := b.out.DeleteAlert(token); err != nil {
				return err
			}
			b.cfg.Log.Info("wall clock: alert removed, timer ended while unobserved", "timer", named(a.name, "Timer"))
		default:
			if err := b.out.DeleteAlert(token); err != nil {
				return err
			}
			b.cfg.Log.Info("wall clock: alert removed, timer canceled or paused", "timer", named(a.name, "Timer"))
		}
		delete(b.held, token)
	}

	// One burst per step, however many timers ended together. A timer that ends while an earlier
	// pulse is still running starts a pulse of its own: the clock stops of its own accord after about
	// ten seconds, so without this the second finish would be silent.
	if rang > 0 {
		if err := b.out.TimersActive(); err != nil {
			return err
		}
		b.pulsing = now.Add(b.cfg.PulseClearDelay)
		b.cfg.Log.Info("wall clock: finish pulsed")
	}
	return nil
}

// addOrMove tells the clock about timers it does not hold, and about ones whose end has moved (time
// added, or resumed after a pause). Soonest first, so the order sent matches the order the ring
// emphasises. An alarm set beside a running timer is left alone; the clock draws both.
func (b *Bridge) addOrMove(desired map[string]Timer) error {
	tokens := make([]string, 0, len(desired))
	for token := range desired {
		tokens = append(tokens, token)
	}
	slices.SortFunc(tokens, func(a, c string) int {
		return cmp.Or(
			desired[a].Due.Compare(desired[c].Due),
			strings.Compare(desired[a].Name, desired[c].Name),
		)
	})

	for _, token := range tokens {
		t := desired[token]
		// The device reports seconds left, so the end moves by up to a second on every look. Resend
		// only when it has really moved — time added, or a pause resumed — or the clock would replay
		// its "set" animation once a second.
		if a, ok := b.held[token]; ok && absDuration(a.end.Sub(t.Due)) < dueTolerance {
			continue
		}
		if err := b.out.SetAlert(token, t.Due); err != nil {
			return err
		}
		b.held[token] = alert{end: t.Due, name: t.Name}
		b.cfg.Log.Info("wall clock: alert set", "timer", named(t.Name, "Timer"),
			"at", FormatTime(t.Due, b.cfg.Timezone))
	}
	return nil
}

// clearPulse deletes the finished timers once their pulse has run, and tells the clock it holds
// nothing when that was the last of them.
func (b *Bridge) clearPulse(now time.Time) error {
	if b.pulsing.IsZero() || now.Before(b.pulsing) {
		return nil
	}
	for _, token := range b.finished {
		if err := b.out.DeleteAlert(token); err != nil {
			return err
		}
	}
	b.finished = nil
	b.pulsing = time.Time{}
	if len(b.held) == 0 {
		if err := b.out.TimersCleared(); err != nil {
			return err
		}
		b.cfg.Log.Info("wall clock: finish pulse cleared")
	}
	return nil
}

// Token is the alert token for a timer: 10 decimal digits, which is the form a real Echo sends. It is
// derived from the timer's id so it stays the same for the life of the timer — the clock replays its
// "set" animation whenever a token it already holds is set again, so a token that changed every look
// would animate forever.
func Token(id string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(id))
	return fmt.Sprintf("1%09d", h.Sum64()%1_000_000_000)
}

func named(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// ClockOutput sends the bridge's directives over a live link.
type ClockOutput struct {
	Sink Sink
	Loc  *time.Location
}

func (o ClockOutput) Tell(payload []byte) error { return o.Sink.Send(payload) }

func (o ClockOutput) Timeinfo(at time.Time) error {
	return o.Sink.Send(agt.StateUpdate(agt.State{Name: "timeinfo", Value: FormatTime(at, o.Loc)}))
}

func (o ClockOutput) ClearIndicator() error { return o.Sink.Send(agt.ClearIndicator()) }

func (o ClockOutput) SetAlert(token string, end time.Time) error {
	return o.Sink.Send(agt.SetAlert(token, agt.AlertTypeTimer, FormatTime(end, o.Loc)))
}

func (o ClockOutput) DeleteAlert(token string) error { return o.Sink.Send(agt.DeleteAlert(token)) }

func (o ClockOutput) TimersActive() error { return o.Sink.Send(agt.TimersActive()) }

func (o ClockOutput) TimersCleared() error { return o.Sink.Send(agt.TimersCleared()) }
