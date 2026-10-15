// Package mute is the microphone mute: the switch in Home Assistant, the button on top of the
// device, and what the ring shows while the microphones are cut.
//
// The cut is real rather than a software flag, so muted here means the microphones are
// disconnected. Home Assistant asks for a state, the button asks for the other one, and start-up
// asks for whatever was stored — they move the line differently and then want exactly the same
// things to follow, so they share settled.
package mute

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/ring"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/buttons"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/camera"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/led"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/mic"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/privacy"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hook"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

func init() {
	component.Register(component.Device, Get(), component.Order(30))
}

// The two levels the mute LED has. Neither way of reaching it offers anything between them.
const (
	dim    = "Dim"
	bright = "Bright"
)

// mutedColor is what an inheriting animation runs in while the microphones are cut. Red, like the
// button's own LED and like a failure: the device cannot hear, which is closer to being broken than
// to being a color someone chose.
var mutedColor = led.Color{R: 0xC0, G: 0x00, B: 0x00}

type Mute struct {
	sw         *esphome.Switch
	brightness *esphome.Select
	line       privacy.Mute
	led        privacy.LED

	// ring is the animation to show while the microphones are cut, and claim is where it goes. The
	// select lives here rather than with the other settings because choosing one has to take effect
	// immediately: being muted has no next occurrence to wait for, it is already happening.
	ring  *esphome.Select
	claim *led.Claim

	// Changed fires when the microphones are actually cut or brought back, with the new state. It is
	// what lets the detector stop running models over silence.
	Changed hook.Hook[bool]

	// mu takes whatever publishes the line one at a time: Home Assistant, the screen, the button, a
	// press that went to a ring, and the watch after a hold. Each reads the switch as the state it
	// starts from, so none may publish a move that another is in the middle of.
	mu sync.Mutex

	// button is whether the mute button is down, and when it last went down (buttons.Pressed). Tests
	// stand in for it.
	button func() (down bool, went time.Time)

	// announce is what follows once the switch says what the line reads: announced, unless a test
	// stands in for it.
	announce func(muted, asked bool)
}

var (
	once   sync.Once
	shared *Mute
)

func Get() *Mute {
	once.Do(func() { shared = build() })
	return shared
}

func build() *Mute {
	m := &Mute{
		sw: &esphome.Switch{
			Base: esphome.Base{
				ObjectID: "mic_mute",
				DeviceID: component.DeviceMicrophone,
				Name:     "Microphone mute",
				Icon:     "mdi:microphone-off",
			},
		},
		claim: led.Get().Claim(led.PriorityMute),
		ring: &esphome.Select{
			Base: esphome.Base{
				ObjectID: "ring_muted",
				DeviceID: component.DeviceRing,
				Name:     "Ring while muted",
				Icon:     "mdi:microphone-off",
				Category: esphome.CategoryConfig,
			},
		},
	}
	m.sw.OnCommand = m.Set
	component.BindEffect(m.ring, led.EffectNames(), m.show, config.Set().Ring().Muted)

	// The entities exist whether or not the pins do. A device that hides controls when its hardware
	// fails is a device nobody can tell has failed, and the log says which of the two went missing.
	m.brightness = &esphome.Select{
		Base: esphome.Base{
			ObjectID: "mute_led_brightness",
			DeviceID: component.DeviceMicrophone,
			Name:     "Mute LED brightness",
			Icon:     "mdi:brightness-6",
			Category: esphome.CategoryConfig,
		},
		Options: []string{dim, bright},
	}
	m.brightness.OnCommand = m.setBrightness

	var err error
	if m.line, err = privacy.Microphone(); err != nil {
		slog.Error("mute unavailable", "err", err)
	}
	if m.led, err = privacy.Light(); err != nil {
		slog.Error("mute LED unavailable", "err", err)
	}

	buttons.Get().Events.Listen(m.pressed)
	return m
}

func (m *Mute) Name() string { return "microphone mute" }

func (m *Mute) Entities() []esphome.Entity {
	if !led.HasRing {
		return []esphome.Entity{m.sw, m.brightness}
	}
	return []esphome.Entity{m.sw, m.ring, m.brightness}
}

