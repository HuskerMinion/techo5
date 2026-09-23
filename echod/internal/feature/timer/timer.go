// Package timer is a kitchen timer: Home Assistant keeps the timers, the device counts them down on
// the ring and rings when one finishes.
//
// Home Assistant sends an event when a timer starts, is changed, is cancelled or finishes, and
// nothing in between — so the countdown here is local arithmetic against a monotonic clock, corrected
// whenever an event arrives. A finished timer is dropped at that end, so the ringing is entirely this
// device's to start and to stop.
package timer

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"
	"github.com/ygelfand/go-esphome-device/api"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/feature/ring"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/led"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hook"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

func init() {
	component.Register(component.Device, Get(), component.Order(31))

	// Only a silence, and no snooze: a timer cannot be put off, so an offer accepted beside a
	// ringing alarm stops the timer and snoozes the alarm, which is what somebody means.
	ring.Silences(func() bool { return Get().Stop() })
}

const (
	// refresh is how often the countdown is redrawn. The frame is only sent when it changes, so a long
	// timer costs nothing between segments.
	refresh = 250 * time.Millisecond

	// ringFor is how long a finished timer rings for if nobody stops it, and ringEvery how often the
	// tone repeats within that.
	ringFor   = 15 * time.Minute
	ringEvery = 2 * time.Second

	// alarmLevel is louder than the tones the device uses for feedback: this one is meant to fetch
	// somebody from another room.
	alarmLevel = 0.6
)

var (
	countdownColor = led.Color{R: 0xFF, G: 0x8C, B: 0x00}
	alarmColor     = led.Color{R: 0xFF, G: 0x40, B: 0x00}
)

// Timers is every timer Home Assistant has told this device about.
type Timers struct {
	countdown *led.Claim
	alarm     *led.Claim

	// names is what is counting down, since the ring can only ever say that something is.
	names *esphome.TextSensor

	// woke is how a new timer restarts the redraw, which stops while there is nothing counting down.
	woke chan struct{}

	mu    sync.Mutex
	held  map[string]*timer
	stop  context.CancelFunc
	rang  string // the name of what is ringing, for the screen
	shown []led.Color

	// Changed fires when a timer starts, changes, ends or rings, and when the ringing stops.
	Changed hook.Hook[struct{}]
}

// Countdown is a timer as the screen shows it. Local is one of the device's own, which the screen
// may cancel; Home Assistant's are cancelled where they were set.
type Countdown struct {
	ID     string
	Name   string
	Left   time.Duration
	Total  time.Duration
	Active bool
	Local  bool
}

// List is every timer, soonest running first and paused ones after.
func (t *Timers) List(now time.Time) []Countdown {
	t.mu.Lock()
	out := make([]Countdown, 0, len(t.held))
	for id, c := range t.held {
		out = append(out, Countdown{ID: id, Name: c.name, Left: c.remaining(now), Total: c.total, Active: c.active, Local: c.local})
	}
	t.mu.Unlock()
	slices.SortFunc(out, func(a, b Countdown) int {
		if a.Active != b.Active {
			if a.Active {
				return -1
			}
			return 1
		}
		return cmp.Or(cmp.Compare(a.Left, b.Left), cmp.Compare(a.Name, b.Name))
	})
	return out
}

// RingingName is the name of the timer ringing now, if one is.
func (t *Timers) RingingName() (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.rang, t.stop != nil
}

// timer is one of them. left is what was last known and at is when that was true, so a running timer
// is left minus however long ago that was.
type timer struct {
	name   string
	total  time.Duration
	left   time.Duration
	at     time.Time
	active bool

	// local is a timer set on the device rather than by Home Assistant: it finishes here, from this
	// clock, so it runs with Home Assistant away.
	local bool
}

func (t *timer) remaining(now time.Time) time.Duration {
	if !t.active {
		return t.left
	}
	return max(t.left-now.Sub(t.at), 0)
}

var (
	once   sync.Once
	shared *Timers
)

