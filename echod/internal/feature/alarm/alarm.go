// Package alarm is the device's alarm clock: alarms set on the device itself, and Home Assistant
// helpers it follows as alarms. Both ring here, from the device's own clock, so an alarm set on the
// device still goes off while Home Assistant is down.
//
// A ringing alarm sounds until it is stopped or snoozed, or for ring.RingFor. Stop is the same stop as a
// timer's: the stop word, the action button, the screen, or Home Assistant's button.
package alarm

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/hastate"
	"github.com/HuskerMinion/techo5/echod/internal/feature/remind"
	"github.com/HuskerMinion/techo5/echod/internal/feature/ring"
	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/led"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hook"
)

func init() {
	component.Register(component.Device, Get(), component.Order(32))

	// So that anything meaning "stop the noise" can say so without knowing there are two ring
	// engines. Registered here rather than lazily because Get is called on the line above, so by the
	// time a button can be pressed this is already in place.
	ring.Silences(func() bool { return Get().Stop() })
	ring.Snoozes(func() bool { return Get().Snooze() })
}

const (
	// look is the longest the scheduler sleeps, so a clock set by NTP or a helper changed in Home
	// Assistant is noticed within it.
	look = 20 * time.Second
)

// Ring is an alarm sounding now.
type Ring struct {
	Key   string
	Label string
	At    time.Time // when it was due
}

// Upcoming is an alarm yet to ring.
type Upcoming struct {
	Key     string
	Label   string
	At      time.Time
	Snoozed bool
}

// Followed is a Home Assistant helper and what it currently says.
type Followed struct {
	Entity string
	Label  string
	At     time.Time // zero while the helper has not said, or says nothing usable
	Armed  bool
}

// View is what the screen shows.
type View struct {
	Ringing  *Ring
	Next     *Upcoming
	Snoozed  []Upcoming
	Local    []config.Alarm // by time of day
	Followed []Followed
}

type Alarms struct {
	// Changed fires when anything the screen shows changes; listeners must not block.
	Changed hook.Hook[struct{}]

	next   *esphome.TextSensor
	stop   *esphome.Button
	snooze *esphome.Button

	// sun is the ring while the light before an alarm comes up, under a ringing alarm and over
	// everything quieter; lighting is whether it is showing anything.
	sun      *led.Claim
	lighting bool

	// sound and snoozeFor are the ring's settings in Home Assistant; the screen sets them too.
	sound     *esphome.Select
	snoozeFor *esphome.Number
	ringVol   *esphome.Number

	wake chan struct{}

	mu      sync.Mutex
	ringing *Ring
	silence func()
	snoozed []source
}

var (
	once   sync.Once
	shared *Alarms
)

func Get() *Alarms {
	once.Do(func() { shared = build() })
	return shared
}

func build() *Alarms {
	a := &Alarms{
		next: &esphome.TextSensor{Base: esphome.Base{ObjectID: "next_alarm", Name: "Next alarm", Icon: "mdi:alarm"}},
		sun:  led.Get().Claim(led.PriorityTimer),
		wake: make(chan struct{}, 1),
	}
	// Stops whatever is ringing, a timer as much as an alarm. The object id stays "alarm_stop" from
	// when it only stopped alarms, so automations that press it keep working.
	a.stop = &esphome.Button{Base: esphome.Base{ObjectID: "alarm_stop", Name: "Stop ringing", Icon: "mdi:alarm-off"}, OnPress: func() {
		// From Home Assistant, stop also means a snoozed alarm is not wanted back.
		if !ring.End() {
			a.CancelSnoozes()
		}
	}}
	a.snooze = &esphome.Button{Base: esphome.Base{ObjectID: "alarm_snooze", Name: "Snooze alarm", Icon: "mdi:alarm-snooze"}, OnPress: func() { a.Snooze() }}
	a.sound = &esphome.Select{
		Base:      esphome.Base{ObjectID: "alarm_sound", Name: "Alarm sound", Icon: "mdi:alarm-bell", Category: esphome.CategoryConfig},
		Options:   speaker.AlarmSounds(),
		OnCommand: func(v string) { a.SetSound(v, false) },
	}
	a.snoozeFor = &esphome.Number{
		Base: esphome.Base{ObjectID: "alarm_snooze_length", Name: "Snooze length", Icon: "mdi:alarm-snooze", Category: esphome.CategoryConfig},
		Min:  config.MinSnoozeMinutes, Max: config.MaxSnoozeMinutes, Step: 1, Unit: "min",
		Mode: esphome.NumberBox,
	}
	a.snoozeFor.OnCommand = func(v float32) { a.SetSnooze(int(v)) }
	a.ringVol = &esphome.Number{
		Base: esphome.Base{ObjectID: "ring_volume", Name: "Ring volume", Icon: "mdi:bell-ring", Category: esphome.CategoryConfig},
		Min:  0, Max: config.VolumeSteps, Step: 1,
		Mode: esphome.NumberSlider,
	}
	a.ringVol.OnCommand = func(v float32) { a.SetRingVolume(int(v), false) }
	hastate.Get().Changed.Listen(func(u hastate.Update) {
		if a.follows(u.Entity) {
			a.poke()
		}
	})
	return a
}