// Muted reports whether the line is cut, which is what a turn has to check before opening the
// microphones.
func (m *Mute) Muted() (bool, error) {
	if m.line == nil {
		return false, nil
	}
	return m.line.Get()
}

// Restore cuts the microphones if they were cut when the device was last on. The line does not
// survive a reboot, so what was stored is applied rather than read — and it is the line that
// decides, so a device whose mute cannot be reached comes up live and says so rather than claiming
// to be muted.
func (m *Mute) Restore(c config.Config) {
	if m.line == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	was, err := m.line.Get()
	if err != nil {
		slog.Error("reading mute state failed", "err", err)
		return
	}

	// What to show while cut, before cutting, so the ring is right the first time settled looks.
	component.RestoreEffect(m.ring, c.Ring.Muted, nil, config.Set().Ring().Muted)

	want := c.Microphone.Muted
	if want != was {
		if err := m.line.Set(want); err != nil {
			slog.Error("setting mute failed", "muted", want, "err", err)
		}
	}
	m.settled(false)
	slog.Info("restored", "what", "microphone mute", "muted", m.sw.Get(), "asked", want)

	m.applyBrightness(c.Microphone.LEDBright)
	slog.Info("restored", "what", m.brightness.ObjectID, "using", label(c.Microphone.LEDBright))
}

// Set is the switch in Home Assistant.
func (m *Mute) Set(muted bool) {
	if m.line == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.set(muted)
}

func (m *Mute) set(muted bool) {
	if err := m.line.Set(muted); err != nil {
		slog.Error("setting mute failed", "muted", muted, "err", err)
		return
	}
	m.settled(true)
}

// Flip is the microphone switch on the screen. It asks for the other state, as Home Assistant does,
// rather than standing in for the button: a press is news where the hardware has already moved, and
// from the screen nothing has, so on a Show the switch did nothing to the line and only played the
// sound again. Unmuting a Show from it fails the way it does from Home Assistant.
func (m *Mute) Flip() {
	if m.line == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	is, err := m.line.Get()
	if err != nil {
		slog.Error("reading mute state failed", "err", err)
		return
	}
	m.set(!is)
}

// Toggle is the button on top of the device. Where the hardware has already acted on the press, the
// press is only news: what follows is the same either way. Whether it has can depend on which way
// the press goes, and the switch still holds the state from before it.
func (m *Mute) Toggle() {
	if m.line == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.mustMove(m.sw.Get()) {
		if _, err := m.line.Toggle(); err != nil {
			slog.Error("toggling mute failed", "err", err)
			return
		}
	}
	m.settled(true)
}

// mustMove reports whether a press has to move the line itself, given the switch from before it. Not
// where the hardware acts on the press, and not where the line has already left that state: Amazon's
// gating driver sets the latch itself on a press of half a second or more, which is still a tap
// here, and toggling after it asked for an unmute the latch refuses - the press was dropped and the
// switch left saying live over a set latch, so the next tap set it again instead of letting it go
// (#153). A line that cannot be read is left to Toggle, which says so.
func (m *Mute) mustMove(was bool) bool {
	if m.line.HardwareActs(was) {
		return false
	}
	is, err := m.line.Get()
	return err != nil || is == was
}

// pressed is the mute button. A hold only sounds: nothing is bound to it, and the tone says the
// press was heard.
func (m *Mute) pressed(e buttons.Event) {
	if e.Name != buttons.Mute {
		return
	}
	switch e.Kind {
	case buttons.Tap:
		// A ring takes the press, and the microphone is left where it was (keep, which undoes it
		// where the hardware has already moved). Cutting the microphone is the last thing somebody
		// wants at a ringing alarm, since it would take the stop word with it — and on a device with
		// no action button this is one of the three that can stop a ring.
		if ring.Offered() {
			ring.Accept()
			m.keep()
			return
		}
		if ring.Silence() {
			m.keep()
			return
		}
		m.Toggle()
	case buttons.Hold:
		speaker.Sound().Chime(speaker.ToneMuteHold)
		// A hold is what tells the kernel's camera driver the privacy latch is off, on the Show 8
		// and the 1st gen Show 5 with an older boot image; a camera held off since a tap to unmute
		// can be tried again. On a newer one the hold mutes instead (camera.ErrNeedsReboot).
		camera.Get().Unwedge()
		safe.Go("mute after a hold", m.followHold)
	}
}

