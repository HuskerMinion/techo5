// Package alarm is the device's alarm clock: alarms set on the device itself, and Home Assistant
// helpers it follows as alarms. Both ring here, from the device's own clock, so an alarm set on the
// device still goes off while Home Assistant is down.
//
// A ringing alarm sounds until it is stopped or snoozed, or for ringFor. Stop is the same stop as a
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
	"github.com/HuskerMinion/techo5/echod/internal/feature/ring"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/led"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hook"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
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
	// ringFor is how long an alarm sounds if nobody stops it, and ringEvery how often the tone repeats.
	ringFor   = 15 * time.Minute
	ringEvery = 2 * time.Second

	// level is the chime's loudness: the timer's, meant to reach another room.
	level = 0.6

	// look is the longest the scheduler sleeps, so a clock set by NTP or a helper changed in Home
	// Assistant is noticed within it.
	look = 20 * time.Second
)

var ringColor = led.Color{R: 0xFF, G: 0x40, B: 0x00}

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
	claim  *led.Claim

	// sun is the ring while the light before an alarm comes up, under a ringing alarm and over
	// everything quieter; lighting is whether it is showing anything.
	sun      *led.Claim
	lighting bool

	// sound and snoozeFor are the ring's settings in Home Assistant; the screen sets them too.
	sound     *esphome.Select
	snoozeFor *esphome.Number

	wake chan struct{}

	mu      sync.Mutex
	ringing *Ring
	silence context.CancelFunc
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
		next:  &esphome.TextSensor{Base: esphome.Base{ObjectID: "next_alarm", Name: "Next alarm", Icon: "mdi:alarm"}},
		claim: led.Get().Claim(led.PriorityAlarm),
		sun:   led.Get().Claim(led.PriorityTimer),
		wake:  make(chan struct{}, 1),
	}
	a.stop = &esphome.Button{Base: esphome.Base{ObjectID: "alarm_stop", Name: "Stop alarm", Icon: "mdi:alarm-off"}, OnPress: func() {
		// From Home Assistant, stop also means a snoozed alarm is not wanted back.
		if !a.Stop() {
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
	hastate.Get().Changed.Listen(func(u hastate.Update) {
		if a.follows(u.Entity) {
			a.poke()
		}
	})
	return a
}

func (a *Alarms) Name() string { return "alarms" }

func (a *Alarms) Entities() []esphome.Entity {
	return []esphome.Entity{a.next, a.stop, a.snooze, a.sound, a.snoozeFor}
}

func (a *Alarms) Restore(c config.Config) {
	a.sound.Set(soundName(c.Alarms.Sound))
	a.snoozeFor.Set(float32(c.Alarms.Snooze()))
	a.followHelpers(c.Alarms.Follow)
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
		speaker.Sound().Interject(func(p *speaker.Player) { p.Chime(level, speaker.AlarmSound(name)...) })
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
			out = append(out, source{key: al.ID, label: al.Label, hour: al.Hour, min: al.Minute, days: al.Days, local: true})
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
	a.mu.Lock()
	a.snoozed = slices.DeleteFunc(a.snoozed, func(x source) bool { return x.key == s.key })
	if a.ringing != nil {
		a.mu.Unlock()
		return
	}
	a.ringing = &Ring{Key: strings.TrimPrefix(s.key, "snooze:"), Label: s.label, At: now}
	ctx, cancel := context.WithCancel(context.Background())
	a.silence = cancel
	a.mu.Unlock()

	a.Changed.Emit(struct{}{})
	safe.Go("alarm", func() { a.ring(ctx) })
}

func (a *Alarms) ring(ctx context.Context) {
	defer func() {
		a.mu.Lock()
		a.ringing, a.silence = nil, nil
		a.mu.Unlock()
		a.Changed.Emit(struct{}{})
		a.poke()
	}()

	a.claim.Play(led.EffectPulse, ringColor)
	defer a.claim.Clear()

	sound := speaker.Sound()
	sound.Backgrounds().Duck(true)
	defer sound.Backgrounds().Duck(false)

	defer ring.Sounding()()

	over := time.After(ringFor)
	notes := speaker.AlarmSound(a.Sound())
	for {
		// A silenced ring whose offer ran out takes the answer it did not get, and stops.
		if ring.Lapsed() {
			slog.Info("alarm silenced by a button and left unanswered, stopping")
			return
		}
		// Quiet covers both reasons the chime is held back: a near miss on the stop word, and a
		// button press waiting on an answer. The alarm ducks the radio so it can be heard; this is
		// the one thing that ducks the alarm. The LED goes on pulsing through it, so the alarm stays
		// obviously alive while it is silent.
		if !ring.Quiet() {
			sound.Interject(func(p *speaker.Player) { p.Chime(level, notes...) })
		}
		select {
		case <-ctx.Done():
			return
		case <-over:
			slog.Info("alarm rang out", "for", ringFor)
			return
		case <-time.After(ringEvery):
		}
	}
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

// CancelSnoozes drops every snoozed alarm, and reports whether there was one.
func (a *Alarms) CancelSnoozes() bool {
	a.mu.Lock()
	n := len(a.snoozed)
	a.snoozed = nil
	a.mu.Unlock()
	if n == 0 {
		return false
	}
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
	a.mu.Unlock()

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
		if al.Hour == hour && al.Minute == minute && al.Days == days && al.Label == label {
			al.On = true
			return al, a.Put(al)
		}
	}
	al := config.Alarm{ID: strconv.FormatInt(time.Now().UnixNano(), 36), Hour: hour, Minute: minute, Days: days, Label: label, On: true}
	return al, a.Put(al)
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