func (a *Alarms) Name() string { return "alarms" }

func (a *Alarms) Entities() []esphome.Entity {
	return []esphome.Entity{a.next, a.stop, a.snooze, a.sound, a.snoozeFor, a.ringVol}
}

func (a *Alarms) Restore(c config.Config) {
	a.sound.Set(soundName(c.Alarms.Sound))
	a.snoozeFor.Set(float32(c.Alarms.Snooze()))
	a.followHelpers(c.Alarms.Follow)

	// The first start with a ring volume writes down the one it started from, so it stops following
	// the media volume from here on: that it no longer follows is the whole point of it.
	level := c.Alarms.Ring(c.Speaker.Volume)
	a.ringVol.Set(float32(level))
	if c.Alarms.RingVolume == nil {
		if err := config.Set().Alarms().RingVolume(level); err != nil {
			slog.Warn("saving the first ring volume failed", "err", err)
		}
	}

	// The snoozes come back as they were. One whose moment has already passed is not rung — the
	// scheduler's first window is a few microseconds wide, so nothing restored can be due in it, and
	// that is deliberate: a device that screams every time it boots would be worse than one that
	// misses a snooze. It is said out loud rather than dropped in silence, which is what used to
	// happen to every snooze a restart met.
	now := time.Now()
	var live []source
	for _, s := range c.Alarms.Snoozed {
		if !s.At.After(now) {
			slog.Info("a snooze was missed while the device was off",
				"alarm", s.Label, "was due", s.At.Format(time.RFC3339))
			continue
		}
		live = append(live, source{key: s.Key, label: s.Label, once: s.At})
	}

	a.mu.Lock()
	a.snoozed = live
	a.mu.Unlock()

	if len(live) != len(c.Alarms.Snoozed) {
		saveSnoozes(snoozeList(live))
	}
}

// soundName is the alarm sound in force: the saved one, or the default for none or an unknown one.
func soundName(saved string) string {
	names := speaker.AlarmSounds()
	if slices.Contains(names, saved) {
		return saved
	}
	return names[0]
}

// Sound is the name of what alarms ring with.
func (a *Alarms) Sound() string { return soundName(config.Get().Alarms.Sound) }

// SetSound chooses what alarms ring with; preview plays one round of it, for someone choosing on the
// screen who wants to hear it.
func (a *Alarms) SetSound(name string, preview bool) {
	if !slices.Contains(speaker.AlarmSounds(), name) {
		slog.Warn("unknown alarm sound", "value", name)
		return
	}
	if err := config.Set().Alarms().Sound(name); err != nil {
		slog.Error("saving the alarm sound failed", "err", err)
		return
	}
	a.sound.Set(name)
	slog.Info("alarm sound", "name", name)
	if preview && !a.Ringing() {
		ring.Sample(speaker.AlarmSound(name))
	}
}