func Get() *Timers {
	once.Do(func() { shared = build() })
	return shared
}

func build() *Timers {
	return &Timers{
		countdown: led.Get().Claim(led.PriorityTimer),
		alarm:     led.Get().Claim(led.PriorityAlarm),
		names: &esphome.TextSensor{
			Base: esphome.Base{
				ObjectID: "timers",
				Name:     "Timers",
				Icon:     "mdi:timer-outline",
				Category: esphome.CategoryDiagnostic,
			},
		},
		woke: make(chan struct{}, 1),
		held: map[string]*timer{},
	}
}

func (t *Timers) Name() string { return "timers" }

func (t *Timers) Entities() []esphome.Entity { return []esphome.Entity{t.names} }

// Actions let Home Assistant set and cancel the device's own timers, which run and ring here with Home
// Assistant away. Its own timers, set by voice through Assist, are cancelled the same way they were set.
func (t *Timers) Actions() []*esphome.Action {
	return []*esphome.Action{
		{
			Name:    "timer_start",
			Args:    []esphome.Arg{{Name: "duration", Type: esphome.ArgString}, {Name: "label", Type: esphome.ArgString}},
			Answers: true,
			Run: func(c esphome.Call) (any, error) {
				d, err := ParseDuration(c.String("duration"))
				if err != nil {
					return nil, err
				}
				return map[string]string{"id": t.Start(strings.TrimSpace(c.String("label")), d)}, nil
			},
		},
		{
			Name: "timer_cancel",
			Args: []esphome.Arg{{Name: "id", Type: esphome.ArgString}},
			Run: func(c esphome.Call) (any, error) {
				return nil, t.cancelFromHA(strings.TrimSpace(c.String("id")))
			},
		},
	}
}

// cancelFromHA cancels one of the device's own timers by id, or every one of them for "all".
func (t *Timers) cancelFromHA(id string) error {
	if id == "all" {
		n := 0
		for _, c := range t.List(time.Now()) {
			if c.Local && t.Cancel(c.ID) {
				n++
			}
		}
		slog.Info("timers cancelled from home assistant", "count", n)
		return nil
	}
	if id != "" && !strings.HasPrefix(id, localPrefix) {
		return fmt.Errorf("timers: %q was set through Home Assistant; cancel it there", id)
	}
	if !t.Cancel(id) {
		return fmt.Errorf("timers: no timer %q on this device", id)
	}
	slog.Info("timer cancelled from home assistant", "id", id)
	return nil
}

// Run redraws the countdown while there is one, and waits to be woken while there is not.
func (t *Timers) Run(ctx context.Context) error {
	for {
		if !t.counting() {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.woke:
			}
			continue
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(refresh):
		}
		t.ripe(time.Now())
		t.show()
	}
}

func (t *Timers) counting() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.soonest(time.Now()) != nil
}

// localPrefix marks the ids of timers set on the device. Home Assistant's ids are its own and never
// look like this.
const localPrefix = "local:"

// Start sets a timer of the device's own and returns its id. It counts down, shows and rings exactly
// as one from Home Assistant does, because from here on it is the same timer.
func (t *Timers) Start(name string, d time.Duration) string {
	if d <= 0 {
		return ""
	}
	id := localPrefix + strconv.FormatInt(time.Now().UnixNano(), 36)
	t.mu.Lock()
	t.held[id] = &timer{name: name, total: d, left: d, at: time.Now(), active: true, local: true}
	t.mu.Unlock()
	slog.Info("timer set here", "name", name, "for", d)

	select {
	case t.woke <- struct{}{}:
	default:
	}
	t.show()
	t.publish()
	t.Changed.Emit(struct{}{})
	return id
}

// Cancel drops one of the device's own timers. Home Assistant's are left alone: they are cancelled
// where they were set, and it would tell us about that itself.
func (t *Timers) Cancel(id string) bool {
	if !strings.HasPrefix(id, localPrefix) {
		return false
	}
	t.mu.Lock()
	_, had := t.held[id]
	delete(t.held, id)
	t.mu.Unlock()
	if !had {
		return false
	}
	t.show()
	t.publish()
	t.Changed.Emit(struct{}{})
	return true
}

