//go:build spot

// Package display is the Echo Spot's round screen: a clock that follows the conversation, the
// volume, timers and the microphone mute around its rim, and a ring menu under a held finger.
//
// Everything is drawn by the daemon onto the kernel framebuffer (hardware/screen), 480×480, as on
// the Show; the layouts are the Spot's own (render_spot.go), because nothing of a 960×480 page fits a
// circle.
//
// The weather: the reading under the clock, and a weather face (weather_spot.go) from the dial or after
// a question about the weather.
//
// Touch: a tap starts or ends a turn (on a dark screen it only lights it); a swipe up or down is the
// volume, a step per 60 pixels; a held finger opens the ring menu (menu_spot.go). While the menu is open
// the touch screen follows every moving finger, so dragging round the ring turns the dial (or, for a
// value, is a jog wheel), and a tap in the middle does the item at the top.
//
// The backlight: the panel shows almost nothing below about 120 of 255 and glares at 255, so a
// brightness in percent spans backlightMin to the top. From 22:00 to 07:00 (config Screen.Night) it is
// held to nightCeiling.
//
// To Home Assistant the screen is a light with brightness, and a switch for auto-brightness, the
// same entities the Show has.
package display

import (
	"context"
	"fmt"
	"image"
	"log/slog"
	"math"
	"net"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/alarm"
	"github.com/HuskerMinion/techo5/echod/internal/feature/announce"
	"github.com/HuskerMinion/techo5/echod/internal/feature/btaudio"
	"github.com/HuskerMinion/techo5/echod/internal/feature/hastate"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/feature/mute"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	"github.com/HuskerMinion/techo5/echod/internal/feature/remind"
	"github.com/HuskerMinion/techo5/echod/internal/feature/setup"
	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/ambient"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/buttons"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/camera"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/screen"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
	"github.com/HuskerMinion/techo5/echod/internal/service"
)

func init() {
	component.Register(component.Device, Get(), component.Order(60),
		component.Supervise(service.Restart(time.Second, 30*time.Second)))
}

const (
	// linger is how long the last turn's words stay on the screen after it ends.
	linger = 10 * time.Second

	// volumeShow is how long the level stays up after it last moved.
	volumeShow = 2 * time.Second

	// menuIdle closes a ring menu nobody is touching; jogIdle ends a jog wheel's value the same way.
	// (restartWindow, how long the first tap on Restart waits for the second, is shared with the Show.)
	menuIdle = 6 * time.Second
	jogIdle  = 3 * time.Second

	// dialFrame is the redraw while the dial turns; dialEase how much of the way to its rest it moves
	// each frame.
	dialFrame = 40 * time.Millisecond
	dialEase  = 0.35

	idleFrame   = time.Second
	activeFrame = 120 * time.Millisecond

	// slowFrame is a frame long enough to notice, logged once a minute at most.
	slowFrame = 80 * time.Millisecond

	// backlightMin is where this panel starts to be readable: 0 % of brightness lands here. Measured
	// by eye 2026-09-16: 120 looks almost off in a lit room, 191 is fine, 255 is too bright.
	backlightMin = 120

	// nightCeiling is the most brightness the night allows, in percent; defaultNight the hours when
	// nothing is set.
	nightCeiling = 30
	defaultNight = "22-7"

	// Auto-brightness: from darkFraction of the ceiling in the dark to all of it at brightLux,
	// smoothed. Milder than the Show's, because the panel's own range is already narrow.
	darkFraction = 0.6
	brightLux    = 400.0
	autoSmooth   = 0.25
)

