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
// volume, a step per 60 pixels, while Swipe for volume is on; a held finger opens the ring menu
// (menu_spot.go). While the menu is open the touch screen follows every moving finger, so dragging
// round the ring turns the dial (or, for a value, is a jog wheel), and a tap in the middle does the
// item at the top.
//
// The backlight: the panel shows almost nothing below about 120 of 255 and glares at 255, so a
// brightness in percent spans backlightMin to the top. Through the night hours, 22:00 to 07:00 unless
// others are set (config Screen.Night), it is held to nightCeiling, or the face is left to the red night
// clock at the night light's level, or put out (night_spot.go).
//
// To Home Assistant the screen is a light with brightness, and a switch for auto-brightness, with the
// night's settings and Night mode beside them: the same entities the Show has.
package display

import (
	"context"
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
	"github.com/HuskerMinion/techo5/echod/internal/feature/assistant"
	"github.com/HuskerMinion/techo5/echod/internal/feature/btaudio"
	"github.com/HuskerMinion/techo5/echod/internal/feature/camstream"
	"github.com/HuskerMinion/techo5/echod/internal/feature/doorbell"
	"github.com/HuskerMinion/techo5/echod/internal/feature/hastate"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/feature/mute"
	"github.com/HuskerMinion/techo5/echod/internal/feature/notify"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	"github.com/HuskerMinion/techo5/echod/internal/feature/presence"
	"github.com/HuskerMinion/techo5/echod/internal/feature/remind"
	"github.com/HuskerMinion/techo5/echod/internal/feature/setup"
	"github.com/HuskerMinion/techo5/echod/internal/feature/talkback"
	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
	"github.com/HuskerMinion/techo5/echod/internal/feature/video"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/ambient"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/buttons"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/camera"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/screen"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
	"github.com/HuskerMinion/techo5/echod/internal/lib/fonts"
	"github.com/HuskerMinion/techo5/echod/internal/service"
)

func init() {
	component.Register(component.Device, Get(), component.Order(60),
		component.Supervise(service.Restart(time.Second, 30*time.Second)))
}

// hasEqualizer is whether this screen offers the Wave and Bars turn screens (render_turn_spot.go).
const hasEqualizer = true

// hasRainbow is whether this screen offers the Rainbow turn screen: not the Spot's, which is round.
const hasRainbow = false

// hasTimerStyles: the Spot's timers stay around its rim, whatever the Show's are set to.
const hasTimerStyles = false

// hasSkins: the Spot has the skins too, in their colors, fonts and filters (skin_spot.go); what the
// Show draws around its own clock is the Show's.
const hasSkins = true

// hasMicrophone: the Spot always has its microphones (the Show's is render_welcome.go).
var hasMicrophone = func() bool { return true }