// ripe finishes any of the device's own timers that have run out, since nothing else will tell us.
// Home Assistant sends an event for its own, which is why only local ones are looked at here.
func (t *Timers) ripe(now time.Time) {
	t.mu.Lock()
	var done []string
	for id, c := range t.held {
		if c.local && c.active && c.remaining(now) <= 0 {
			done = append(done, id)
		}
	}
	t.mu.Unlock()
	for _, id := range done {
		t.mu.Lock()
		name := ""
		if c := t.held[id]; c != nil {
			name = c.name
		}
		delete(t.held, id)
		t.mu.Unlock()
		slog.Info("timer finished here", "name", name)
		t.startRinging(cmp.Or(name, "Timer"))
		t.publish()
		t.Changed.Emit(struct{}{})
	}
}

// Event is a timer event from Home Assistant.
func (t *Timers) Event(e esphome.TimerEvent) {
	slog.Info("timer",
		"event", e.Type, "name", e.Name, "left", e.SecondsLeft, "total", e.TotalSeconds, "active", e.IsActive)

	switch e.Type {
	case api.VoiceAssistantTimerEvent_VOICE_ASSISTANT_TIMER_STARTED,
		api.VoiceAssistantTimerEvent_VOICE_ASSISTANT_TIMER_UPDATED:
		t.set(e)
	case api.VoiceAssistantTimerEvent_VOICE_ASSISTANT_TIMER_CANCELLED:
		t.forget(e.TimerID)
	case api.VoiceAssistantTimerEvent_VOICE_ASSISTANT_TIMER_FINISHED:
		t.finished(e)
	}
	t.show()
	t.publish()
	t.Changed.Emit(struct{}{})
}

// publish names what is counting down, soonest first. It follows the table rather than the clock, so
// it does not send Home Assistant anything four times a second.
func (t *Timers) publish() {
	t.mu.Lock()
	now := time.Now()
	running := make([]*timer, 0, len(t.held))
	for _, c := range t.held {
		if c.active {
			running = append(running, c)
		}
	}
	t.mu.Unlock()

	slices.SortFunc(running, func(a, b *timer) int { return cmp.Compare(a.remaining(now), b.remaining(now)) })

	names := make([]string, 0, len(running))
	for _, c := range running {
		names = append(names, cmp.Or(c.name, "Timer"))
	}
	t.names.Set(strings.Join(names, ", "))
}

// Forget drops every timer, for a Home Assistant that has stopped listening: it holds them in memory
// and comes back without them, so a countdown that outlived the connection is counting down to
// nothing. Whatever is already ringing carries on, since that no longer depends on Home Assistant.
func (t *Timers) Forget() {
	t.mu.Lock()
	n := len(t.held)
	t.held = map[string]*timer{}
	t.mu.Unlock()

	if n > 0 {
		slog.Info("timers forgotten", "count", n)
	}
	t.show()
	t.publish()
	t.Changed.Emit(struct{}{})
}

// Ringing reports whether a finished timer is sounding, which is one of the things that makes the
// device audible.
func (t *Timers) Ringing() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stop != nil
}

// Stop silences a ringing timer and reports whether there was one. The timer itself is Home
// Assistant's, and it has already dropped it.
func (t *Timers) Stop() bool {
	t.mu.Lock()
	stop := t.stop
	t.mu.Unlock()

	if stop == nil {
		return false
	}
	stop()
	return true
}

func (t *Timers) set(e esphome.TimerEvent) {
	t.mu.Lock()
	t.held[e.TimerID] = &timer{
		name:   e.Name,
		total:  time.Duration(e.TotalSeconds) * time.Second,
		left:   time.Duration(e.SecondsLeft) * time.Second,
		at:     time.Now(),
		active: e.IsActive,
	}
	t.mu.Unlock()

	select {
	case t.woke <- struct{}{}:
	default:
	}
}