// SetSnooze sets how many minutes Snooze puts an alarm off.
func (a *Alarms) SetSnooze(minutes int) {
	minutes = min(max(minutes, config.MinSnoozeMinutes), config.MaxSnoozeMinutes)
	if err := config.Set().Alarms().SnoozeMinutes(minutes); err != nil {
		slog.Error("saving the snooze length failed", "err", err)
		return
	}
	a.snoozeFor.Set(float32(minutes))
	slog.Info("snooze length", "minutes", minutes)
}

// SetRingVolume sets how loud alarms and timers ring, and plays one round of the alarm sound at it
// when asked, so a level chosen on the screen is heard as it is chosen.
func (a *Alarms) SetRingVolume(step int, preview bool) {
	step = min(max(step, 0), config.VolumeSteps)
	if err := config.Set().Alarms().RingVolume(step); err != nil {
		slog.Error("saving the ring volume failed", "err", err)
		return
	}
	a.ringVol.Set(float32(step))
	slog.Info("ring volume", "step", step, "of", config.VolumeSteps)
	if preview && !a.Ringing() {
		ring.Sample(speaker.AlarmSound(a.Sound()))
	}
}

func (a *Alarms) poke() {
	select {
	case a.wake <- struct{}{}:
	default:
	}
}

// Run rings what comes due, looking at least every look and whenever something changes.
func (a *Alarms) Run(ctx context.Context) error {
	last := time.Now()
	published := ""
	for {
		now := time.Now()
		if clockSet(now) && clockSet(last) {
			for _, s := range due(a.sources(now), last, now) {
				a.fire(s, now)
			}
			// After firing, because a snooze due in this very window has just rung and pruned
			// itself. What is left with its moment behind it is one the stale window refused, or
			// one the clock jumped over: it will never ring, since next() reports nothing for a
			// one-off already past, and nothing else ever removed it. It used to sit there for
			// good, with the settings sheet drawing "Snoozed until" and a time in the past — an
			// alarm promised to somebody that was never coming.
			a.pruneSnoozes(now)
		}
		last = now

		label := ""
		if up := a.View(now).Next; up != nil {
			label = up.At.Format("Mon 3:04 PM")
			if up.Label != "" {
				label += " · " + up.Label
			}
		}
		if label != published {
			a.next.Set(label)
			published = label
			a.Changed.Emit(struct{}{})
		}

		wait := look
		if _, at, ok := soonest(a.sources(now), now); ok && time.Until(at) < wait {
			wait = max(time.Until(at), 50*time.Millisecond)
		}
		// The light before an alarm is repainted often enough that it rises rather than steps.
		if a.sunriseRing(now) {
			wait = min(wait, sunriseEvery)
		}
		select {
		case <-ctx.Done():
			a.Stop()
			return nil
		case <-a.wake:
		case <-time.After(wait):
		}
	}
}

// sources is everything that can ring: armed device alarms, armed helpers with a usable state, snoozes.
func (a *Alarms) sources(now time.Time) []source {
	c := config.Get().Alarms
	var out []source
	for _, al := range c.List {
		if al.On {
			out = append(out, source{key: al.ID, label: al.Label, hour: al.Hour, min: al.Minute, days: al.Days, local: true,
				remind: al.Remind, ringOn: al.RingOn})
		}
	}
	for _, f := range a.followed(now) {
		if !f.Armed || f.At.IsZero() {
			continue
		}
		if s, ok := a.helperSource(f.Entity, now); ok {
			s.label = f.Label
			out = append(out, s)
		}
	}
	a.mu.Lock()
	out = append(out, a.snoozed...)
	a.mu.Unlock()
	return out
}