// After a hold the switch can be behind the line. Amazon's gating driver (the 1st gen Show 5, the
// Show 8) takes a press of half a second or more as its own mute toggle and sets the latch when the
// button is let go, and any press releases it; where the hardware acts on the press itself (the 2nd
// gen, the Dot) a press held long enough to be a hold moves it all the same. Nothing here acts on a
// hold, so the line is watched instead. Left behind, the switch said live over a latch that was set,
// and the next tap - taken for a press with the microphones live - set the latch again instead of
// letting it go: the button seemed unable to unmute (#153).
var (
	// followFor is how long after a hold the line is watched. The driver moves it when the button
	// is let go, which can be any time after the hold is reported, and a release can be followed
	// by the driver's own set.
	followFor = 15 * time.Second

	// followPoll is how often it looks.
	followPoll = 100 * time.Millisecond
)

// following keeps it to one watch, and followUntil is when that watch ends: a hold during it moves
// the end on rather than starting a second one.
var (
	following   atomic.Bool
	followUntil atomic.Int64
)

// followHold publishes whatever the line does during and after a hold.
func (m *Mute) followHold() {
	if m.line == nil {
		return
	}
	followUntil.Store(time.Now().Add(followFor).UnixNano())
	until := func() time.Time { return time.Unix(0, followUntil.Load()) }
	for following.CompareAndSwap(false, true) {
		m.follow(until, followPoll)
		following.Store(false)
		// A hold that came in as the watch was ending moved the end on, and found it still running.
		if !time.Now().Before(until()) {
			return
		}
	}
}

// follow publishes the line whenever it differs from the switch, looking every poll until until has
// passed.
//
// A difference is taken only once two looks in a row have read it, the second with the button up,
// and the button not pressed since the first was taken. The hardware moves the line for a tap as the button goes down, and the tap
// arrives when it comes up; published in between, the switch already said where the press went, and
// the tap - taken for a press from there - set the latch straight back on a Show (#153 again) or
// played its sound a second time elsewhere. The button reads as down until its tap has been dealt
// with, and the second look covers a press the hardware acted on just before it was heard.
func (m *Mute) follow(until func() time.Time, poll time.Duration) {
	var prev look
	for {
		prev = m.look(prev)
		if !time.Now().Before(until()) {
			return
		}
		time.Sleep(poll)
	}
}

// look is one read of the line during a watch.
type look struct {
	at    time.Time
	muted bool
	ok    bool
}

// look reads the line once, and publishes it if prev read the same and nothing has pressed the
// button since; see follow.
func (m *Mute) look(prev look) look {
	m.mu.Lock()
	defer m.mu.Unlock()
	at := time.Now()
	is, err := m.line.Get()
	if err != nil {
		return look{}
	}
	down, went := m.pressedState()
	if prev.ok && prev.muted == is && is != m.sw.Get() && !down && went.Before(prev.at) {
		m.settled(true)
	}
	return look{at: at, muted: is, ok: true}
}

// pressedState is whether the mute button is down, and when it last went down.
func (m *Mute) pressedState() (bool, time.Time) {
	if m.button != nil {
		return m.button()
	}
	return buttons.Get().Pressed(buttons.Mute)
}