type Display struct {
	light *esphome.Light
	auto  *esphome.Switch
	clock *esphome.Select
	// callBtn is the home screen's Call button, on or off (callbutton.go).
	callBtn *esphome.Switch
	// weatherFx is the weather page's sky moving, on or off (weatherfx.go).
	weatherFx *esphome.Switch
	lang      *esphome.Select

	mu      sync.Mutex
	on      bool
	ceiling int

	// The dashboard face (dashboard_spot.go): asked for, when last touched, whether the last frame
	// drew it, whether the touch screen follows fingers for it, where it is scrolled and on which
	// dashboard, a finger moving on it, a level being slid, the idle one put away until, and a finger
	// held still on it.
	dash          bool
	dashTouched   time.Time
	dashShowing   bool
	dashFollow    bool
	dashScroll    int
	dashScrollFor string
	dashDrag      drawnDrag
	dashAdjust    dashAdjusting
	dashAwayUntil time.Time
	dashMoved     bool
	dashHoldAt    time.Time
	dashHoldPt    image.Point
	autoOn        bool
	level         float64
	view          voice.State
	viewAt        time.Time
	volume        int
	volAt         time.Time

	// menuOpen is the ring menu on the screen, menuMode what it shows; menuSel the item at (or turning
	// to) the top; menuRot the dial's rotation now and menuRest where it is heading; menuAt the last
	// touch; spinning a finger turning it, spinAngle its last direction from the center; jogTurn how
	// far a jog wheel has turned towards its next step; restartArm the first tap on Restart and
	// forgetArm on Forget, on the settings screen.
	menuOpen   bool
	menuMode   menuMode
	menuSel    int
	menuRot    float64
	menuRest   float64
	menuAt     time.Time
	spinning   bool
	spinAngle  float64
	jogTurn    float64
	restartArm time.Time
	forgetArm  time.Time

	// sheetOpen is the settings screen up in place of the Settings dial, sheetGrid its six categories
	// rather than one of them; sheetCtl is where in it.
	sheetOpen bool
	sheetGrid bool
	sheetAt   time.Time // the last touch on it, for closing it when left alone
	sheetCtl

	// reminderID is the reminder on the face, so a new one starts at the top; reminderScroll how far
	// its words are dragged up, and the drag's own start while a finger is on it.
	reminderID         string
	reminderScroll     int
	reminderDragging   bool
	reminderDragFrom   int
	reminderDragScroll int

	// wasNight is whether the last backlight was set for the night, so the change of hour relights.
	wasNight bool

	// slideshowIdleSince is when the face last became the plain idle clock (nothing else showing);
	// zero while it is not. Screensaver mode waits for this to run long enough before taking over.
	slideshowIdleSince time.Time

	// weatherArmed is a weather question in progress; weatherUntil when the weather face comes down.
	weatherArmed bool
	weatherUntil time.Time

	// quiet is a turn that was a screen command ("go home", "show the deck"): its words and reply are
	// not shown, so the screen moves at once, as on the Show. radioCue is when Home Assistant last
	// named a station as playing, which comes seconds before the stream. radar turns the weather face
	// to the rain map; radioSel is the station list's middle row.
	quiet    bool
	radioCue time.Time
	radar    bool
	radioSel int
	// cameraSel is the camera list's middle row.
	cameraSel int
	// contactTop is the first contact the Call list shows.
	contactTop int
	// callees are who the Call list showed, callShown the Call button on the face last drawn: taps act
	// on what was on the screen.
	callees   []phone.Callee
	callShown bool
	// slowSaid is when a slow frame was last logged.
	slowSaid time.Time

	// demoUntil puts placeholders where the settings screen shows the owner's details, for
	// screenshots that are going to be published.
	demoUntil time.Time

	poke chan struct{}

	// shots are screenshot requests, answered with a copy of the next frame once it is drawn whole.
	shots chan chan *image.RGBA
	dev   *screen.Device
	r     *roundRenderer
}

var (
	once   sync.Once
	shared *Display
)

func Get() *Display {
	once.Do(func() { shared = build() })
	return shared
}

func build() *Display {
	d := &Display{
		light: &esphome.Light{
			Base:                esphome.Base{ObjectID: "screen", Name: "Screen", Icon: "mdi:monitor"},
			SupportedColorModes: []esphome.ColorMode{esphome.ColorModeBrightness},
		},
		auto: &esphome.Switch{
			Base: esphome.Base{
				ObjectID: "screen_auto_brightness",
				Name:     "Screen auto-brightness",
				Icon:     "mdi:brightness-auto",
				Category: esphome.CategoryConfig,
			},
		},
		poke:  make(chan struct{}, 1),
		shots: make(chan chan *image.RGBA, 4),
		view:  voice.State{Phase: "idle"},
	}
	d.light.OnCommand = d.command
	d.auto.OnCommand = func(on bool) { d.setAuto(on, true) }
	d.clock = clockSelect(d.wake)
	d.callBtn = callButtonSwitch(d.wake)
	d.weatherFx = weatherAnimationSwitch(d.wake)
	d.lang = langSelect()
	voice.Changed.Listen(d.changed)
	media.Get().OnVolume.Listen(d.volumeMoved)
	ambient.Get().Lux.Listen(d.lux)
	touch.Get().Gestures.Listen(d.gesture)
	timer.Get().Changed.Listen(func(struct{}) { d.ringLights() })
	alarm.Get().Changed.Listen(func(struct{}) { d.ringLights() })
	remind.Get().Changed.Listen(func(struct{}) { d.reminderLights() })
	home.Get().Changed.Listen(func(struct{}) { d.wake() })
	onMissed(d.wake)
	hastate.Get().Changed.Listen(func(u hastate.Update) {
		// Only a change means a station is starting; the first value is the one that played last.
		if u.First || u.Attribute != "" || u.Entity == "" || u.Entity != config.Get().Home.Radio.Now {
			return
		}
		if u.Value == "" || u.Value == "unknown" || u.Value == "unavailable" {
			return
		}
		d.mu.Lock()
		d.radioCue = time.Now()
		d.mu.Unlock()
		d.wake()
	})
	btaudio.Get().Changed.Listen(func(btaudio.State) { d.wake() })
	phone.Get().Changed.Listen(d.callLights)
	// The mute button toggles the mute on the buttons' goroutine; redraw once it has.
	buttons.Get().Events.Listen(func(buttons.Event) {
		go func() {
			time.Sleep(100 * time.Millisecond)
			d.wake()
		}()
	})
	return d
}

func (d *Display) Name() string { return "screen" }

func (d *Display) Entities() []esphome.Entity {
	return []esphome.Entity{d.light, d.auto, d.clock, d.callBtn, d.weatherFx, d.lang}
}