// fire starts ringing, or folds into what is already ringing: one sound covers alarms that meet.
func (a *Alarms) fire(s source, now time.Time) {
	slog.Info("alarm", "key", s.key, "label", s.label)
	if s.local && s.days == config.DaysOnce {
		c := config.Get().Alarms
		if i := slices.IndexFunc(c.List, func(x config.Alarm) bool { return x.ID == s.key }); i >= 0 {
			al := c.List[i]
			al.On = false
			if err := config.Set().Alarms().Put(al); err != nil {
				slog.Warn("turning off a one-off alarm failed", "err", err)
			}
		}
	}
	// A reminder is said, not rung: nothing here holds it open, and it cannot be snoozed.
	if s.remind {
		remind.Get().Fire(s.label, s.ringOn)
		return
	}
	a.mu.Lock()
	before := len(a.snoozed)
	a.snoozed = slices.DeleteFunc(a.snoozed, func(x source) bool { return x.key == s.key })
	// A snooze that rings is spent, so the saved copy must go with it or a restart would bring it
	// back. Taken under the lock and written outside it: saving marshals and fsyncs the whole file.
	spent, saved := len(a.snoozed) != before, snoozeList(a.snoozed)
	if a.ringing != nil {
		a.mu.Unlock()
		if spent {
			saveSnoozes(saved)
		}
		return
	}
	a.ringing = &Ring{Key: strings.TrimPrefix(s.key, "snooze:"), Label: s.label, At: now}
	// Started under the lock, so there is no moment where an alarm is ringing and Stop finds
	// nothing to stop. The bell calls rang from its own goroutine, never from here.
	a.silence = ring.Start("alarm", speaker.AlarmSound(a.Sound()), a.rang)
	a.mu.Unlock()

	if spent {
		saveSnoozes(saved)
	}

	a.Changed.Emit(struct{}{})
}

// rang is the bell telling the alarm its ring is over, however that came about.
func (a *Alarms) rang() {
	a.mu.Lock()
	a.ringing, a.silence = nil, nil
	a.mu.Unlock()
	a.Changed.Emit(struct{}{})
	a.poke()
}

// Ringing reports whether an alarm is sounding.
func (a *Alarms) Ringing() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.ringing != nil
}

// Stop silences a ringing alarm and reports whether there was one.
func (a *Alarms) Stop() bool {
	a.mu.Lock()
	silence := a.silence
	a.mu.Unlock()
	if silence == nil {
		return false
	}
	slog.Info("alarm stopped")
	silence()
	return true
}

// pruneSnoozes drops the snoozes whose moment has gone by without them ringing, and says so.
//
// The trace is the point. A snooze lost this way used to leave nothing at all: not a log line, not a
// removal, not a change on the screen.
func (a *Alarms) pruneSnoozes(now time.Time) {
	a.mu.Lock()
	live, missed := splitSnoozes(a.snoozed, now)
	a.snoozed = live
	saved := snoozeList(live)
	a.mu.Unlock()

	if len(missed) == 0 {
		return
	}
	for _, s := range missed {
		slog.Info("a snooze passed without ringing",
			"alarm", s.label, "was due", s.once.Format(time.RFC3339), "late by", now.Sub(s.once).Round(time.Second))
	}
	saveSnoozes(saved)
	a.Changed.Emit(struct{}{})
}

// snoozeList is the snoozes as they are saved. Call it with the lock held.
func snoozeList(snoozed []source) []config.Snooze {
	out := make([]config.Snooze, 0, len(snoozed))
	for _, s := range snoozed {
		out = append(out, config.Snooze{Key: s.key, Label: s.label, At: s.once})
	}
	return out
}

// saveSnoozes writes the snoozes down so a restart between pressing Snooze and the alarm coming back
// does not lose it. Called when one is made, rings or is dropped, and never on a tick: every set
// marshals the whole config and fsyncs it.
func saveSnoozes(list []config.Snooze) {
	if err := config.Set().Alarms().Snoozed(list); err != nil {
		slog.Warn("saving the snoozes failed", "err", err)
	}
}