// keep leaves the microphones where they were after a press that went to a ring. Where the hardware
// acts on the button itself - the Dot's keypad driver, the 2nd gen Show 5's - it has already moved
// the mute by the time the press arrives, and nothing here followed: a Dot muted before an alarm was
// live after the press that stopped it, with its wake words still stopped and the stored state
// still muted, so it answered nothing and came back muted after a restart (techo5-dot#4). The same
// press on a live Dot cut its microphones behind Home Assistant's back.
//
// So the line is put back. If it will not go back, what the hardware did is taken as the press it
// was, and published like any other, so that the device at least says what it is doing.
func (m *Mute) keep() {
	if m.line == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	was := m.sw.Get()
	if !m.line.HardwareActs(was) {
		return
	}
	m.await(was)
	is, err := m.line.Get()
	if err != nil {
		slog.Error("reading mute state failed", "err", err)
		return
	}
	if is == was {
		return
	}
	if err := m.line.Set(was); err != nil {
		slog.Error("putting the mute back after a ring failed", "muted", was, "err", err)
	}
	if now, err := m.line.Get(); err == nil && now != was {
		m.settled(true)
		return
	}
	// A latch released under the microphones may take the chip down with it; see settled.
	if !was {
		mic.Rewire()
	}
	slog.Info("microphone mute left as it was after a press that went to a ring", "muted", was)
}

// settled publishes what the line now reads — not what was asked for, so a line that did not move
// says so — and shows it on the ring. asked is false at start-up, where nobody asked.
func (m *Mute) settled(asked bool) {
	if asked {
		m.await(m.sw.Get())
	}

	muted, err := m.line.Get()
	if err != nil {
		slog.Error("reading mute state failed", "err", err)
		return
	}

	m.sw.Set(muted)
	if m.announce != nil {
		m.announce(muted, asked)
		return
	}
	m.announced(muted, asked)
}

// announced shows what the line reads on the ring and, when somebody asked, tells everything else.
func (m *Mute) announced(muted, asked bool) {
	m.show(component.ChosenEffect(m.ring))

	// A latch that has just been released may have taken the microphone chip down with it, which
	// brings it back muted and its stream dead (hardware/mic.Rewire). Only on a real change: at
	// start-up the capture device has just been opened.
	if asked && !muted {
		mic.Rewire()
	}

	if !asked {
		return
	}

	if err := config.Set().Microphone().Muted(muted); err != nil {
		slog.Error("saving mute state failed", "err", err)
	}
	slog.Info("microphone mute", "muted", muted)

	m.Changed.Emit(muted)

	if muted {
		speaker.Sound().Chime(speaker.MuteSound(true))
		return
	}
	speaker.Sound().Chime(speaker.MuteSound(false))
}

// pollInterval is how often await looks while it waits.
const pollInterval = 25 * time.Millisecond

// await gives the hardware the time it says it needs to leave from, so what gets published is where
// the microphones ended up rather than where they were. It returns as soon as they have moved, and
// gives up quietly: a request that changed nothing is not an error.
func (m *Mute) await(from bool) {
	for waited := time.Duration(0); waited < m.line.Lag(); waited += pollInterval {
		if is, err := m.line.Get(); err != nil || is != from {
			return
		}
		time.Sleep(pollInterval)
	}
}

// show puts an animation on the ring for as long as the microphones are cut, or takes it off. Unlike
// a failure this has no duration of its own, so the claim is held and cleared rather than timed.
//
// It takes the name rather than reading the setting, because it is called both when the mute state
// changes and when the choice does, and on that second path the setting has not been written yet.
func (m *Mute) show(name string) {
	// Without a ring there is nothing to show, and a choice saved before its control was taken away
	// (led.HasRing) would run for nothing for as long as the device is muted.
	if name == "" || !m.sw.Get() || !led.HasRing {
		m.claim.Clear()
		return
	}
	m.claim.Play(name, mutedColor)
}

func (m *Mute) setBrightness(v string) {
	on := v == bright
	m.applyBrightness(on)
	if err := config.Set().Microphone().LEDBright(on); err != nil {
		slog.Error("saving mute LED brightness failed", "err", err)
	}
}

func (m *Mute) applyBrightness(on bool) {
	if m.led == nil {
		return
	}
	if err := m.led.SetBright(on); err != nil {
		slog.Error("setting mute LED brightness failed", "bright", on, "err", err)
		return
	}
	if now, err := m.led.Bright(); err == nil {
		m.brightness.Set(label(now))
	}
}

func label(on bool) string {
	if on {
		return bright
	}
	return dim
}