// Restore lights the panel the way it was left.
func (d *Display) Restore(c config.Config) {
	setClock24(d.clock, c.Screen.Clock24)
	setCallButton(d.callBtn, c.Screen.CallButton)
	setWeatherAnimation(d.weatherFx, !c.Screen.WeatherStill)
	d.setAuto(c.Screen.Auto, false)
	d.apply(c.Screen.On, c.Screen.Brightness, false)
}

func (d *Display) command(s esphome.LightState) {
	pct := int(math.Round(float64(s.Brightness) * 100))
	if s.On && s.Brightness == 0 {
		pct = int(math.Round(float64(d.light.Get().Brightness) * 100))
		if pct == 0 {
			pct = config.DefaultScreenBrightness
		}
	}
	d.apply(s.On, pct, true)
}

func (d *Display) apply(on bool, pct int, save bool) {
	pct = min(max(pct, 0), 100)
	d.mu.Lock()
	d.on, d.ceiling = on, pct
	d.mu.Unlock()
	d.relight(true)

	d.light.Set(esphome.LightState{On: on, Brightness: float32(pct) / 100, ColorMode: esphome.ColorModeBrightness})
	d.wake()

	if save {
		if err := config.Set().Screen().On(on); err != nil {
			slog.Error("saving the screen state failed", "err", err)
		}
		if err := config.Set().Screen().Brightness(pct); err != nil {
			slog.Error("saving the screen brightness failed", "err", err)
		}
	}
	slog.Info("screen", "on", on, "brightness", pct)
}

func (d *Display) setAuto(on bool, save bool) {
	d.mu.Lock()
	d.autoOn = on
	d.mu.Unlock()
	d.auto.Set(on)
	d.relight(true)
	if save {
		if err := config.Set().Screen().Auto(on); err != nil {
			slog.Error("saving the auto-brightness setting failed", "err", err)
		}
	}
}

func (d *Display) relight(jump bool) {
	night := inNight(time.Now())
	d.mu.Lock()
	d.wasNight = night
	target := 0.0
	if d.on {
		pct := float64(d.ceiling)
		if night {
			pct = math.Min(pct, nightCeiling)
		}
		if d.autoOn {
			if lux, _, ok := ambient.Get().Current(); ok {
				pct *= allowed(lux)
			}
		}
		target = backlightMin + (screen.BacklightMax-backlightMin)*math.Min(math.Max(pct, 0), 100)/100
	}
	// The light before an alarm takes the backlight over while it runs: it starts under whatever the
	// room would otherwise ask for and ends at the face's own brightness.
	if p := sunriseProgress(time.Now()); p > 0 && d.on {
		full := backlightMin + (screen.BacklightMax-backlightMin)*float64(min(max(d.ceiling, 0), 100))/100
		target = backlightMin + (full-backlightMin)*sunriseLevel(p)
	}
	if jump || d.level == 0 {
		d.level = target
	} else {
		d.level += (target - d.level) * autoSmooth
	}
	level := int(math.Round(d.level))
	d.mu.Unlock()

	if err := screen.SetBacklight(level); err != nil {
		slog.Warn("setting the backlight failed", "err", err)
	}
}

// inNight says whether now is within the night hours, "from-to" in whole hours, wrapping midnight.
func inNight(now time.Time) bool {
	v := config.Get().Screen.Night
	if v == "" {
		v = defaultNight
	}
	var from, to int
	if _, err := fmt.Sscanf(v, "%d-%d", &from, &to); err != nil || from == to {
		return false
	}
	h := now.Hour()
	if from < to {
		return h >= from && h < to
	}
	return h >= from || h < to
}

func allowed(lux float64) float64 {
	f := darkFraction + (1-darkFraction)*math.Log10(1+math.Max(lux, 0))/math.Log10(1+brightLux)
	return math.Min(math.Max(f, darkFraction), 1)
}

func (d *Display) lux(float64) {
	d.mu.Lock()
	auto, on := d.autoOn, d.on
	d.mu.Unlock()
	if auto && on {
		d.relight(false)
	}
}

func (d *Display) changed(s voice.State) {
	d.mu.Lock()
	newHeard := s.Heard != "" && s.Heard != d.view.Heard
	if s.Phase == "listening" && d.view.Phase != "listening" {
		d.quiet = false
	}
	d.view = s
	d.viewAt = time.Now()
	// A question about the weather brings the weather face up once the answer is done.
	if newHeard && aboutWeather(s.Heard) {
		d.weatherArmed = true
		d.radar = aboutRadar(s.Heard)
	}
	// "Show the front door" goes up at once, while the assistant answers; "go home" takes it down.
	if newHeard {
		if entity := home.Get().MatchCamera(s.Heard); entity != "" {
			d.weatherArmed, d.quiet = false, true
			if d.menuOpen {
				d.closeMenu()
			}
			go home.Get().ShowCamera(entity, cameraShow)
		}
		if aboutGoingHome(s.Heard) {
			// Back to the clock: whatever is up comes down, and music stops rather than holding the
			// now-playing face.
			d.weatherArmed, d.quiet, d.radar, d.radioCue = false, true, false, time.Time{}
			d.dash = false
			if d.menuOpen {
				d.closeMenu()
			}
			go home.Get().HideCamera()
			go stopMusic()
			slog.Info("screen: home by voice")
		}
	}
	if s.Phase == "idle" && d.weatherArmed {
		d.weatherArmed = false
		if !d.menuOpen || d.menuMode == modeWeather {
			d.openMenu(modeWeather, "")
			d.weatherUntil = time.Now().Add(weatherShow)
		}
	}
	d.mu.Unlock()
	d.wake()
}