const (
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
	// power is Screen power, the screen's on and off as a switch, and roomSw Screen ignores room light
	// commands (screen_room.go).
	power  *esphome.Switch
	roomSw *esphome.Switch
	clock  *esphome.Select
	// clockStyleSel is Clock style, how the clock looks all day (clock_style.go).
	clockStyleSel *esphome.Select
	// clockPos is Clock position, the clock across the middle or at the foot (clock_layout_spot.go).
	clockPos *esphome.Select
	// fontSel is Screen font, the font of every word the screen draws (font.go).
	fontSel *esphome.Select
	// styleNamed is the clock style a swipe turned to, said on the clock until styleNamedUntil.
	styleNamed      string
	styleNamedUntil time.Time
	// clockUp is the last frame having been the clock page itself, in whatever style: not the light
	// before an alarm, the screensaver, the night clock or another page. A swipe turns the style only
	// on it.
	clockUp bool
	// camTime is how long a camera opened from the screen stays up, and answerTime how long a turn's
	// words do once it is over.
	camTime    *esphome.Select
	answerTime *esphome.Select
	turnStyle  *esphome.Select // Turn screen: Classic, Wave or Bars
	skinSl     *esphome.Select // Screen skin
	// callBtn is the home screen's Call button, on or off (callbutton.go).
	callBtn *esphome.Switch
	// styleSwipeSw is Swipe between clock styles (style_swipe.go).
	styleSwipeSw *esphome.Switch
	// volumeSwipeSw is Swipe for volume (volume_swipe.go).
	volumeSwipeSw *esphome.Switch
	// weatherFx is the weather page's sky moving, on or off (weatherfx.go).
	weatherFx *esphome.Switch
	// muteRing is the muted ring drawn thin and dim, on or off (mutering_spot.go).
	muteRing *esphome.Switch
	lang     *esphome.Select

	mu      sync.Mutex
	on      bool
	ceiling int

	// The dashboard face (dashboard_spot.go): asked for, when last touched, whether the last frame
	// drew it, whether the touch screen follows fingers for it, where it is scrolled and on which
	// dashboard, a finger moving on it, a level being slid, the idle one put away until, and a finger
	// held still on it.
	dash          bool
	dashHeld      bool // put up by Home Assistant: stays until it is taken down, not spotDashForgotten
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
	settled       bool // level has reached the target; settle steps it there between readings
	view          voice.State
	viewAt        time.Time
	volume        int
	volAt         time.Time

	// menuOpen is the ring menu on the screen, menuMode what it shows; menuSel the item at (or turning
	// to) the top; menuRot the dial's rotation now and menuRest where it is heading; menuAt the last
	// touch; spinning a finger turning it, spinAngle its last direction from the center; jogTurn how
	// far a jog wheel has turned toward its next step; restartArm the first tap on Restart and
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

	// The alert face (alerts_spot.go): up until alertUntil, on alertIdx of the alerts at home,
	// scrolled alertScroll lines.
	alertUntil  time.Time
	alertIdx    int
	alertScroll int

	// reminderID is the reminder on the face, so a new one starts at the top; reminderScroll how far
	// its words are dragged up, and the drag's own start while a finger is on it.
	reminderID         string
	reminderScroll     int
	reminderDragging   bool
	reminderDragFrom   int
	reminderDragScroll int

	// wasNight is whether the last backlight was set for the night, so the change of hour relights.
	wasNight bool

	// touchedAt is the last finger on the face. nightGlow is the night having left the red clock alone
	// on the face at the night light's level, until a touch or the end of the night takes it down
	// (night_spot.go); redShown is the frame drawn last being that clock. nightDark is the face put out
	// by the night (Screen off) rather than by anyone.
	touchedAt time.Time
	nightGlow bool
	redShown  bool
	nightDark bool
	// soonAt is when the red clock was last asked for at once (nightSoon): the night starting, Night
	// clock chosen, or the settings or the menu closing. A touch or a turn after it waits nightIdle as
	// before. nightIn and nightUsing are what the last frame found, to see the night begin and the
	// settings close.
	soonAt     time.Time
	nightIn    bool
	nightUsing bool
	// glowUntil is the end of a step of the night clock's brightness showing on the backlight, from
	// the settings sheet at night (stepGlow).
	glowUntil time.Time
	// glowTimer relights the face once a step's glowPreview is over: one timer, put off by each step,
	// so a run of steps ends in one relight rather than one for each, and something that holds the
	// face (a test, here) can stop it. Under mu.
	glowTimer *time.Timer

	// The night's settings in Home Assistant, as on the Show (night_select.go): its hours, their two
	// ends to the quarter hour, what it does to the face, the red clock's look and brightness.
	nightHours, atNight  *esphome.Select
	nightStart, nightEnd *esphome.Select
	nightStyle           *esphome.Select
	glowLevel            *esphome.Number
	// nightMode is Night mode: Home Assistant turning the night on and off; nightShown what it last said.
	nightMode  *esphome.Switch
	nightShown bool
	nightSeen  bool // nightShown has been sent at least once
	// nightPubMu keeps publishing the Night mode switch to one caller at a time (showNightMode).
	nightPubMu sync.Mutex
	// lightMu makes each relight one step, from working out the level to writing it.
	lightMu sync.Mutex
	// sunriseLit is the light before an alarm having been on at the last settle tick (autobright.go).
	sunriseLit bool

	// slideshowIdleSince is when the face last became the plain idle clock (nothing else showing);
	// zero while it is not. Screensaver mode waits for this to run long enough before taking over.
	slideshowIdleSince time.Time

	// weatherArmed is a weather question in progress; weatherUntil when the weather face comes down.
	weatherArmed bool
	weatherUntil time.Time
	calUntil     time.Time // the calendar face's, the same way

	// quiet is a turn that was a screen command ("go home", "show the deck"): its words and reply are
	// not shown, so the screen moves at once, as on the Show. radioCue is when Home Assistant last
	// named a station as playing, which comes seconds before the stream. radar turns the weather face
	// to the rain map; radioSel is the station list's middle row.
	quiet    bool
	radioCue time.Time
	radar    bool
	radioSel int
	// musicQuiet is how long the music on the face has been paused or stopped, and musicAway whether
	// that has gone on past pausedAway: the clock comes back, and the face's taps go back to it, while
	// the track stays held for play to pick up again.
	musicQuiet quietTimer
	musicAway  bool
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

	// The video face (video_spot.go), as the Show's display holds its page.
	videoUntil    time.Time
	videoOnScreen bool
	videoTried    uint64
	videoPainting bool
	videoOver     image.Rectangle
	vp            videoPainter
	vask          askLatch
	videoLit      uint64

	// notifyLit is the last notification that lit the face, once per notification like videoLit.
	// notifySeen is the one the face may show at night: the one that came up by day, or that a touch
	// released (notice_spot.go). Both under mu.
	notifyLit  string
	notifySeen string
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
	d.buildRoomLights()
	d.clock = clockSelect(d.wake)
	d.clockStyleSel = clockStyleSelect(d)
	d.clockPos = clockPositionSelect(d.wake)
	d.fontSel = fontSelect(d)
	setup.SetScreen(&setup.ScreenChoices{Styles: clockStyleOptions(), Current: clockStyleIndex, Choose: d.setClockStyle,
		Fonts: fonts.Names(), FontNow: fontIndex, ChooseFont: d.setFont,
		OwnFonts: ownFonts, AddFont: d.addOwnFont, RemoveFont: d.removeOwnFont, MaxFont: fonts.MaxOwnFile,
		Places: spotWorld, Swipe: func(on bool) { setStyleSwipe(d.styleSwipeSw, on) }, VolumeSwipe: func(on bool) { setVolumeSwipe(d.volumeSwipeSw, on) },
		IgnoreRoomLights: d.setRoomLightsIgnored})
	d.camTime = cameraTimeSelect()
	d.answerTime = answerTimeSelect()
	d.turnStyle = turnStyleSelect(d.wake)
	d.skinSl = skinSelect(d.wake)
	d.callBtn = callButtonSwitch(d.wake)
	d.styleSwipeSw = styleSwipeSwitch()
	d.volumeSwipeSw = volumeSwipeSwitch()
	d.weatherFx = weatherAnimationSwitch(d.wake)
	d.muteRing = muteRingSwitch(d.wake)
	d.nightHours = nightHoursSelect(d)
	d.nightStart, d.nightEnd = nightEndSelect(d, true), nightEndSelect(d, false)
	d.atNight = atNightSelect(d)
	d.nightStyle = nightStyleSelect(d)
	d.glowLevel = glowNumber(d)
	d.nightMode = nightModeSwitch(d)
	d.lang = langSelect()
	voice.Changed.Listen(d.changed)
	media.Get().OnVolume.Listen(d.volumeMoved)
	ambient.Get().Lux.Listen(d.lux)
	touch.Get().Gestures.Listen(d.gesture)
	timer.Get().Changed.Listen(func(struct{}) { d.ringLights() })
	alarm.Get().Changed.Listen(func(struct{}) { d.ringLights() })
	remind.Get().Changed.Listen(func(struct{}) { d.reminderLights() })
	notify.Get().Changed.Listen(func(struct{}) { d.notifyLights() })
	notify.SetNight(nightNow)
	home.Get().Changed.Listen(func(struct{}) { d.wake() })
	// An announcement arriving lights a face the night put out (frame).
	announce.Get().Changed.Listen(func(struct{}) { d.wake() })
	doorbell.Get().Changed.Listen(func(struct{}) { d.wake() })
	camstream.Get().Changed.Listen(func(struct{}) {
		d.relight(true) // a dark face keeps a little light while the camera is watched (watchedLight)
		d.wake()
	})
	d.watchRoom()
	talkback.Get().Changed.Listen(func(struct{}) { d.wake() })
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
	d.listenDashboard()
	assistant.SetScreen(d.showPageSpot)
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

// turnStyleSel is the Turn screen setting in Home Assistant.
func (d *Display) turnStyleSel() *esphome.Select { return d.turnStyle }

// timerStyleSel: the Spot has no Timers setting.
func (d *Display) timerStyleSel() *esphome.Select { return nil }

// skinSel is the Screen skin setting in Home Assistant.
func (d *Display) skinSel() *esphome.Select { return d.skinSl }

func (d *Display) Entities() []esphome.Entity {
	return []esphome.Entity{d.light, d.power, d.roomSw, d.auto, d.clock, d.clockStyleSel, d.clockPos, d.camTime, d.answerTime, d.turnStyle, d.skinSl, d.callBtn, d.weatherFx, d.muteRing, d.lang, d.styleSwipeSw, d.volumeSwipeSw, d.fontSel,
		d.nightHours, d.nightStart, d.nightEnd, d.nightMode, d.atNight, d.nightStyle, d.glowLevel}
}

// Restore lights the panel the way it was left.
func (d *Display) Restore(c config.Config) {
	setClock24(d.clock, c.Screen.Clock24)
	d.roomSw.Set(c.Screen.IgnoreRoomLights)
	d.describeScreen()
	d.clockStyleSel.Set(clockStyles[clockStyleIndex()].label)
	d.clockPos.Set(clockPositions[clockPositionIndex()].label)
	d.fontSel.Set(fonts.Names()[fontIndex()])
	d.camTime.Set(cameraTimes[cameraTimeIndex()].label)
	d.answerTime.Set(answerTimes[answerTimeIndex()].label)
	d.turnStyle.Set(turnStyles[turnStyleIndex()].label)
	d.skinSl.Set(skins[skinIndex()].label)
	setCallButton(d.callBtn, c.Screen.CallButton)
	d.styleSwipeSw.Set(!c.Screen.NoStyleSwipe)
	d.volumeSwipeSw.Set(!c.Screen.NoVolumeSwipe)
	setWeatherAnimation(d.weatherFx, !c.Screen.WeatherStill)
	setMuteRingSubtle(d.muteRing, c.Screen.MuteRingSubtle)
	d.nightHoursChanged()
	d.atNight.Set(atNightOptions[atNightIndex()])
	d.nightStyle.Set(nightStyleOptions[nightStyleIndex()])
	d.glowLevel.Set(float32(d.glowSetting()))
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

// wakeLight is the light before an alarm as this face shows it: all of it, here.
func (d *Display) wakeLight(now time.Time) float64 { return sunriseProgress(now) }

func (d *Display) apply(on bool, pct int, save bool) {
	presence.Hush() // the screen's own light is about to change; the camera is not to take it for somebody
	pct = min(max(pct, 0), 100)
	d.mu.Lock()
	d.on, d.ceiling = on, pct
	// Somebody chose this, so the night no longer owns the face: a face put to sleep in the night stays
	// asleep when it ends, as on the Show.
	if save {
		d.nightDark = false
	}
	d.mu.Unlock()
	d.relight(true)

	d.light.Set(esphome.LightState{On: on, Brightness: float32(pct) / 100, ColorMode: esphome.ColorModeBrightness})
	if d.power != nil {
		d.power.Set(on)
	}
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
	if jump {
		presence.Hush() // a sudden change of the screen's light, not somebody
	}
	// One at a time from working out the level to writing it: the settle ticker, a reading and a
	// setting changed on the screen all relight, and a level worked out first must not land last.
	d.lightMu.Lock()
	defer d.lightMu.Unlock()
	night := nightNow(time.Now())
	d.mu.Lock()
	// The hour turning is a change to show at once, whoever relights first: the settle ticker taking
	// it would otherwise leave the frame nothing to jump for, and it would fade in.
	if d.wasNight != night {
		jump = true
	}
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
		if d.nightGlow || (night && time.Now().Before(d.glowUntil)) {
			// The red night clock: the night light's level, whatever the room and the brightness ask. A
			// step of its brightness on the settings sheet shows it the same way for a moment.
			target = float64(d.glowBacklight())
		}
	}
	// The light before an alarm takes the backlight over while it runs: it starts under whatever the
	// room would otherwise ask for and ends at the face's own brightness.
	if p := sunriseProgress(time.Now()); p > 0 && d.on {
		full := backlightMin + (screen.BacklightMax-backlightMin)*float64(min(max(d.ceiling, 0), 100))/100
		target = backlightMin + (full-backlightMin)*sunriseLevel(p)
	}
	target = watchedLight(target, d.on, camstream.Watching(), backlightMin)
	if jump || d.level == 0 {
		d.level = target
	} else {
		d.level += (target - d.level) * autoSmooth
	}
	// Close enough is the target itself: smoothing alone would stop up to half a step short, which
	// rounds a step under it.
	if math.Abs(target-d.level) < 0.5 {
		d.level, d.settled = target, true
	} else {
		d.settled = false
	}
	level := int(math.Round(d.level))
	d.mu.Unlock()

	if err := screen.SetBacklight(level); err != nil {
		slog.Warn("setting the backlight failed", "err", err)
	}
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
	// A question about the weather brings the weather face up once the answer is done. Not when the
	// device answers directly: its assistant knows where a question was about and puts the face up
	// itself (showPageSpot).
	if newHeard && aboutWeather(s.Heard) && !config.Get().Brain.Direct() {
		d.weatherArmed = true
		d.radar = aboutRadar(s.Heard)
	}
	// "Show the front door" goes up at once, while the assistant answers; "go home" takes it down.
	if newHeard {
		if entity := home.Get().MatchCamera(s.Heard); entity != "" {
			d.weatherArmed, d.quiet = false, true
			d.closeAlert()
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
			d.closeAlert()
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
			d.closeAlert() // the forecast or the rain map asked for comes up over an alert
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
	d.touchedAt = time.Now()
	d.mu.Unlock()
	// A notification that came up at night waits for a touch (releaseNotice), but not for one that stops
	// a ring: that one is for the alarm. The touch that releases it only releases: whoever touched the
	// face has not seen the card yet, so it is not a tap on anything.
	held := d.noticeHeld()
	ringTap := g.Kind == touch.Tap && ringingNow(time.Now()).any()

	if !on {
		// A dark panel only lights; nothing under the finger is acted on. A long press on a face the
		// night put out goes on to open the ring menu, as it does on the red night clock.
		if g.Kind == touch.Tap || g.Kind == touch.Hold {
			if !ringTap {
				d.releaseHeld()
			}
			byNight := d.darkByNight()
			d.apply(true, d.ceilingOrDefault(), true)
			if byNight && g.Kind == touch.Hold && !held && !ringingNow(time.Now()).any() {
				d.mu.Lock()
				d.openMenu(modeMain, itemTalk)
				d.spinning, d.spinAngle = true, fingerAngle(g.X, g.Y)
				d.mu.Unlock()
				d.wake()
			}
		}
		// Unless something is ringing, where spending the press on the backlight is the wrong trade:
		// the person is reaching for a noise, not for a screen they cannot see. A ring lights the
		// panel by itself when it starts (ringLights), so this is the panel having been put to sleep
		// after that — from the menu — and the tap would otherwise be swallowed.
		if !ringingNow(time.Now()).any() {
			return
		}
	}

	// The red night clock: a touch lights the face as it is by night, dimmed, and only lights it, as a
	// touch on a dark face does: nobody can see what they are pressing on a clock alone at a glow. A ring
	// under it is stopped or snoozed as on its own face, and a notification held for the night is only
	// released. A long press goes on to open the ring menu, so the menu is one press away in the dark.
	if d.redClockUp() {
		if d.ringGesture(g) {
			return
		}
		if held {
			d.releaseHeld()
			d.wake()
			return
		}
		switch g.Kind {
		case touch.Tap, touch.Hold, touch.SwipeUp, touch.SwipeDown, touch.SwipeLeft, touch.SwipeRight:
			d.liftNight()
		}
		if g.Kind == touch.Hold {
			d.mu.Lock()
			d.openMenu(modeMain, itemTalk)
			d.spinning, d.spinAngle = true, fingerAngle(g.X, g.Y)
			d.mu.Unlock()
			d.wake()
		}
		return
	}

	if d.callGesture(g) || d.ringGesture(g) || d.pinGesture(g) {
		return
	}
	// A browser asking to be let in: its face takes every tap, and only the two answers decide.
	if setup.Get().Waiting() {
		if g.Kind == touch.Tap {
			if allow, answered := askTapSpot(g.Y); answered {
				answerSetup(allow)
			}
		}
		d.wake()
		return
	}
	// A DLNA video asking to be shown, the same way; then the video face, which takes every finger.
	if _, _, _, asking := video.Get().Asking(); asking {
		if id, ok := d.vask.answerable(time.Now()); ok && g.Kind == touch.Tap {
			if allow, answered := askTapSpot(g.Y); answered {
				go video.Get().Answer(id, allow)
			}
		}
		d.wake()
		return
	}
	if d.videoUpSpot() {
		d.videoGestureSpot(g)
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
	// The touch that releases a held notification only releases it, rather than falling through to what
	// is under the finger. The pages that take every tap have had their say above. A menu left open is
	// put away now: the card is about to be drawn over it, and notifyLights left it alone for the night.
	if held {
		d.releaseHeld()
		d.wake()
		return
	}
	// A notification takes the face too: a tap puts it away and tells Home Assistant. A camera view is
	// over it (the card stays up underneath and shows when the view ends), so the view takes the taps.
	if _, camera := home.Get().Camera(); !camera {
		if n, up := notify.Get().Card(); up {
			if g.Kind == touch.Tap {
				go notify.Get().DismissID(n.ID)
			}
			d.wake()
			return
		}
	}
	// The alert face takes every gesture while it is up.
	if d.alertUpSpot() {
		d.alertGestureSpot(g)
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
	// A cue turn's captions over the dashboard, the music or the clock: a tap on them puts them away,
	// and anywhere else is the face's (turn_cue.go).
	if d.captionsTap(g) {
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
			// The sound's control silences the sound and leaves the view up, which is the whole use of
			// it at a doorbell; anywhere else on the face takes the view down as it always has. Talk
			// starts or ends talking through the camera.
			if d.r != nil && d.r.cameraSoundTapped(g.X, g.Y) {
				// Silence it, or ask for it again: the control is a toggle, and the view stays either way.
				go home.Get().ToggleCameraSound()
				return
			}
			if d.r != nil && d.r.cameraTalkTapped(g.X, g.Y) {
				go talkback.Get().Toggle(v.Entity)
				return
			}
			go closeCamera(v)
			return
		case touch.SwipeLeft:
			// A captioned view is a notification's, and stepping to the next camera would replace it
			// without a word to Home Assistant; it stays, and a tap still dismisses it.
			if v.Caption == nil {
				go stepCamera(v.Entity, +1)
			}
			return
		case touch.SwipeRight:
			if v.Caption == nil {
				go stepCamera(v.Entity, -1)
			}
			return
		case touch.Hold:
			d.mu.Lock()
			d.openMenu(modeCameras, cameraItem(cameraIndex(v.Entity)))
			d.mu.Unlock()
			d.wake()
			return
		}
	}
	if d.musicFaceUp() {
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
		// A finished turn's words: a tap puts them away rather than starting another turn.
		if d.answerUp(time.Now()) {
			d.clearAnswer()
			return
		}
		// The wake light's face, held at full after an alarm with nothing ringing: a tap lets it go, as
		// stop does (#161). While it is only coming up before an alarm, the tap is the clock's as ever.
		if tapLetsGoOfLight(time.Now()) {
			d.wake()
			return
		}
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
		// The clock's alert pill opens the alert rather than starting a turn.
		if d.r != nil && d.r.alertPillTapped(g.X, g.Y) && len(home.Get().Alerts().Here) > 0 {
			d.mu.Lock()
			d.openAlertSpot(0)
			d.mu.Unlock()
			d.wake()
			return
		}
		voice.Get().Action()
	case touch.SwipeUp:
		swipeVolume(+1)
	case touch.SwipeDown:
		swipeVolume(-1)
	case touch.SwipeLeft, touch.SwipeRight:
		// Across the clock face, the next clock style or the one before: only on the face itself, not
		// while a turn is on or its words are up, nor over the screensaver or the light before an alarm.
		d.mu.Lock()
		idle, up := pageIdle(d.view.Phase), d.clockUp
		d.mu.Unlock()
		if !idle || !up || d.answerUp(time.Now()) || !styleSwipeOn() {
			return
		}
		if g.Kind == touch.SwipeLeft {
			d.stepClockStyle(+1)
		} else {
			d.stepClockStyle(-1)
		}
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
	touch.Get().SetFollow(mode != modeWeather && mode != modeContacts && mode != modeCalendar)
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

	case mode == modeCalendar:
		if g.Kind == touch.Tap {
			d.closeMenu()
		} else {
			d.calUntil = time.Now().Add(calendarIdle)
		}

	case mode == modeWeather:
		switch g.Kind {
		case touch.Tap:
			// The alert pill over the rain map opens the alert; anywhere else closes the face.
			if d.radar && d.r != nil && d.r.alertPillTapped(g.X, g.Y) && len(home.Get().Alerts().Here) > 0 {
				d.openAlertSpot(0)
			} else {
				d.closeMenu()
			}
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
		go home.Get().ShowCamera(home.LocalCamera, cameraScreenTime())
	case itemWeather:
		d.locked(func() {
			d.openMenu(modeWeather, "")
			d.weatherUntil = time.Now().Add(weatherIdle)
		})
	case itemCalendar:
		d.locked(func() {
			d.openMenu(modeCalendar, "")
			d.calUntil = time.Now().Add(calendarIdle)
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
	fw, fh := dev.FrameSize()
	w, h := dev.Size()
	video.Get().UseScreen(video.Screen{W: w, H: h, Rotated: dev.Rotated(), PixFmt: dev.PixFmt()}, fw, fh)
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
	go d.settle(ctx)
	for {
		wait := d.frame()
		if d.videoPainting {
			d.vp.paint(ctx, d.dev, d.poke, wait, d.videoOver)
			if ctx.Err() != nil {
				return nil
			}
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-d.poke:
		case <-time.After(wait):
		}
	}
}

func (d *Display) frame() (next time.Duration) {
	d.videoPainting, d.vp.dark = false, false
	now := time.Now()
	if d.dev != nil {
		applySkin(d.dev, side, side)
	} else {
		applySkin(nil, side, side)
	}
	d.mu.Lock()
	nightChanged := d.wasNight != nightNow(now)
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
		case d.menuMode == modeCalendar:
			if now.After(d.calUntil) {
				d.closeMenu()
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
	if d.sheetOpen && pinIsOpen() {
		// The PIN pad over the settings takes the taps, and someone typing is someone there: the
		// settings wait behind it rather than closing on them, which would lose what the PIN was for.
		d.sheetAt = now
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

	_, reminding := remind.Get().Showing()
	vs := video.Get().State()
	busy := view.Phase != "idle" || sheetOpen || ringingNow(now).any() || phone.Get().Busy() || pinIsOpen() ||
		sunriseProgress(now) > 0 || setup.Get().Waiting() || reminding || vs.Active()
	if d.awayTick(now, on, busy, nightNow(now)) {
		d.mu.Lock()
		on = d.on
		d.mu.Unlock()
	}
	if vs.Active() && vs.ID != d.videoLit && (vs.Phase != video.Asking || !nightNow(now)) {
		// A video starting lights a dark face, once per video; a DLNA video's question only by day.
		d.videoLit = vs.ID
		if !on {
			d.apply(true, d.ceilingOrDefault(), false)
			on = true
		}
	}
	if !on && d.darkByNight() && (sunriseProgress(now) > 0 || announcing()) {
		// A face the night put out comes on for the light before an alarm and for an announcement, which
		// are shown on the red night clock too; a ring, a call, a reminder and a video light it themselves.
		d.apply(true, d.ceilingOrDefault(), false)
		on = true
	}
	// The red night clock up or down, or the face put out or back; relit at once, and drawn from this
	// frame.
	d.night(now, on, view)
	d.mu.Lock()
	on = d.on
	s.redClock, s.redStyle = d.nightGlow && nightClockOn(), config.Get().Screen.NightClockStyle
	d.mu.Unlock()
	if !on && !video.Get().State().Active() {
		d.vp.drop() // a video that ended while the panel was dark: its frame goes with it
	}
	playing := !on && vs.Frames
	if playing {
		// Dark with a video playing: its frames are taken as they fall due so its sound goes on.
		video.Get().Covered(false)
		d.videoPainting, d.vp.dark, d.videoOver = true, true, image.Rectangle{}
	}
	if !on && camstream.Watching() {
		// Dark, with the camera watched over the network: its mark, alone on black (not a video's last
		// frame), at the dimmest light (relight), so the room can tell. Looked at again until the
		// watching ends.
		d.r.watchedDark()
		unskinned(d.dev, true)
		if err := d.dev.Present(); err != nil {
			slog.Warn("presenting the frame failed", "err", err)
		}
		if playing {
			return videoRecheck
		}
		return 5 * time.Second
	}
	if playing {
		return videoRecheck
	}
	if !on {
		if d.darkByNight() {
			return time.Minute // until the night ends, or something lights the face
		}
		return time.Hour
	}
	if view.Phase == "idle" && (view.Heard != "" || view.Reply != "") && now.Sub(at) < linger() {
		s.phase = "lingering"
	}
	if quiet && (s.phase == "thinking" || s.phase == "replying" || s.phase == "lingering") {
		// A screen command: the screen it asked for is the answer, not the words.
		s.phase, s.heard, s.reply = "idle", "", ""
	}
	if cueOn() {
		s.cue, s.phase, s.heard, s.reply = cueTurn(s.phase, s.heard, s.reply, captionsOn())
	}
	if equalizerOn() && (s.phase == "listening" || s.phase == "thinking" || s.phase == "replying" || s.phase == "lingering") {
		s.eq = eqFor(s.phase, nightNow(now), now)
		s.eq.wave = waveOn()
	}
	s.muted, _ = mute.Get().Muted()
	s.mutedSubtle = muteRingSubtle.Load()
	// A stream this player is carrying is the room's when it is what is being heard: the face names it,
	// and says what it is doing, though the audio never passes through this player's own stream. Both,
	// not just playing: the face tests paused first, so a station left paused underneath would label
	// somebody else's track as Paused - and a remote merely holding the speaker, with a station playing
	// underneath it, is not the room's at all (see media.Player.Carried).
	s.playing, s.paused = musicState()
	s.maxVolume = config.VolumeSteps
	s.pin = pinNow(now)
	relockOnClose(s.sheetOpen)
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
	s.atDoor = s.call.Phase != phone.Idle && doorbell.Get().AtDoor(now)
	s.weather = home.Get().Weather()
	s.style = styleFactsFor(clockStyle(), now)
	s.style.named = d.styleName(now)
	s.camera, s.showCamera = home.Get().Camera()
	s.camera, s.showCamera = d.gateCamera(s.camera, s.showCamera, nightNow(now))
	s.cameraSound, s.cameraSoundLive = home.Get().CameraSoundOn(), home.Get().CameraSoundLive()
	if s.showCamera {
		s.talkOffered, s.talk = talkback.Offered(s.camera.Entity), talkback.Get().State()
	}
	s.cameraLive = camera.Get().Running()
	bt := btaudio.Get().State()
	s.btPairing = bt.Pairing
	if s.menuOpen {
		// The dial's line under the calendar, and the calendar face itself.
		if src := home.Get().CalendarSources(); len(src) > 0 {
			s.calToday, s.calNext = comingUp(now, 2)
			s.calOrder = src
		}
	}
	if s.menuOpen && s.menuMode == modeWeather {
		s.forecast = home.Get().Forecast()
		// Asked for while the weather is up, so the rain map is ready when it is turned to.
		if v := home.Get().Radar(); s.radarOn {
			s.radar = v
		} else {
			s.sky = skyNow(weatherNow(s.weather, s.forecast, s.now))
		}
	}
	music := d.showsNowPlaying()
	s.nowPlaying = s.phase == "idle" && music
	if s.nowPlaying || (s.menuOpen && s.menuMode == modeRadio) {
		s.radio = home.Get().Radio()
		if s.radio.Followed {
			s.playing, s.paused = s.radio.Playing, s.radio.Paused
		}
	}
	// Music quiet for pausedAway gives the face back to the clock; playing again brings it back. The
	// time runs through a turn, so a question asked while the music is paused does not bring the face
	// back for another half minute after its answer.
	d.mu.Lock()
	away := d.musicQuiet.over(now, music, s.playing)
	d.musicAway = away
	d.mu.Unlock()
	if away {
		s.nowPlaying = false
	}
	if s.nowPlaying || (s.menuOpen && s.menuMode == modeRadio) {
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
	s.notification, s.showNotification = notify.Get().Card()
	d.gateNotice(&s, nightNow(now))
	if s.reminder.From != config.Get().Device.Name {
		s.reminderFrom = s.reminder.From
	}
	d.mu.Lock()
	s.reminderScroll = d.reminderScroll
	d.mu.Unlock()
	d.alertSceneSpot(&s, now)
	s.missed = missedNote(now, true)

	if boring {
		s.sunrise, s.sunriseFace = sunriseProgress(now), config.Get().Alarms.SunriseFace
		// The weather art only where it will be seen: not under the night clock or the light before an
		// alarm, which take the whole screen, where composing it each second is work for nothing.
		artSeen := !s.redClock && s.sunrise == 0
		s.slideshow = home.Get().SlideshowBackground()
		if home.Get().SlideshowMode() == config.SlideshowBackground && artSeen {
			if art, fx := sceneArt(now, side, side); art != nil {
				s.slideshow, s.artFx = art, fx
			}
		}
		if s.slideshow == nil {
			s.slideshowTrouble = home.Get().SlideshowTrouble()
		}
	}
	wait := home.Get().SlideshowIdleTimeout()
	d.mu.Lock()
	d.clockUp = boring && s.sunrise == 0 && s.slideshowScreensaver == nil && !s.redClock
	if !boring {
		d.slideshowIdleSince = time.Time{}
	} else if d.slideshowIdleSince.IsZero() || (s.cue != "" && now.Sub(d.slideshowIdleSince) < wait) {
		// A turn under a cue is somebody at the screen, as any turn is: the screensaver's wait starts
		// again from it. One already up stays up under the light, as any page under a cue does.
		d.slideshowIdleSince = now
	}
	idleSince := d.slideshowIdleSince
	d.mu.Unlock()
	if boring && !idleSince.IsZero() && now.Sub(idleSince) >= wait {
		s.slideshowScreensaver = home.Get().SlideshowScreensaverPhoto()
		if home.Get().SlideshowMode() == config.SlideshowScreensaver && !s.redClock && s.sunrise == 0 {
			if art, fx := sceneArt(now, side, side); art != nil {
				s.slideshowScreensaver, s.artFx = art, fx
			}
		}
		s.slideshowOverlay = home.Get().SlideshowOverlay()
		if s.slideshowScreensaver != nil && !home.Get().SlideshowArt() {
			s.slideshowCaptions = home.Get().SlideshowCaptions()
		}
	}

	d.dashSceneSpot(&s)
	s.callButton = callButton.Load()
	d.videoSceneSpot(&s, now)
	d.mu.Lock()
	if s.showDash && d.dashHeld {
		// A dashboard Home Assistant put up is shown over the night, as on the Show: somebody set it up
		// to be seen.
		s.redClock = false
	}
	d.redShown = s.nightClockFace()
	d.mu.Unlock()
	if s.showVideo && s.videoLive {
		// The picture's own frames go to the panel; the canvas holds only the controls.
		d.r.draw(s)
		d.videoPainting, d.videoOver = true, d.r.videoOver
		for pending := true; pending; {
			select {
			case reply := <-d.shots:
				reply <- d.vp.shot(d.dev, d.dev.Canvas())
			default:
				pending = false
			}
		}
		return d.videoNextSpot(now)
	}
	if !s.showVideo && !s.video.Active() {
		d.vp.drop() // kept while the video is only covered (the Show's frame loop says why)
	}
	drawn := time.Now()
	d.r.draw(s)
	d.mu.Lock()
	d.callShown = d.r.callDrawn
	d.mu.Unlock()
	painted := time.Now()
	unskinned(d.dev, s.nightClockFace() || s.muted || s.cameraLive)
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

	if cueFace(s) && cueLit(s.cue) {
		// The cue's rim breathes and runs over whatever face is up; a face that moves faster keeps
		// its own pace.
		defer func() { next = min(next, cueFrame) }()
	}
	switch {
	case turning || (s.menuOpen && d.isSpinning()):
		return dialFrame
	case s.showCamera && stillOnly(s.camera):
		return time.Until(now.Truncate(idleFrame).Add(idleFrame)) // a still: its picture arriving wakes the loop
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
	case d.r.artDrawn:
		return artFxFrame // rain or snow is falling over the weather art on the screen
	case s.eq != nil && !s.showVolume && !s.menuOpen && !s.sheetOpen && !s.showCamera && s.call.Phase == phone.Idle &&
		!s.ringing.any() && !s.setupAsking && !s.announceRecording && !s.showReminder && !s.showAnnouncement && !s.showNotification &&
		!s.showAlert && !(s.phase == "lingering" && s.eq.quiet):
		if s.eq.wave || s.eq.style == "orb" {
			return waveFrame // ten a second: the orb is a soft glow that needs no more
		}
		return eqFrame // the bars are moving
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

// popupSettingsChanged: the Spot has no event pop-ups yet.
func (d *Display) popupSettingsChanged() {}

// showPageSpot is the voice assistant putting a face up (feature/assistant): the forecast or the rain
// map once the answer has been said, as a question about the weather does, or the calendar.
func (d *Display) showPageSpot(page string) bool {
	switch page {
	case "weather", "radar":
		d.mu.Lock()
		d.weatherArmed, d.radar = true, page == "radar"
		d.mu.Unlock()
		d.wake()
		return true
	case "calendar":
		if len(home.Get().CalendarSources()) == 0 {
			return false
		}
		d.locked(func() {
			d.openMenu(modeCalendar, "")
			d.calUntil = time.Now().Add(calendarIdle)
		})
		d.wake()
		return true
	}
	return false
}