func (t *Timers) forget(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.held, id)
}

// finished starts the ringing, or joins a timer that is already ringing: one alarm covers however
// many of them went off.
func (t *Timers) finished(e esphome.TimerEvent) {
	t.mu.Lock()
	delete(t.held, e.TimerID)
	t.mu.Unlock()
	t.startRinging(cmp.Or(e.Name, "Timer"))
}

// startRinging rings for a timer that has run out, or joins the ringing already going: one alarm
// covers however many of them went off, whoever set them.
func (t *Timers) startRinging(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.stop != nil {
		return
	}
	t.rang = name

	var ctx context.Context
	ctx, t.stop = context.WithCancel(context.Background())
	safe.Go("timer alarm", func() { t.ring(ctx) })
}

// ring sounds until it is stopped or ringFor is up. Music is ducked rather than suspended: it is one
// room of what may be a whole house, and the tone is audible over it.
func (t *Timers) ring(ctx context.Context) {
	defer func() {
		t.mu.Lock()
		t.stop, t.rang = nil, ""
		t.mu.Unlock()
		t.show()
		t.Changed.Emit(struct{}{})
	}()

	t.alarm.Play(led.EffectPulse, alarmColor)
	defer t.alarm.Clear()

	sound := speaker.Sound()
	sound.Backgrounds().Duck(true)
	defer sound.Backgrounds().Duck(false)

	defer ring.Sounding()()

	over := time.After(ringFor)
	for {
		// A silenced ring whose offer ran out takes the answer it did not get, and stops.
		if ring.Lapsed() {
			slog.Info("timer silenced by a button and left unanswered, stopping")
			return
		}
		// Quiet covers both reasons the chime is held back: a near miss on the stop word, and a
		// button press waiting on an answer. The LED goes on pulsing through it.
		if !ring.Quiet() {
			sound.Interject(func(p *speaker.Player) { p.Chime(alarmLevel, speaker.ToneTimer...) })
		}

		select {
		case <-ctx.Done():
			return
		case <-over:
			slog.Info("timer rang out", "for", ringFor)
			return
		case <-time.After(ringEvery):
		}
	}
}

// show draws the soonest timer, or clears the ring when there is none. It sends nothing when the
// frame has not moved, so the driver is not woken four times a second for a timer with an hour to go.
func (t *Timers) show() {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	var next []led.Color
	if soon := t.soonest(now); soon != nil {
		next = frame(soon.remaining(now), soon.total)
	}
	if slices.Equal(t.shown, next) {
		return
	}
	t.shown = next

	if next == nil {
		t.countdown.Clear()
		return
	}
	t.countdown.Paint(next)
}

// soonest is the running timer with the least left. A paused one is shown as nothing rather than as a
// ring that has stopped moving, because a paused timer is not counting and the light underneath says
// more.
func (t *Timers) soonest(now time.Time) *timer {
	var soon *timer
	for _, c := range t.held {
		if !c.active {
			continue
		}
		if soon == nil || c.remaining(now) < soon.remaining(now) {
			soon = c
		}
	}
	return soon
}

// frame is how much is left, drawn as a fraction of the ring: whole segments for the part still to
// run and the leading one dimmed by the fraction it holds, the same shape Voice PE draws.
func frame(left, total time.Duration) []led.Color {
	lit := float64(led.Segments) * left.Seconds() / math.Max(total.Seconds(), 1)

	out := make([]led.Color, led.Segments)
	for i := range out {
		switch part := lit - float64(i); {
		case part >= 1:
			out[i] = countdownColor
		case part > 0:
			out[i] = dim(countdownColor, part)
		}
	}
	return out
}

func dim(c led.Color, by float64) led.Color {
	scale := func(v byte) byte { return byte(math.Round(float64(v) * by)) }
	return led.Color{R: scale(c.R), G: scale(c.G), B: scale(c.B)}
}