func (d *Display) volumeMoved(step int) {
	d.mu.Lock()
	d.volume, d.volAt = step, time.Now()
	d.mu.Unlock()
	d.wake()
}

// gesture is a finger on the panel. It runs on the touch reader's goroutine: it records, acts and
// wakes the loop, and never draws.
func (d *Display) gesture(g touch.Gesture) {
	d.mu.Lock()
	on, open := d.on, d.menuOpen
	d.mu.Unlock()

	if !on {
		// A dark panel only lights; nothing under the finger is acted on.
		if g.Kind == touch.Tap || g.Kind == touch.Hold {
			d.apply(true, d.ceilingOrDefault(), true)
		}
		// Unless something is ringing, where spending the press on the backlight is the wrong trade:
		// the person is reaching for a noise, not for a screen they cannot see. A ring lights the
		// panel by itself when it starts (ringLights), so this is the panel having been put to sleep
		// after that — from the menu — and the tap would otherwise be swallowed.
		if !ringingNow(time.Now()).any() {
			return
		}
	}

	if d.callGesture(g) || d.ringGesture(g) {
		return
	}
	// A browser asking to be let in: its face takes every tap, and only the two answers decide.
	if setup.Get().Waiting() {
		if g.Kind == touch.Tap {
			if allow, answered := askTapSpot(g.Y); answered {
				setup.Get().Answer(allow)
			}
		}
		d.wake()
		return
	}
	// The microphone open for an announcement: its face takes every gesture, because a face nobody
	// can leave is one you have to wait out. A tap is "that is all of it" and sends what was said; a
	// hold throws it away.
	if announce.Get().Recording() {
		switch g.Kind {
		case touch.Tap:
			go announce.Get().Finish()
		case touch.Hold:
			go announce.Get().Cancel()
		}
		d.wake()
		return
	}
	// A reminder takes the face, over an announcement, and every gesture on it is its.
	if _, showing := remind.Get().Showing(); showing {
		d.reminderGesture(g)
		return
	}
	// One that arrived takes the face too, so it needs the same way out. A tap puts it away: it has
	// already been heard by the time anybody is touching the screen, and until this there was no way
	// past it but to wait.
	if _, showing := announce.Get().Showing(); showing {
		if g.Kind == touch.Tap {
			go announce.Get().Dismiss()
		}
		d.wake()
		return
	}
	d.mu.Lock()
	sheet := d.sheetOpen
	d.mu.Unlock()
	if sheet {
		d.sheetGesture(g)
		return
	}
	if open {
		d.menuGesture(g)
		return
	}
	d.mu.Lock()
	dashUp := d.dashShowing
	d.mu.Unlock()
	if dashUp {
		d.dashGestureSpot(g)
		d.wake()
		return
	}
	if v, up := home.Get().Camera(); up {
		switch g.Kind {
		case touch.Tap:
			go home.Get().HideCamera()
			return
		case touch.SwipeLeft:
			go stepCamera(v.Entity, +1)
			return
		case touch.SwipeRight:
			go stepCamera(v.Entity, -1)
			return
		case touch.Hold:
			d.mu.Lock()
			d.openMenu(modeCameras, cameraItem(cameraIndex(v.Entity)))
			d.mu.Unlock()
			d.wake()
			return
		}
	}
	if d.showsNowPlaying() {
		switch g.Kind {
		case touch.Tap:
			if onDone(g.X, g.Y) {
				go stopMusic()
			} else if onStar(g.X, g.Y) {
				go d.favoriteSpot()
			} else {
				togglePlay()
			}
			d.wake()
			return
		case touch.SwipeLeft:
			go stepStation(+1)
			return
		case touch.SwipeRight:
			go stepStation(-1)
			return
		}
	}
	switch g.Kind {
	case touch.Tap:
		d.mu.Lock()
		call := d.callShown && onCallButton(g.X, g.Y)
		if call {
			d.openMenu(modeContacts, "")
			d.contactTop = 0
		}
		d.mu.Unlock()
		if call {
			d.wake()
			return
		}
		voice.Get().Action()
	case touch.SwipeUp:
		media.Get().Adjust(+1)
	case touch.SwipeDown:
		media.Get().Adjust(-1)
	case touch.Hold:
		d.mu.Lock()
		d.openMenu(modeMain, itemTalk)
		d.spinning, d.spinAngle = true, fingerAngle(g.X, g.Y)
		d.mu.Unlock()
		d.wake()
	}
}