// CancelSnoozes drops every snoozed alarm, and reports whether there was one.
func (a *Alarms) CancelSnoozes() bool {
	a.mu.Lock()
	n := len(a.snoozed)
	a.snoozed = nil
	a.mu.Unlock()
	if n == 0 {
		return false
	}
	saveSnoozes(nil)
	slog.Info("snoozes cancelled", "count", n)
	a.poke()
	a.Changed.Emit(struct{}{})
	return true
}

// Snooze silences a ringing alarm and rings it again after the snooze.
func (a *Alarms) Snooze() bool {
	a.mu.Lock()
	r, silence := a.ringing, a.silence
	if r == nil {
		a.mu.Unlock()
		return false
	}
	minutes := config.Get().Alarms.Snooze()
	at := time.Now().Add(time.Duration(minutes) * time.Minute).Truncate(time.Second)
	a.snoozed = append(a.snoozed, source{key: "snooze:" + r.Key, label: r.Label, once: at})
	saved := snoozeList(a.snoozed)
	a.mu.Unlock()

	saveSnoozes(saved)
	slog.Info("alarm snoozed", "minutes", minutes)
	silence()
	a.poke()
	return true
}

// View is the alarms as the screen shows them.
func (a *Alarms) View(now time.Time) View {
	c := config.Get().Alarms
	v := View{Local: c.List, Followed: a.followed(now)}
	slices.SortFunc(v.Local, func(x, y config.Alarm) int {
		return cmp.Or(cmp.Compare(x.Hour, y.Hour), cmp.Compare(x.Minute, y.Minute), cmp.Compare(x.ID, y.ID))
	})
	a.mu.Lock()
	if a.ringing != nil {
		r := *a.ringing
		v.Ringing = &r
	}
	for _, s := range a.snoozed {
		v.Snoozed = append(v.Snoozed, Upcoming{Key: s.key, Label: s.label, At: s.once, Snoozed: true})
	}
	a.mu.Unlock()
	if s, at, ok := soonest(a.sources(now), now); ok {
		v.Next = &Upcoming{Key: s.key, Label: s.label, At: at, Snoozed: strings.HasPrefix(s.key, "snooze:")}
	}
	return v
}

// Set adds an alarm on the device, or turns on the one that already rings at that time on those days.
func (a *Alarms) Set(hour, minute int, days uint8, label string) (config.Alarm, error) {
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return config.Alarm{}, fmt.Errorf("alarms: %d:%02d is not a time of day", hour, minute)
	}
	for _, al := range config.Get().Alarms.List {
		if !al.Remind && al.Hour == hour && al.Minute == minute && al.Days == days && al.Label == label {
			al.On = true
			return al, a.Put(al)
		}
	}
	al := config.Alarm{ID: strconv.FormatInt(time.Now().UnixNano(), 36), Hour: hour, Minute: minute, Days: days, Label: label, On: true}
	return al, a.Put(al)
}

// SetReminder adds a reminder, said once at that time on those days, here and on ringOn; or turns on
// the one already set for that time, those days and those words, with ringOn now.
func (a *Alarms) SetReminder(hour, minute int, days uint8, label string, ringOn []string) (config.Alarm, error) {
	label = strings.TrimSpace(label)
	if label == "" {
		return config.Alarm{}, fmt.Errorf("reminders: a reminder needs something to say")
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return config.Alarm{}, fmt.Errorf("reminders: %d:%02d is not a time of day", hour, minute)
	}
	for _, al := range config.Get().Alarms.List {
		if al.Remind && al.Hour == hour && al.Minute == minute && al.Days == days && al.Label == label {
			al.On, al.RingOn = true, ringOn
			return al, a.Put(al)
		}
	}
	al := config.Alarm{ID: strconv.FormatInt(time.Now().UnixNano(), 36), Hour: hour, Minute: minute, Days: days,
		Label: label, On: true, Remind: true, RingOn: ringOn}
	return al, a.Put(al)
}

