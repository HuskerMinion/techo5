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
	"regexp"
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
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

func init() {
	component.Register(component.Device, Get(), component.Order(32))

	// So that anything meaning "stop the noise" can say so without knowing there are two ring
	// engines. Registered here rather than lazily because Get is called on the line above, so by the
	// time a button can be pressed this is already in place.
	ring.Silences(func() bool { return Get().Stop() })
	ring.Snoozes(func(minutes int) bool { return Get().SnoozeFor(minutes) })
	ring.Releases(func() bool { return Get().LetGoOfLight() })
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

	// snoozed is set once the ring has been put off, so it is put off once: the device acts on a
	// spoken snooze as it is heard and Home Assistant's automation asks again a moment later, while
	// the bell is still winding the ring down.
	snoozed bool

	// folded are alarms that went off while this one rang and joined its ring: they end with it, and
	// Home Assistant is told each one stopped.
	folded []folded

	// local is an alarm set on the device, or its snooze: one deleted or turned off while it rings is
	// not snoozed back (SnoozeFor).
	local bool

	// lit is a ring with a wake light, its own or a folded alarm's: the light stays at full while it
	// rings and through its snooze (sunrise.go).
	lit bool
}

// folded is an alarm that joined a ring already sounding.
type folded struct {
	key, label string
	at         time.Time
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
	words  *esphome.Select

	// sun is the ring while the light before an alarm comes up, under a ringing alarm and over
	// everything quieter; lighting is whether it is showing anything.
	sun      *led.Claim
	lighting bool
	// bridge is the next alarm the light is held at full for, after a lit ring ended inside that
	// alarm's window; under mu.
	bridge bridge
	// glow is when the light held at full after a light-only alarm's time lets go (lightOnlyGlow);
	// zero for none, under mu. glowKey is the alarm that set it. Neither is saved: a restart inside
	// the half hour lets the light go.
	glow    time.Time
	glowKey string

	// lightLevel and lightColor are the wake light in Home Assistant, for lamps in the room to follow;
	// told is what they last said.
	lightLevel *esphome.Sensor
	lightColor *esphome.TextSensor
	told       lightTold

	// sound and snoozeFor are the ring's settings in Home Assistant; the screen sets them too.
	sound     *esphome.Select
	snoozeFor *esphome.Number
	ringVol   *esphome.Number

	wake chan struct{}

	// aliveAt is when the device last wrote down that it was running, and since the moment before
	// this start that it last had; both the scheduler's alone.
	aliveAt time.Time
	since   time.Time

	mu sync.Mutex
	// saveMu keeps one save of the snoozes at a time, each writing the list as it is then, so two
	// changes saved at once cannot leave the older list on disk (saveSnoozes).
	saveMu  sync.Mutex
	ringing *Ring
	silence func()
	snoozed []source
	// lastSnooze is the snooze made last, for alarm_snooze_for to answer about one the device made
	// from what it heard a moment before Home Assistant asked.
	lastSnooze snoozeMade
}

// snoozeMade is one snooze: when it was made, for how long, and when the alarm rings again.
type snoozeMade struct {
	made, until time.Time
	minutes     int
	// stopped is a snooze taken as a stop: the alarm was deleted or turned off while it rang.
	stopped bool
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
	// The wake light as it is now, for a lamp in the room to follow (sunrise.go).
	a.lightLevel = &esphome.Sensor{Base: esphome.Base{ObjectID: "wake_light_brightness", Name: "Wake light brightness", Icon: "mdi:weather-sunset-up"}, Unit: "%"}
	a.lightColor = &esphome.TextSensor{Base: esphome.Base{ObjectID: "wake_light_color", Name: "Wake light color", Icon: "mdi:palette"}}
	// Stops whatever is ringing, a timer as much as an alarm. The object id stays "alarm_stop" from
	// when it only stopped alarms, so automations that press it keep working.
	a.stop = &esphome.Button{Base: esphome.Base{ObjectID: "alarm_stop", Name: "Stop alarm or timer", Icon: "mdi:alarm-off"}, OnPress: func() {
		// From Home Assistant, stop also means a snoozed alarm is not wanted back - when nothing is
		// sounding - and the wake light held for the next alarm not wanted either. Asked of the ring
		// and not of End, which also takes down a reminder on the screen and would otherwise leave the
		// snooze standing. The light a light-only alarm left up goes either way.
		sounding := ring.IsSounding()
		ring.End()
		a.dropGlow()
		if !sounding {
			a.CancelSnoozes()
			a.dropBridge()
		}
	}}
	a.snooze = &esphome.Button{Base: esphome.Base{ObjectID: "alarm_snooze", Name: "Snooze alarm", Icon: "mdi:alarm-snooze"}, OnPress: func() { a.Snooze() }}
	a.sound = &esphome.Select{
		Base:      esphome.Base{ObjectID: "alarm_sound", Name: "Alarm sound", Icon: "mdi:alarm-bell", Category: esphome.CategoryConfig},
		Options:   speaker.AlarmSounds(),
		OnCommand: func(v string) { a.SetSound(v, false) },
	}
	a.words = a.sayChoice()
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
	hastate.Get().Changed.Listen(a.helperChanged)
	return a
}