// openMenu shows a dial with item id at the top, or a value's jog wheel. Called with d.mu held.
func (d *Display) openMenu(mode menuMode, id itemID) {
	d.menuOpen, d.menuMode, d.menuAt, d.jogTurn = true, mode, time.Now(), 0
	if items := itemsFor(mode); items != nil {
		d.menuSel = indexOf(items, id)
		d.menuRot = restFor(d.menuSel, len(items))
		d.menuRest = d.menuRot
	}
	// The finger follows (every move a turn of the ring) wherever there is a ring to turn. The weather
	// face and the lists have none, and need their swipes and taps as they are.
	touch.Get().SetFollow(mode != modeWeather && mode != modeContacts)
}

// followFingers has the touch screen follow every moving finger (Hold, Drag, Release) rather than
// report swipes: for turning the ring, and for dragging the settings screen's pages.
func (d *Display) followFingers(on bool) { touch.Get().SetFollow(on) }

// closeMenu takes the menu off the screen. Called with d.mu held.
func (d *Display) closeMenu() {
	d.menuOpen, d.spinning = false, false
	touch.Get().SetFollow(false)
}

func (d *Display) menuGesture(g touch.Gesture) {
	d.mu.Lock()
	d.menuAt = time.Now()
	mode := d.menuMode

	switch {
	case mode.jogging():
		switch g.Kind {
		case touch.Hold:
			d.spinning, d.spinAngle = true, fingerAngle(g.X, g.Y)
		case touch.Drag:
			if !d.spinning {
				break
			}
			a := fingerAngle(g.X, g.Y)
			d.jogTurn += wrapAngle(a - d.spinAngle)
			d.spinAngle = a
			steps := 0
			for d.jogTurn >= jogStep {
				d.jogTurn -= jogStep
				steps++
			}
			for d.jogTurn <= -jogStep {
				d.jogTurn += jogStep
				steps--
			}
			if steps != 0 {
				d.mu.Unlock()
				d.jogBy(mode, steps)
				d.wake()
				return
			}
		case touch.Release:
			d.spinning, d.jogTurn = false, 0
		case touch.Tap:
			d.finishJog(mode)
		}

	case mode == modeWeather:
		switch g.Kind {
		case touch.Tap:
			d.closeMenu()
		case touch.SwipeLeft, touch.SwipeRight:
			d.radar = !d.radar
			d.weatherUntil = time.Now().Add(weatherIdle)
		default:
			d.weatherUntil = time.Now().Add(weatherIdle)
		}

	case mode == modeRadio:
		switch g.Kind {
		case touch.Hold:
			d.spinning, d.spinAngle = true, fingerAngle(g.X, g.Y)
		case touch.Drag:
			if !d.spinning {
				break
			}
			a := fingerAngle(g.X, g.Y)
			d.jogTurn += wrapAngle(a - d.spinAngle)
			d.spinAngle = a
			for d.jogTurn >= jogStep {
				d.jogTurn -= jogStep
				d.radioSel++
			}
			for d.jogTurn <= -jogStep {
				d.jogTurn += jogStep
				d.radioSel--
			}
		case touch.Release:
			d.spinning, d.jogTurn = false, 0
		case touch.Tap:
			if src := sourceAt(g.X, g.Y); src != "" {
				d.radioSel = 0
				go home.Get().SetRadioSource(src)
				break
			}
			sel := d.radioSel
			d.closeMenu()
			// Now playing comes up at once, saying the station is starting, rather than the clock until the
			// stream arrives.
			d.radioCue = time.Now()
			d.mu.Unlock()
			go pickStation(sel)
			d.wake()
			return
		case touch.SwipeLeft, touch.SwipeRight:
			d.radioSel = 0
			go home.Get().NextRadioSource()
		}

	case mode == modeContacts:
		list := d.callees
		n := len(list)
		switch g.Kind {
		case touch.SwipeUp:
			d.contactTop = contactTopFor(d.contactTop+contactRows-1, n)
		case touch.SwipeDown:
			d.contactTop = contactTopFor(d.contactTop-(contactRows-1), n)
		}
		if g.Kind != touch.Tap {
			break
		}
		row := contactRowAt(g.Y, contactTopFor(d.contactTop, n), n)
		d.closeMenu() // a tap off the list puts it away
		if row < 0 {
			break
		}
		d.mu.Unlock()
		if row < len(list) {
			go func(c phone.Callee) {
				if err := phone.Get().CallCallee(c); err != nil {
					slog.Warn("screen: call", "err", err)
				}
			}(list[row])
		}
		d.wake()
		return

	default:
		items := itemsFor(mode)
		n := len(items)
		switch g.Kind {
		case touch.Hold:
			d.spinning, d.spinAngle = true, fingerAngle(g.X, g.Y)
		case touch.Drag:
			if d.spinning {
				a := fingerAngle(g.X, g.Y)
				d.menuRot += wrapAngle(a - d.spinAngle)
				d.spinAngle = a
				d.menuRest = d.menuRot
				d.menuSel = topItem(d.menuRot, n)
			}
		case touch.Release:
			d.spinning = false
			d.menuRest = nearestRest(d.menuRot, d.menuSel, n)
		case touch.Tap:
			item, middle := dialHitAt(g.X, g.Y, d.menuRot, n)
			switch {
			case middle || item >= 0:
				// A tap on an item is going there, wherever it is on the ring; the middle is the one at the
				// top. Turning is for looking round the ring, not a step before choosing.
				if item >= 0 {
					d.menuSel = item
				}
				id := items[d.menuSel].id
				d.mu.Unlock()
				d.act(id)
				d.wake()
				return
			}
		case touch.SwipeLeft:
			d.menuSel = (d.menuSel + 1) % n
			d.menuRest = nearestRest(d.menuRot, d.menuSel, n)
		case touch.SwipeRight:
			d.menuSel = (d.menuSel + n - 1) % n
			d.menuRest = nearestRest(d.menuRot, d.menuSel, n)
		}
	}
	d.mu.Unlock()
	d.wake()
}