// reminderTime reads when a reminder goes off: a time of day, or a time from now ("in 20 minutes"),
// which is rounded up to the next whole minute, since a reminder is kept as a time of day.
func reminderTime(s string, now time.Time) (hour, minute int, fromNow bool, err error) {
	if hour, minute, err = parseClock(s); err == nil {
		return hour, minute, false, nil
	}
	d, derr := timer.ParseDuration(s)
	if derr != nil {
		return 0, 0, false, fmt.Errorf("reminders: %q is neither a time of day nor a time from now", s)
	}
	at := now.Add(d)
	if at.Truncate(time.Minute) != at {
		at = at.Truncate(time.Minute).Add(time.Minute)
	}
	return at.Hour(), at.Minute(), true, nil
}

// devices reads a list of device names, comma-separated.
func devices(s string) []string {
	var out []string
	for _, n := range strings.Split(s, ",") {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// Put saves an alarm on the device as given.
func (a *Alarms) Put(al config.Alarm) error {
	if err := config.Set().Alarms().Put(al); err != nil {
		return err
	}
	a.poke()
	a.Changed.Emit(struct{}{})
	return nil
}

// Delete removes an alarm set on the device.
func (a *Alarms) Delete(id string) error {
	if err := config.Set().Alarms().Delete(id); err != nil {
		return err
	}
	a.poke()
	a.Changed.Emit(struct{}{})
	return nil
}

// Actions are for Home Assistant: voice sentences and automations set alarms with them.
func (a *Alarms) Actions() []*esphome.Action {
	return []*esphome.Action{
		{
			Name: "reminder_set",
			Args: []esphome.Arg{
				{Name: "time", Type: esphome.ArgString}, {Name: "days", Type: esphome.ArgString},
				{Name: "label", Type: esphome.ArgString}, {Name: "ring_on", Type: esphome.ArgString},
			},
			Answers: true,
			Run: func(c esphome.Call) (any, error) {
				hour, minute, fromNow, err := reminderTime(c.String("time"), time.Now())
				if err != nil {
					return nil, err
				}
				days, err := config.ParseDays(c.String("days"))
				if err != nil {
					return nil, err
				}
				if fromNow && days != config.DaysOnce {
					return nil, fmt.Errorf("reminders: a time from now happens once; give a time of day to repeat it")
				}
				al, err := a.SetReminder(hour, minute, days, c.String("label"), devices(c.String("ring_on")))
				if err != nil {
					return nil, err
				}
				slog.Info("reminder set from home assistant", "time", fmt.Sprintf("%d:%02d", al.Hour, al.Minute),
					"days", config.DaysLabel(al.Days), "label", al.Label, "ring_on", al.RingOn)
				return map[string]string{"id": al.ID}, nil
			},
		},
		{
			Name: "alarm_set",
			Args: []esphome.Arg{{Name: "time", Type: esphome.ArgString}, {Name: "days", Type: esphome.ArgString}, {Name: "label", Type: esphome.ArgString}},
			Run: func(c esphome.Call) (any, error) {
				hour, minute, err := parseClock(c.String("time"))
				if err != nil {
					return nil, err
				}
				days, err := config.ParseDays(c.String("days"))
				if err != nil {
					return nil, err
				}
				al, err := a.Set(hour, minute, days, strings.TrimSpace(c.String("label")))
				if err != nil {
					return nil, err
				}
				slog.Info("alarm set from home assistant", "time", fmt.Sprintf("%d:%02d", al.Hour, al.Minute), "days", config.DaysLabel(al.Days), "label", al.Label)
				return nil, nil
			},
		},
		{
			Name: "alarm_delete",
			Args: []esphome.Arg{{Name: "time", Type: esphome.ArgString}, {Name: "label", Type: esphome.ArgString}},
			Run: func(c esphome.Call) (any, error) {
				hour, minute, err := parseClock(c.String("time"))
				if err != nil {
					return nil, err
				}
				label := strings.TrimSpace(c.String("label"))
				n := 0
				for _, al := range config.Get().Alarms.List {
					if al.Hour == hour && al.Minute == minute && (label == "" || strings.EqualFold(al.Label, label)) {
						if err := a.Delete(al.ID); err != nil {
							return nil, err
						}
						n++
					}
				}
				if n == 0 {
					return nil, fmt.Errorf("alarms: none at %d:%02d", hour, minute)
				}
				slog.Info("alarms deleted from home assistant", "count", n)
				return nil, nil
			},
		},
		{
			Name: "alarm_delete_id",
			Args: []esphome.Arg{{Name: "id", Type: esphome.ArgString}},
			Run: func(c esphome.Call) (any, error) {
				id := strings.TrimSpace(c.String("id"))
				if !slices.ContainsFunc(config.Get().Alarms.List, func(al config.Alarm) bool { return al.ID == id }) {
					return nil, fmt.Errorf("alarms: no alarm %q on this device", id)
				}
				if err := a.Delete(id); err != nil {
					return nil, err
				}
				slog.Info("alarm deleted from home assistant", "id", id)
				return nil, nil
			},
		},
		{
			Name:    "alarms_list",
			Answers: true,
			Run: func(esphome.Call) (any, error) {
				now := time.Now()
				return listing(now, a.View(now), timer.Get().List(now)), nil
			},
		},
		{
			Name: "alarms_follow",
			Args: []esphome.Arg{{Name: "entities", Type: esphome.ArgString}},
			Run: func(c esphome.Call) (any, error) {
				var list []string
				for _, e := range strings.Split(c.String("entities"), ",") {
					if e = strings.TrimSpace(e); e != "" {
						list = append(list, e)
					}
				}
				if err := config.Set().Alarms().Follow(list); err != nil {
					return nil, err
				}
				a.followHelpers(list)
				slog.Info("alarms: following home assistant helpers", "entities", list)
				component.Reconnect.Emit(struct{}{})
				return nil, nil
			},
		},
	}
}

// parseClock reads "7:30", "07:30", "19:30:00", "7:30 pm" or "7pm".
func parseClock(s string) (hour, minute int, err error) {
	s = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), ".", ""))
	for _, layout := range []string{"15:04", "15:04:05", "3:04 pm", "3:04pm", "3 pm", "3pm"} {
		if t, e := time.Parse(layout, s); e == nil {
			return t.Hour(), t.Minute(), nil
		}
	}
	return 0, 0, fmt.Errorf("alarms: %q is not a time", s)
}