func (a *Alarms) Name() string { return "alarms" }

func (a *Alarms) Entities() []esphome.Entity {
	return []esphome.Entity{a.next, a.stop, a.snooze, a.sound, a.words, a.snoozeFor, a.ringVol, a.lightLevel, a.lightColor}
}

func (a *Alarms) Restore(c config.Config) {
	a.sound.Set(soundName(c.Alarms.Sound))
	a.words.Set(c.Alarms.Say.Label())
	a.snoozeFor.Set(float32(c.Alarms.Snooze()))
	a.followHelpers(c.Alarms.Follow)

	// The first start with a ring volume writes down the one it started from, so it stops following
	// the media volume from here on: that it no longer follows is the whole point of it.
	level := c.Alarms.Ring(c.Speaker.Daytime())
	a.ringVol.Set(float32(level))
	if c.Alarms.RingVolume == nil {
		if err := config.Set().Alarms().RingVolume(level); err != nil {
			slog.Warn("saving the first ring volume failed", "err", err)
		}
	}

	// The snoozes come back as they were. One whose moment passed more than ResumeWithin ago is not
	// rung, deliberately: a device that screams every time it boots would be worse than one that
	// misses a snooze. It is written down as missed and shown, rather than dropped in silence, which
	// is what used to happen to every snooze a restart met.
	now := time.Now()
	var live []source
	for _, s := range c.Alarms.Snoozed {
		// One due in the last ResumeWithin is kept, and the scheduler's first look rings it.
		if !s.At.After(now.Add(-ResumeWithin)) {
			ring.Missed("snooze", s.Label, s.At)
			continue
		}
		live = append(live, source{key: s.Key, label: s.Label, once: s.At, lit: s.Lit})
	}

	a.mu.Lock()
	a.snoozed = live
	a.mu.Unlock()

	if len(live) != len(c.Alarms.Snoozed) {
		a.saveSnoozes()
	}

	// The light held for the next alarm comes back too, so a restart while it waits does not drop the
	// room to wherever that alarm's ramp is (#161). Only while it still holds: an alarm turned off,
	// moved or gone by while the device was down lets it go, as it would have with the device up.
	//
	// A helper followed from Home Assistant cannot be asked yet: its state comes in a moment after the
	// start, and until then it is no alarm at all. Its hold comes back unless its time has gone by, and
	// holds() decides on every look once Home Assistant has said; it used to be dropped every time.
	if h := c.Alarms.Held; h != nil {
		b := bridge{key: h.Key, at: h.At}
		local := slices.ContainsFunc(c.Alarms.List, func(al config.Alarm) bool { return al.ID == h.Key })
		if b.holds(a.sources(now), now) || !local && !now.After(b.at.Add(bridgeGrace)) {
			a.mu.Lock()
			a.bridge = b
			a.mu.Unlock()
			slog.Info("wake light held for the next alarm, as it was", "key", b.key, "at", b.at.Format("15:04"))
		} else {
			a.saveBridge()
		}
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
	// Published on the first pass even when it is "" (no alarm), so Home Assistant does not show the
	// sensor as unknown on a device with no alarms set.
	published, sent := "", false
	looked, started, edited := false, time.Now(), false
	for {
		now := time.Now()
		rang := false
		if clockSet(now) && !looked {
			// Once, and only with a clock worth trusting: what fell due while the device was off.
			// A clock still behind the last record after an hour is not going to catch up, and
			// the record is what is wrong: give up on it rather than never record again.
			looked = a.away(now) || time.Since(started) > time.Hour
		}
		if clockSet(now) && clockSet(last) {
			for _, m := range overdue(a.sources(now), last, a.since, now) {
				a.missed(m)
			}
			for _, s := range due(a.sources(now), last, now) {
				a.fire(s, now)
				rang = true
			}
			// After firing, because a snooze due in this very window has just rung and pruned
			// itself. What is left with its moment behind it is one the stale window refused, or
			// one the clock jumped over: it will never ring, since next() reports nothing for a
			// one-off already past, and nothing else ever removed it. It used to sit there for
			// good, with the settings sheet drawing "Snoozed until" and a time in the past — an
			// alarm promised to somebody that was never coming.
			a.pruneSnoozes(now)
			// Not before the look: written from a clock still behind, the record would move back
			// and make the look call rings missed that sounded.
			if looked {
				// An edited alarm is recorded too, or a one-off set just behind the last record
				// would read as missed after a restart.
				a.keepAlive(now, rang || edited)
				edited = false
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
		if label != published || !sent {
			a.next.Set(label)
			published, sent = label, true
			a.Changed.Emit(struct{}{})
		}

		wait := look
		if _, at, ok := soonest(a.sources(now), now); ok && time.Until(at) < wait {
			wait = max(time.Until(at), 50*time.Millisecond)
		}
		// The light before an alarm is repainted often enough that it rises rather than steps.
		light := a.SunriseProgress(now)
		if a.sunriseRing(light) {
			wait = min(wait, sunriseEvery)
		}
		a.tellLight(light, now)
		select {
		case <-ctx.Done():
			a.Stop()
			return nil
		case <-a.wake:
			edited = true
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
			src := source{key: al.ID, label: al.Label, hour: al.Hour, min: al.Minute, days: al.Days, local: true,
				remind: al.Remind, ringOn: al.RingOn, sunrise: c.SunriseFor(al), silent: al.Silent,
				lightOnly: al.LightOnly && !al.Remind && !al.Silent}
			// A dated one-off is a fixed moment, the way a snooze is.
			if at, ok := al.OnDate(); ok {
				src.once = at
			}
			out = append(out, src)
		}
	}
	for _, f := range a.followed(now) {
		if !f.Armed || f.At.IsZero() {
			continue
		}
		if s, ok := a.helperSource(f.Entity, now); ok {
			s.label = f.Label
			s.sunrise = max(c.SunriseMinutes, 0)
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
	// A silent alarm is Home Assistant's: the event is all there is, with nothing to ring or stop.
	if s.silent {
		fireEvent("silent", s.key, s.label, now)
		return
	}
	// A light-only alarm has had its light: what is left is the night ending, with nothing to stop.
	if s.lightOnly {
		a.mu.Lock()
		a.glow, a.glowKey = now.Add(lightOnlyGlow), s.key
		a.mu.Unlock()
		slog.Info("wake light held after a light-only alarm", "key", s.key, "until", now.Add(lightOnlyGlow).Format("15:04"))
		fireEvent("light_only", s.key, s.label, now)
		a.endNight(now)
		return
	}
	a.mu.Lock()
	before := len(a.snoozed)
	a.snoozed = slices.DeleteFunc(a.snoozed, func(x source) bool { return x.key == s.key })
	// A snooze that rings is spent, so the saved copy must go with it or a restart would bring it
	// back. Written outside the lock: saving marshals and fsyncs the whole file.
	spent := len(a.snoozed) != before
	if a.ringing != nil {
		a.ringing.folded = append(a.ringing.folded, folded{key: s.key, label: s.label, at: now})
		a.ringing.lit = a.ringing.lit || lights(s)
		a.mu.Unlock()
		if spent {
			a.saveSnoozes()
		}
		// Folded into the ring already sounding, which may be one somebody silenced: this alarm is a
		// new reason to ring and is owed a chime of its own, and its words if it has any.
		ring.Again()
		fireEvent("ringing", s.key, s.label, now)
		if words := spokenNow(s.label, now); words != "" {
			safe.Go("alarm words", func() { sayOverRing(words) })
		}
		return
	}
	key := strings.TrimPrefix(s.key, "snooze:")
	a.ringing = &Ring{Key: key, Label: s.label, At: now, local: s.local || (isSnooze(s) && localAlarm(key)), lit: lights(s)}
	// Started under the lock, so there is no moment where an alarm is ringing and Stop finds
	// nothing to stop. The bell calls rang from its own goroutine, never from here.
	a.silence = ring.Start("alarm", s.label, speaker.AlarmSound(a.Sound()), a.rang)
	a.mu.Unlock()

	if spent {
		a.saveSnoozes()
	}

	a.Changed.Emit(struct{}{})
	fireEvent("ringing", s.key, s.label, now)
	if words := spokenNow(s.label, now); words != "" {
		safe.Go("alarm words", func() { sayOverRing(words) })
	}
}

// lights is whether s ringing holds the wake light at full: it has a wake light, or is the snooze of
// an alarm that had one.
func lights(s source) bool { return s.sunrise > 0 || s.lit }

// sayFor is how long the ring holds its chime back for an alarm's words (spoken): long enough for Home
// Assistant to make the words and for them to be said, short enough that an alarm whose words never
// come — Home Assistant down, or the device not allowed to ask — is soon audibly an alarm again.
const sayFor = 9 * time.Second

// sayOverRing says an alarm's words (spoken), with the chime held back so they are heard. The ring's
// first chime has already gone by then, which is what turns somebody's head; the words follow.
func sayOverRing(words string) {
	remind.Say(words)
	for end := time.Now().Add(sayFor); time.Now().Before(end) && ring.IsSounding(); {
		ring.Hush()
		time.Sleep(ring.HushFor / 2)
	}
}

// rang is the bell telling the alarm its ring is over, however that came about.
func (a *Alarms) rang() {
	a.mu.Lock()
	r := a.ringing
	a.ringing, a.silence = nil, nil
	a.mu.Unlock()
	// A lit ring that ended for good hands the light to the next alarm whose window is already open,
	// or lets it go. A snoozed one holds it through its snooze instead.
	if r != nil && r.lit && !r.snoozed {
		now := time.Now()
		if a.endLight(a.sources(now), now) {
			a.saveBridge()
		}
	}
	// A snoozed ring has said so already, and rings again later; any other end is a stop, and a stop
	// ends the light a light-only alarm left up as well: the alarm that woke somebody is done with.
	if r != nil && !r.snoozed {
		a.dropGlow()
		fireEvent("stopped", r.Key, r.Label, r.At)
		for _, f := range r.folded {
			fireEvent("stopped", f.key, f.label, f.at)
		}
	}
	a.Changed.Emit(struct{}{})
	a.poke()
}

// Ringing reports whether an alarm is sounding.
func (a *Alarms) Ringing() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.ringing != nil
}

// ringingKey is the alarm ringing now, empty for none.
func (a *Alarms) ringingKey() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.ringing == nil {
		return ""
	}
	return a.ringing.Key
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
	a.mu.Unlock()

	if len(missed) == 0 {
		return
	}
	for _, s := range missed {
		ring.Missed("snooze", s.label, s.once)
	}
	a.saveSnoozes()
	a.Changed.Emit(struct{}{})
}

// snoozeList is the snoozes as they are saved. Call it with the lock held.
func snoozeList(snoozed []source) []config.Snooze {
	out := make([]config.Snooze, 0, len(snoozed))
	for _, s := range snoozed {
		out = append(out, config.Snooze{Key: s.key, Label: s.label, At: s.once, Lit: s.lit})
	}
	return out
}

// saveSnoozes writes the snoozes down so a restart between pressing Snooze and the alarm coming back
// does not lose it. Called when one is made, rings or is dropped, and never on a tick: every set
// marshals the whole config and fsyncs it. Call it without a.mu held. The list is read under saveMu,
// not handed in, so of two saves at once the later writes the newer list.
func (a *Alarms) saveSnoozes() {
	a.saveMu.Lock()
	defer a.saveMu.Unlock()
	a.mu.Lock()
	list := snoozeList(a.snoozed)
	a.mu.Unlock()
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
	a.saveSnoozes()
	slog.Info("snoozes canceled", "count", n)
	a.poke()
	a.Changed.Emit(struct{}{})
	return true
}

// localAlarm is whether an alarm with this ID is set on the device.
func localAlarm(id string) bool {
	return slices.ContainsFunc(config.Get().Alarms.List, func(x config.Alarm) bool { return x.ID == id })
}

// alarmGone is whether the device's alarm with this ID was deleted, or turned off when it repeats. A
// one-off turns itself off as it rings, so off says nothing about it.
func alarmGone(id string) bool {
	list := config.Get().Alarms.List
	i := slices.IndexFunc(list, func(x config.Alarm) bool { return x.ID == id })
	return i < 0 || (!list[i].On && list[i].Days != config.DaysOnce)
}

// dropSnooze drops the snooze of the alarm with this ID, or of the helper with this entity, if there
// is one, and says whether there was.
func (a *Alarms) dropSnooze(id string) bool {
	key := "snooze:" + id
	a.mu.Lock()
	before := len(a.snoozed)
	a.snoozed = slices.DeleteFunc(a.snoozed, func(x source) bool { return x.key == key })
	dropped := len(a.snoozed) != before
	a.mu.Unlock()
	if !dropped {
		return false
	}
	a.saveSnoozes()
	slog.Info("snooze canceled with its alarm", "id", id)
	return true
}

// Snooze silences a ringing alarm and rings it again after the snooze length set on the device.
func (a *Alarms) Snooze() bool { return a.SnoozeFor(0) }

// SnoozeFor silences a ringing alarm and rings it again in minutes, held to the snooze length's limits;
// 0 is the length set on the device.
func (a *Alarms) SnoozeFor(minutes int) bool {
	if minutes <= 0 {
		minutes = config.Get().Alarms.Snooze()
	}
	minutes = min(max(minutes, config.MinSnoozeMinutes), config.MaxSnoozeMinutes)

	a.mu.Lock()
	r, silence := a.ringing, a.silence
	if r == nil || r.snoozed {
		a.mu.Unlock()
		return false
	}
	// Deleted or turned off while it rang: a snooze would bring back an alarm nobody wants, so it is
	// stopped instead (#159).
	if (r.local && alarmGone(r.Key)) || helperOff(r.Key) {
		// Remembered, so alarm_snooze_for arriving a moment after a spoken snooze answers that it
		// stopped rather than that nothing is ringing.
		a.lastSnooze = snoozeMade{made: time.Now(), stopped: true}
		a.mu.Unlock()
		slog.Info("alarm snoozed after it was turned off or deleted; stopped instead", "key", r.Key)
		return a.Stop()
	}
	r.snoozed = true
	now := time.Now()
	at := now.Add(time.Duration(minutes) * time.Minute).Truncate(time.Second)
	a.snoozed = append(a.snoozed, source{key: "snooze:" + r.Key, label: r.Label, once: at, lit: r.lit})
	a.lastSnooze = snoozeMade{made: now, until: at, minutes: minutes}
	a.mu.Unlock()

	a.saveSnoozes()
	slog.Info("alarm snoozed", "minutes", minutes)
	fireEvent("snoozed", r.Key, r.Label, r.At)
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
	return a.SetOn(hour, minute, days, label, "")
}

// SetOn is Set for a one-off on a particular day (date as config.DateLayout, or none).
func (a *Alarms) SetOn(hour, minute int, days uint8, label, date string) (config.Alarm, error) {
	return a.setOn(hour, minute, days, label, date, nil)
}

// alarmKind is what an alarm set from Home Assistant goes off as: silent, light only, or neither (the zero
// value, an alarm that rings).
type alarmKind struct{ silent, lightOnly bool }

// setOn is SetOn, making the alarm the kind k says as well when there is one, in the one save.
func (a *Alarms) setOn(hour, minute int, days uint8, label, date string, k *alarmKind) (config.Alarm, error) {
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return config.Alarm{}, fmt.Errorf("alarms: %d:%02d is not a time of day", hour, minute)
	}
	for _, al := range config.Get().Alarms.List {
		if !al.Remind && al.Hour == hour && al.Minute == minute && al.Days == days && al.Label == label && al.Date == date {
			al.On = true
			if k != nil {
				if (al.Silent != k.silent || al.LightOnly != k.lightOnly) && a.ringingKey() == al.ID {
					slog.Warn("a ringing alarm was set to another kind; it rings on until stopped, and is the new kind from its next time",
						"silent", k.silent, "light_only", k.lightOnly)
				}
				al.Silent, al.LightOnly = k.silent, k.lightOnly
			}
			return al, a.Put(al)
		}
	}
	al := config.Alarm{ID: strconv.FormatInt(time.Now().UnixNano(), 36), Hour: hour, Minute: minute, Days: days, Label: label, On: true, Date: date}
	if k != nil {
		al.Silent, al.LightOnly = k.silent, k.lightOnly
	}
	return al, a.Put(al)
}

// SetReminder adds a reminder, said once at that time on those days, here and on ringOn; or turns on
// the one already set for that time, those days and those words, with ringOn now.
func (a *Alarms) SetReminder(hour, minute int, days uint8, label string, ringOn []string) (config.Alarm, error) {
	return a.SetReminderOn(hour, minute, days, label, ringOn, "")
}

// SetReminderOn is SetReminder for a one-off on a particular day (date as config.DateLayout, or none).
func (a *Alarms) SetReminderOn(hour, minute int, days uint8, label string, ringOn []string, date string) (config.Alarm, error) {
	label = strings.TrimSpace(label)
	if label == "" {
		return config.Alarm{}, fmt.Errorf("reminders: a reminder needs something to say")
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return config.Alarm{}, fmt.Errorf("reminders: %d:%02d is not a time of day", hour, minute)
	}
	for _, al := range config.Get().Alarms.List {
		if al.Remind && al.Hour == hour && al.Minute == minute && al.Days == days && al.Label == label && al.Date == date {
			al.On, al.RingOn = true, ringOn
			return al, a.Put(al)
		}
	}
	al := config.Alarm{ID: strconv.FormatInt(time.Now().UnixNano(), 36), Hour: hour, Minute: minute, Days: days,
		Label: label, On: true, Remind: true, RingOn: ringOn, Date: date}
	return al, a.Put(al)
}

// ReminderTime reads when a reminder goes off: a time of day, or a time from now ("in 20 minutes"),
// which is rounded up to the next whole minute, since a reminder is kept as a time of day.
// daysOrDate reads an action's days: the repeating days ParseDays reads, or a single day for a one-off
// - "today", "tomorrow", or a date as 2026-09-29. A day whose hour:minute has already gone by is
// refused rather than taken for next year.
func daysOrDate(s string, hour, minute int, now time.Time) (days uint8, date string, err error) {
	v := strings.ToLower(strings.TrimSpace(s))
	var d time.Time
	switch v {
	case "today":
		d = now
	case "tomorrow":
		d = now.AddDate(0, 0, 1)
	default:
		t, perr := time.ParseInLocation(config.DateLayout, v, now.Location())
		if perr != nil {
			days, err = config.ParseDays(s)
			return days, "", err
		}
		d = t
	}
	at := time.Date(d.Year(), d.Month(), d.Day(), hour, minute, 0, 0, now.Location())
	if !at.After(now) {
		return 0, "", fmt.Errorf("alarms: %s at %d:%02d has already gone by", at.Format("Mon Jan 2"), hour, minute)
	}
	return config.DaysOnce, at.Format(config.DateLayout), nil
}

func ReminderTime(s string, now time.Time) (hour, minute int, fromNow bool, err error) {
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
	// It is kept as a time of day, which rings at its next occurrence: one a whole day ahead would
	// ring today instead, a minute from now.
	if !at.Add(-24 * time.Hour).Before(now) {
		return 0, 0, false, fmt.Errorf("reminders: %q is a day or more from now; give a time of day instead", s)
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

// Put saves an alarm on the device as given. Turning an alarm off drops its snooze too (#159): a
// snooze pressed by mistake is undone the way a phone's is, by turning the alarm off, and on again if
// it should ring tomorrow.
func (a *Alarms) Put(al config.Alarm) error {
	wasOn := slices.ContainsFunc(config.Get().Alarms.List, func(x config.Alarm) bool { return x.ID == al.ID && x.On })
	if err := config.Set().Alarms().Put(al); err != nil {
		return err
	}
	if wasOn && !al.On {
		a.dropSnooze(al.ID)
		a.dropGlowOf(al.ID)
	}
	a.poke()
	a.Changed.Emit(struct{}{})
	return nil
}

// Delete removes an alarm set on the device, and its snooze with it, and the light it left up if it
// is light only.
func (a *Alarms) Delete(id string) error {
	if err := config.Set().Alarms().Delete(id); err != nil {
		return err
	}
	a.dropSnooze(id)
	a.dropGlowOf(id)
	a.poke()
	a.Changed.Emit(struct{}{})
	return nil
}

// snoozeRecent is how long after a snooze alarm_snooze_for still answers with it: the device acts on
// what it heard as soon as it is heard, and Home Assistant's automation arrives a moment later.
const snoozeRecent = 15 * time.Second

// snoozeAnswer is alarm_snooze_for: it snoozes what is ringing for what sentence asks, and says whether
// an alarm is snoozed now, for how long and until when.
func (a *Alarms) snoozeAnswer(sentence string) map[string]any {
	minutes, _ := ring.SnoozeAsked(sentence)
	snoozed := a.SnoozeFor(minutes)
	stopped := ring.End() || snoozed // a timer ringing beside it cannot be put off, so it stops
	a.mu.Lock()
	last := a.lastSnooze
	a.mu.Unlock()
	if last.made.IsZero() || time.Since(last.made) > snoozeRecent {
		return map[string]any{"snoozed": false, "stopped": stopped}
	}
	if last.stopped {
		return map[string]any{"snoozed": false, "stopped": true}
	}
	slog.Info("alarm snooze from home assistant", "minutes", last.minutes)
	return map[string]any{"snoozed": true, "minutes": last.minutes, "until": last.until.Format("3:04 PM")}
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
				hour, minute, fromNow, err := ReminderTime(c.String("time"), time.Now())
				if err != nil {
					return nil, err
				}
				days, date, err := daysOrDate(c.String("days"), hour, minute, time.Now())
				if err != nil {
					return nil, err
				}
				if fromNow && (days != config.DaysOnce || date != "") {
					return nil, fmt.Errorf("reminders: a time from now happens once; give a time of day for a day or to repeat it")
				}
				al, err := a.SetReminderOn(hour, minute, days, c.String("label"), devices(c.String("ring_on")), date)
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
				days, date, err := daysOrDate(c.String("days"), hour, minute, time.Now())
				if err != nil {
					return nil, err
				}
				al, err := a.setFromHA(hour, minute, days, strings.TrimSpace(c.String("label")), date, alarmKind{})
				if err != nil {
					return nil, err
				}
				slog.Info("alarm set from home assistant", "time", fmt.Sprintf("%d:%02d", al.Hour, al.Minute), "days", config.DaysLabel(al.Days), "label", al.Label)
				return nil, nil
			},
		},
		{
			// alarm_set's twin for an alarm that makes no sound: it goes off only as Home Assistant's
			// esphome.techo5_alarm event, for an automation to wake the house its own way.
			Name: "alarm_set_silent",
			Args: []esphome.Arg{{Name: "time", Type: esphome.ArgString}, {Name: "days", Type: esphome.ArgString}, {Name: "label", Type: esphome.ArgString}},
			Run: func(c esphome.Call) (any, error) {
				hour, minute, err := parseClock(c.String("time"))
				if err != nil {
					return nil, err
				}
				days, date, err := daysOrDate(c.String("days"), hour, minute, time.Now())
				if err != nil {
					return nil, err
				}
				al, err := a.setFromHA(hour, minute, days, strings.TrimSpace(c.String("label")), date, alarmKind{silent: true})
				if err != nil {
					return nil, err
				}
				slog.Info("silent alarm set from home assistant", "time", fmt.Sprintf("%d:%02d", al.Hour, al.Minute), "days", config.DaysLabel(al.Days), "label", al.Label)
				return nil, nil
			},
		},
		{
			// alarm_set's twin for a wake light alone (#164): the light comes up before it, and at its
			// time the night ends, with no sound and nothing to stop.
			Name: "alarm_set_light_only",
			Args: []esphome.Arg{{Name: "time", Type: esphome.ArgString}, {Name: "days", Type: esphome.ArgString}, {Name: "label", Type: esphome.ArgString}},
			Run: func(c esphome.Call) (any, error) {
				hour, minute, err := parseClock(c.String("time"))
				if err != nil {
					return nil, err
				}
				days, date, err := daysOrDate(c.String("days"), hour, minute, time.Now())
				if err != nil {
					return nil, err
				}
				al, err := a.setFromHA(hour, minute, days, strings.TrimSpace(c.String("label")), date, alarmKind{lightOnly: true})
				if err != nil {
					return nil, err
				}
				slog.Info("light-only alarm set from home assistant", "time", fmt.Sprintf("%d:%02d", al.Hour, al.Minute), "days", config.DaysLabel(al.Days), "label", al.Label)
				return nil, nil
			},
		},
		{
			// For a voice automation: snooze the ringing alarm for what the sentence asks ("snooze for ten
			// minutes"), stop a ringing timer, which cannot be put off, and say what was done. The device
			// has usually done it already from what it heard, a moment before Home Assistant got here, so
			// a snooze made in the last few seconds is the answer too.
			Name:    "alarm_snooze_for",
			Args:    []esphome.Arg{{Name: "sentence", Type: esphome.ArgString}},
			Answers: true,
			Run:     func(c esphome.Call) (any, error) { return a.snoozeAnswer(c.String("sentence")), nil },
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
				return nil, a.deleteFromHA(strings.TrimSpace(c.String("id")), timer.Get().List(time.Now()))
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
// clockSep is an hour and minute separated by a dot or a dash rather than a colon.
var clockSep = regexp.MustCompile(`^(\d{1,2})[.-](\d{2})(\D|$)`)

func parseClock(s string) (hour, minute int, err error) {
	// Speech to text writes 8:14 as "8.14" or "8-14" as often as with a colon, and the dots in "a.m."
	// go with the ones between the hour and the minute unless those are taken first.
	s = clockSep.ReplaceAllString(strings.TrimSpace(s), "$1:$2$3")
	s = strings.ToLower(strings.ReplaceAll(s, ".", ""))
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

// helperChanged follows a followed helper's state. Its arm switch off in Home Assistant drops its
// snooze, as turning off an alarm on the device does (#159). Its first report after a start counts
// too: a helper's snooze is only made while its switch is on, so off then means it was turned off
// while the device was down. The drop saves the config, so it runs on its own goroutine, not on the
// state handler's, which must not block.
func (a *Alarms) helperChanged(u hastate.Update) {
	if !a.follows(u.Entity) {
		return
	}
	if u.Attribute == "" && u.Value == "off" {
		for _, h := range parseHelpers(config.Get().Alarms.Follow) {
			if h.arm == u.Entity {
				entity := h.entity
				safe.Go("alarm helper snooze", func() {
					if a.dropSnooze(entity) {
						a.Changed.Emit(struct{}{})
					}
				})
			}
		}
	}
	a.poke()
}

// helperOff is whether key is a followed helper whose on/off switch is off now.
func helperOff(key string) bool {
	for _, h := range parseHelpers(config.Get().Alarms.Follow) {
		if h.entity == key && h.arm != "" {
			return hastate.Get().State(h.arm) == "off"
		}
	}
	return false
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

// deleteFromHA deletes the alarm alarm_delete_id names. A timer's id, from alarms_list's timers, is
// refused with where it goes instead: the device's own to timer_cancel, Home Assistant's to Assist.
func (a *Alarms) deleteFromHA(id string, timers []timer.Countdown) error {
	if !slices.ContainsFunc(config.Get().Alarms.List, func(al config.Alarm) bool { return al.ID == id }) {
		if i := slices.IndexFunc(timers, func(c timer.Countdown) bool { return c.ID == id }); i >= 0 {
			if timers[i].Local {
				return fmt.Errorf("alarms: %.64q is a timer, not an alarm; timer_cancel cancels it", id)
			}
			return fmt.Errorf("alarms: %.64q is a timer set through Home Assistant, not an alarm; cancel it there", id)
		}
		return fmt.Errorf("alarms: no alarm %.64q on this device", id)
	}
	if err := a.Delete(id); err != nil {
		return err
	}
	slog.Info("alarm deleted from home assistant", "id", id)
	return nil
}