// jogBy turns a value by steps (clockwise positive).
func (d *Display) jogBy(mode menuMode, steps int) {
	switch mode {
	case modeVolume:
		media.Get().Adjust(steps)
	}
}

// finishJog is a tap on a jog wheel: back to the dial it came from.
// Called with d.mu held.
func (d *Display) finishJog(mode menuMode) {
	d.spinning, d.jogTurn = false, 0
	switch mode {
	case modeVolume:
		d.openMenu(modeMain, itemVolume)
	}
}

// act does what a dial item says.
func (d *Display) act(id itemID) {
	slog.Info("ring menu", "item", id)
	if i := cameraOf(id); i >= 0 {
		d.locked(d.closeMenu)
		go pickCamera(i)
		return
	}
	switch id {
	case itemTalk:
		d.locked(d.closeMenu)
		voice.Get().Action()
	case itemAnnounce:
		d.locked(d.closeMenu)
		go announce.Get().Speak(context.Background())
	case itemCall:
		d.locked(func() {
			d.openMenu(modeContacts, "")
			d.contactTop = 0
		})
	case itemMute:
		mute.Get().Toggle()
	case itemMusic:
		rd := home.Get().Radio()
		playing, paused := musicState()
		rows := radioRows(rd, playing || paused)
		sel := 0
		for i, row := range rows {
			if strings.EqualFold(row, currentStation(rd)) {
				sel = i
			}
		}
		// Whether this room is in a house is what the Stop row says. See home.PokeGroup.
		go home.Get().PokeGroup()
		d.locked(func() {
			d.openMenu(modeRadio, "")
			d.radioSel = sel
		})
	case itemVolume:
		d.locked(func() { d.openMenu(modeVolume, "") })
	case itemCamera:
		d.locked(d.closeMenu)
		go home.Get().ShowCamera(home.LocalCamera, cameraStep)
	case itemWeather:
		d.locked(func() {
			d.openMenu(modeWeather, "")
			d.weatherUntil = time.Now().Add(weatherIdle)
		})
	case itemDashboard:
		d.toggleDashboard()
	case itemTimers:
		if timer.Get().Ringing() {
			timer.Get().Stop()
			d.locked(d.closeMenu)
		}
	case itemSettings:
		d.locked(d.openSettings)
	case itemSleep:
		d.locked(d.closeMenu)
		d.mu.Lock()
		ceiling := d.ceiling
		d.mu.Unlock()
		d.apply(false, ceiling, true)
	}
}

func (d *Display) locked(f func()) {
	d.mu.Lock()
	defer d.mu.Unlock()
	f()
}

// restartDevice reboots; the slot store and the daemon's state are on disk already.
func restartDevice() {
	syscall.Sync()
	if err := syscall.Reboot(syscall.LINUX_REBOOT_CMD_RESTART); err != nil {
		slog.Error("restart failed", "err", err)
	}
}

// deviceAddress is the first IPv4 address that is up and not the loopback.
func deviceAddress() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "no address"
	}
	for _, i := range ifaces {
		if i.Flags&net.FlagLoopback != 0 || i.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
				return ipn.IP.String()
			}
		}
	}
	return "no address"
}

// bootedSlot is the rootfs slot the initramfs booted, if any.
func bootedSlot() string {
	b, err := os.ReadFile("/run/techo5/slot")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func (d *Display) isSpinning() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.spinning
}

func (d *Display) ceilingOrDefault() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.ceiling > 0 {
		return d.ceiling
	}
	return config.DefaultScreenBrightness
}

func (d *Display) wake() {
	select {
	case d.poke <- struct{}{}:
	default:
	}
}

// Start opens the framebuffer.
func (d *Display) Start(context.Context) error {
	dev, err := screen.Open()
	if err != nil {
		return err
	}
	d.dev = dev
	d.r = newRoundRenderer(dev.Canvas())
	d.settleScreen(dev)
	slog.Info("screen open", "fb", dev.String())
	return nil
}

func (d *Display) Close() error {
	if d.dev == nil {
		return nil
	}
	err := d.dev.Close()
	d.dev = nil
	return err
}

// Run redraws until ctx is canceled: on the second while idle, faster while something moves.
func (d *Display) Run(ctx context.Context) error {
	for {
		wait := d.frame()
		select {
		case <-ctx.Done():
			return nil
		case <-d.poke:
		case <-time.After(wait):
		}
	}
}