// helper is one followed helper: its datetime entity and what arms it.
type helper struct{ entity, arm string }

func parseHelpers(list []string) []helper {
	var out []helper
	for _, item := range list {
		entity, arm, _ := strings.Cut(item, "=")
		if entity = strings.TrimSpace(entity); entity != "" {
			out = append(out, helper{entity: entity, arm: strings.TrimSpace(arm)})
		}
	}
	return out
}

func (a *Alarms) followHelpers(list []string) {
	var keys []hastate.Key
	for _, h := range parseHelpers(list) {
		keys = append(keys, hastate.Key{Entity: h.entity}, hastate.Key{Entity: h.entity, Attribute: "friendly_name"})
		if h.arm != "" {
			keys = append(keys, hastate.Key{Entity: h.arm})
		}
	}
	hastate.Get().Follow("alarms", keys...)
	a.poke()
}

func (a *Alarms) follows(entity string) bool {
	for _, h := range parseHelpers(config.Get().Alarms.Follow) {
		if h.entity == entity || h.arm == entity {
			return true
		}
	}
	return false
}

func (a *Alarms) followed(now time.Time) []Followed {
	t := hastate.Get()
	var out []Followed
	for _, h := range parseHelpers(config.Get().Alarms.Follow) {
		f := Followed{Entity: h.entity, Armed: h.arm == "" || t.State(h.arm) != "off"}
		if name, ok := t.Value(h.entity, "friendly_name"); ok {
			f.Label = name
		}
		if s, ok := a.helperSource(h.entity, now); ok {
			f.At, _ = s.next(now)
		}
		out = append(out, f)
	}
	return out
}

func (a *Alarms) helperSource(entity string, now time.Time) (source, bool) {
	return fromHelper(entity, hastate.Get().State(entity), now.Location())
}