func (d *Display) frame() time.Duration {
	now := time.Now()
	d.mu.Lock()
	nightChanged := d.wasNight != inNight(now)
	d.mu.Unlock()
	if nightChanged {
		d.relight(true)
	}
	d.mu.Lock()
	if d.menuOpen && !d.spinning {
		switch {
		case d.menuMode.jogging() && now.Sub(d.menuAt) > jogIdle:
			d.finishJog(d.menuMode)
		case d.menuMode == modeWeather:
			if now.After(d.weatherUntil) {
				d.closeMenu()
				d.radar = false
			}
		case d.menuMode == modeRadio:
			if now.Sub(d.menuAt) > radioIdle {
				d.closeMenu()
			}
		case d.menuMode == modeContacts:
			if now.Sub(d.menuAt) > cameraListIdle {
				d.closeMenu()
			}
		case !d.menuMode.jogging() && now.Sub(d.menuAt) > menuIdle:
			d.closeMenu()
		}
	}
	if d.sheetOpen && !d.dragging && d.draft == nil && now.Sub(d.sheetAt) > sheetIdle {
		d.sheetOpen, d.picker = false, ""
		d.followFingers(false)
	}
	sheetOpen, sheetGrid := d.sheetOpen, d.sheetGrid
	turning := false
	if d.menuOpen && !d.spinning {
		if diff := d.menuRest - d.menuRot; math.Abs(diff) > 0.002 {
			d.menuRot += diff * dialEase
			turning = true
		} else {
			d.menuRot = d.menuRest
		}
	}
	on, view, at := d.on, d.view, d.viewAt
	s := roundScene{
		now:       now,
		phase:     view.Phase,
		heard:     view.Heard,
		reply:     view.Reply,
		menuOpen:  d.menuOpen,
		menuMode:  d.menuMode,
		menuSel:   d.menuSel,
		menuRot:   d.menuRot,
		radioSel:  d.radioSel,
		cameraSel: d.cameraSel,
		radarOn:   d.radar,
		sheetOpen: sheetOpen,
		sheetGrid: sheetGrid,
	}
	quiet := d.quiet
	if !d.volAt.IsZero() && now.Sub(d.volAt) < volumeShow {
		s.volume, s.showVolume = d.volume, true
	}
	d.mu.Unlock()

	if !on {
		return time.Hour
	}
	if view.Phase == "idle" && (view.Heard != "" || view.Reply != "") && now.Sub(at) < linger {
		s.phase = "lingering"
	}
	if quiet && (s.phase == "thinking" || s.phase == "replying" || s.phase == "lingering") {
		// A screen command: the screen it asked for is the answer, not the words.
		s.phase, s.heard, s.reply = "idle", "", ""
	}
	s.muted, _ = mute.Get().Muted()
	// A stream this player is carrying is the room's when it is what is being heard: the face names it,
	// and says what it is doing, though the audio never passes through this player's own stream. Both,
	// not just playing: the face tests paused first, so a station left paused underneath would label
	// somebody else's track as Paused - and a remote merely holding the speaker, with a station playing
	// underneath it, is not the room's at all (see media.Player.Carried).
	s.playing, s.paused = musicState()
	s.maxVolume = config.VolumeSteps
	if s.sheetOpen {
		s.sheet = d.sheetView(now)
	}
	if !s.showVolume {
		s.volume = media.Get().Volume()
	}
	for _, t := range timer.Get().List(now) {
		if t.Active {
			s.timers = append(s.timers, t)
		}
	}
	s.timerRinging = timer.Get().Ringing()
	s.ringing = ringingNow(now)
	if s.menuOpen && s.menuMode == modeCameras {
		s.cameras = home.Get().Cameras()
	}
	if s.menuOpen {
		s.phoneReady = phone.Get().State().Registered
		s.houseReady = config.Get().Home.HouseWord != ""
		callees := phone.Get().Callees()
		s.contactCount = len(callees)
		if s.menuMode == modeContacts {
			s.contacts = callees
			d.mu.Lock()
			s.contactTop = d.contactTop
			d.callees = callees
			d.mu.Unlock()
		}
	}
	s.call = phone.Get().State()
	s.weather = home.Get().Weather()
	s.camera, s.showCamera = home.Get().Camera()
	s.cameraLive = camera.Get().Running()
	bt := btaudio.Get().State()
	s.btPairing = bt.Pairing
	if s.menuOpen && s.menuMode == modeWeather {
		s.forecast = home.Get().Forecast()
		// Asked for while the weather is up, so the rain map is ready when it is turned to.
		if v := home.Get().Radar(); s.radarOn {
			s.radar = v
		} else {
			s.sky = skyNow(weatherNow(s.weather, s.forecast))
		}
	}
	s.nowPlaying = s.phase == "idle" && d.showsNowPlaying()
	if s.nowPlaying || (s.menuOpen && s.menuMode == modeRadio) {
		s.radio = home.Get().Radio()
		if rows := radioRows(s.radio, s.playing || s.paused); s.radioSel >= len(rows) || s.radioSel < 0 {
			s.radioSel = min(max(s.radioSel, 0), max(len(rows)-1, 0))
			d.mu.Lock()
			d.radioSel = s.radioSel
			d.mu.Unlock()
		}
	}

	// boring is the plain idle clock face — the same set of faces draw() checks before falling
	// through to clockFace. Background mode rides along with it; Screensaver only takes over once it
	// has held for the configured wait, tracked by how long it has run continuously.
	boring := s.phase == "idle" && s.call.Phase == phone.Idle && !s.ringing.any() && !s.showVolume &&
		!s.showCamera && !s.nowPlaying && !s.menuOpen && !s.sheetOpen
	// A browser waiting to be let in is a page of its own, over whatever is on the screen: asking for
	// the setup page is done from the settings screen, so the answer has to reach somebody who is
	// still standing in it. It was set only on the idle page once, and the press could not be given
	// without leaving settings first.
	s.setupAsking = setup.Get().Waiting()
	s.announceReady = config.Get().Home.HouseWord != ""
	s.announceRecording = announce.Get().Recording()
	s.announcePeers = len(announce.Peers())
	s.announcement, s.showAnnouncement = announce.Get().Showing()
	s.reminder, s.showReminder = remind.Get().Showing()
	if s.reminder.From != config.Get().Device.Name {
		s.reminderFrom = s.reminder.From
	}
	d.mu.Lock()
	s.reminderScroll = d.reminderScroll
	d.mu.Unlock()
	s.missed = missedNote(now, true)

	if boring {
		s.slideshow = home.Get().SlideshowBackground()
		if s.slideshow == nil {
			s.slideshowTrouble = home.Get().SlideshowTrouble()
		}
		s.sunrise, s.sunriseFace = sunriseProgress(now), config.Get().Alarms.SunriseFace
	}
	d.mu.Lock()
	if !boring {
		d.slideshowIdleSince = time.Time{}
	} else if d.slideshowIdleSince.IsZero() {
		d.slideshowIdleSince = now
	}
	idleSince := d.slideshowIdleSince
	d.mu.Unlock()
	if boring && !idleSince.IsZero() && now.Sub(idleSince) >= home.Get().SlideshowIdleTimeout() {
		s.slideshowScreensaver = home.Get().SlideshowScreensaverPhoto()
		s.slideshowOverlay = home.Get().SlideshowOverlay()
	}

	d.dashSceneSpot(&s)
	s.callButton = callButton.Load()
	drawn := time.Now()
	d.r.draw(s)
	d.mu.Lock()
	d.callShown = d.r.callDrawn
	d.mu.Unlock()
	painted := time.Now()
	if err := d.dev.Present(); err != nil {
		slog.Warn("presenting the frame failed", "err", err)
	}
	if took := time.Since(drawn); took > slowFrame && time.Since(d.slowSaid) > time.Minute {
		d.slowSaid = time.Now()
		slog.Info("screen: slow frame", "took", took.Round(time.Millisecond), "draw", painted.Sub(drawn).Round(time.Millisecond),
			"present", time.Since(painted).Round(time.Millisecond), "menu", s.menuOpen, "mode", s.menuMode, "now_playing", s.nowPlaying, "camera", s.showCamera)
	}
	for pending := true; pending; {
		select {
		case reply := <-d.shots:
			src := d.dev.Canvas()
			cp := image.NewRGBA(src.Rect)
			copy(cp.Pix, src.Pix)
			reply <- cp
		default:
			pending = false
		}
	}

	switch {
	case turning || (s.menuOpen && d.isSpinning()):
		return dialFrame
	case s.showCamera:
		// New frames wake the loop themselves; this only brings the view down when its time is up.
		return activeFrame
	case s.menuOpen && s.menuMode == modeWeather && s.radarOn:
		return radarStep
	case s.menuOpen && s.menuMode == modeWeather && s.sky != fxNone:
		return fxFrame
	case s.sheetOpen:
		return dialFrame // a finger dragging the page is followed smoothly
	case s.showDash && !s.menuOpen:
		return time.Second // what arrives for it wakes the loop itself
	case s.phase == "listening" || s.phase == "thinking" || s.phase == "replying" || s.showVolume || s.menuOpen || s.btPairing || s.call.Phase != phone.Idle || s.ringing.any():
		return activeFrame
	default:
		return time.Until(now.Truncate(idleFrame).Add(idleFrame))
	}
}

// Screenshot is the next frame drawn, whole, for checking a layout from a PC; nil if the screen is
// not open or draws nothing within two seconds (a dark panel does not draw).
func (d *Display) Screenshot() *image.RGBA {
	if d.dev == nil {
		return nil
	}
	reply := make(chan *image.RGBA, 1)
	select {
	case d.shots <- reply:
	default:
		return nil
	}
	d.wake()
	select {
	case img := <-reply:
		return img
	case <-time.After(2 * time.Second):
		return nil
	}
}

// setAtNight and nightHoursChanged are the Show's night light and its Home Assistant selects; the
// Spot's night only dims, and its hours are not in Home Assistant.
func (d *Display) setAtNight(int)     {}
func (d *Display) nightHoursChanged() {}
