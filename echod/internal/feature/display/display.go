//go:build !dot && !spot

// Package display is the Echo Show's screen: what the device shows on it, what a finger on it does,
// and what Home Assistant gets for it.
//
// The screen follows the conversation. Idle, it is a clock; while a turn runs it says what the
// device is doing and shows the words — what was heard, then the answer — and lets them linger a
// while after the turn ends. Everything is drawn by the daemon itself onto the kernel framebuffer
// (hardware/screen): no compositor, no browser, no Android.
//
// A tap does what the Dot's action button does: starts a turn, or ends the one running; on a dark
// screen it only lights it. A vertical swipe is the volume, a notch per step, with the level shown
// while it moves. The room's light dims the panel when auto-brightness is on; what Home Assistant
// sets is the ceiling.
//
// To Home Assistant the screen is a light with brightness only — on/off and how bright, which is
// what people automate — plus a switch for auto-brightness.
package display

import (
	"context"
	"errors"
	"image"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/alarm"
	"github.com/HuskerMinion/techo5/echod/internal/feature/announce"
	"github.com/HuskerMinion/techo5/echod/internal/feature/assistant"
	"github.com/HuskerMinion/techo5/echod/internal/feature/btaudio"
	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
	"github.com/HuskerMinion/techo5/echod/internal/feature/deck"
	"github.com/HuskerMinion/techo5/echod/internal/feature/hastate"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/feature/mute"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	"github.com/HuskerMinion/techo5/echod/internal/feature/presence"
	"github.com/HuskerMinion/techo5/echod/internal/feature/remind"
	"github.com/HuskerMinion/techo5/echod/internal/feature/security"
	"github.com/HuskerMinion/techo5/echod/internal/feature/setup"
	"github.com/HuskerMinion/techo5/echod/internal/feature/talkback"
	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
	"github.com/HuskerMinion/techo5/echod/internal/feature/video"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/ambient"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/screen"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wifi"
	"github.com/HuskerMinion/techo5/echod/internal/service"
)

func init() {
	// A panel that cannot be opened is retried rather than given up on: the device answers without
	// it, and a boot where the node was late should still end with a screen.
	component.Register(component.Device, Get(), component.Order(60),
		component.Supervise(service.Restart(time.Second, 30*time.Second)))
}

// hasEqualizer is whether this screen offers the equalizer turn screen: the Show's does; the Spot's
// round one would want its own.
const hasEqualizer = true

// hasNightSwitch is whether Home Assistant can turn the night on and off here (Night mode).
const hasNightSwitch = true

const (
	// volumeShow is how long the level stays up after it last moved.
	volumeShow = 2 * time.Second

	// idleFrame and activeFrame are how often the screen is redrawn: once a second for a clock, and
	// fast enough for the listening indicator to breathe and the volume to follow a finger.
	idleFrame = time.Second

	// flipFrame is how often the night's flip clock is drawn while a card is flipping.
	flipFrame   = 33 * time.Millisecond
	activeFrame = 150 * time.Millisecond

	// floor is the dimmest an "on" backlight goes: Fire OS's own floor in a dark room. The night light's
	// lowest steps glow at 1 to 3 (glowSteps), so the panel is plainly lit here. It is under a Brightness
	// of 1 or 2 percent too, which gives 4 and 5 rather than 8.
	floor = 4

	// Auto-brightness: the fraction of the ceiling the room's light allows, from the dimmest (the
	// Dimmest setting, defaultDimmest percent until it is set) at 1 lux and below, rising on a log curve
	// to the full ceiling at brightLux. Applied through a running average so a passing shadow does not
	// flicker the panel.
	defaultDimmest = 12
	darkLux        = 2.0
	brightLux      = 400.0
	autoSmooth     = 0.25
)

type Display struct {
	// holdsMu makes reading whether holds are wanted and telling the touchscreen one step (applyHolds).
	holdsMu sync.Mutex

	light *esphome.Light
	auto  *esphome.Switch
	clock *esphome.Select
	// clockStyleSel is Clock style, how the clock looks all day (clock_style.go).
	clockStyleSel *esphome.Select
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
	clockPos   *esphome.Select // Clock position, and dateCol Date color (clock_layout.go)
	dateCol    *esphome.Select
	answerTime *esphome.Select
	turnStyle  *esphome.Select
	clockTap   *esphome.Select // what a tap on the clock does (clock_tap.go)
	// callBtn is the home screen's Call button, on or off (callbutton.go).
	callBtn *esphome.Switch
	// styleSwipeSw is Swipe between clock styles (style_swipe.go).
	styleSwipeSw *esphome.Switch
	// weatherFx is the weather page's sky moving, on or off (weatherfx.go).
	weatherFx *esphome.Switch
	lang      *esphome.Select

	mu      sync.Mutex
	on      bool
	ceiling int // percent Home Assistant asked for
	autoOn  bool
	level   float64 // backlight actually applied, 0..BacklightMax, as a running average
	settled bool    // level has reached the target; settle steps it there between readings
	// lightMu makes each relight one step, from working out the level to writing it.
	lightMu sync.Mutex
	// sunriseLit is the light before an alarm having been on at the last settle tick (autobright.go).
	sunriseLit bool

	view   voice.State
	viewAt time.Time
	volume int
	volAt  time.Time

	// The dashboard page: asked for, when last touched, whether the last frame drew it, whether the
	// touchscreen was put in follow mode for it, and a finger that started at its left edge.
	dash          bool
	dashHeld      bool // put up by Home Assistant: stays until it is taken down, not dashForgotten
	dashTouched   time.Time
	dashShowing   bool
	dashFollow    bool
	dashEdge      int // edgeNone, or the edge the finger on it started at
	dashEdgeAt    image.Point
	dashAwayUntil time.Time // the idle dashboard put away, the clock up until then
	dashScroll    int       // how far down the drawn dashboard is scrolled
	dashScrollFor string    // the dashboard it is scrolled on
	dashDrag      drawnDrag // a finger moving on the drawn dashboard
	dashAdjust    dashAdjusting
	dashColor     *dashboard.LightColor // the color sheet, while it is up
	dashMedia     *mediaSheet           // the media sheet, while it is up
	sheetSent     sliderSent            // what a sheet's slider last sent, while a finger moves it
	dashHolds     bool                  // the drawn dashboard is up, and wants a finger held still reported

	poke chan struct{}

	// shots are screenshot requests, answered with a copy of the next frame drawn.
	shots chan chan *image.RGBA
	dev   *screen.Device
	r     *renderer

	// booting is the splash: from the first frame until Home Assistant is listening and at least
	// splashMin has passed.
	booting bool
	started time.Time
	logo    *splash

	// sheet is the settings sheet being shown; restartArm is the first of the two taps Restart wants.
	sheet      bool
	restartArm time.Time

	// sheetCtl is where the settings screen is: the open category, a list open over it, how far each
	// is scrolled, and the editors.
	sheetCtl

	// drawer is Cameras and Radio, in from the right over the clock: drawerTab is which,
	// drawerScroll how far its list is scrolled, and drawerPick a list of choices open over it.
	drawer       bool
	drawerTab    int
	drawerScroll int
	drawerPick   string

	// touchedAt is the last finger on the panel; nightDark is the screen having been put out by the
	// night schedule rather than by anyone.
	touchedAt time.Time
	nightDark bool

	// nightGlow is the screen turned down to a night light by the night schedule, until a touch or
	// the end of the night brings it back.
	nightGlow bool

	// nightHours and atNight are the night's settings in Home Assistant, glowLevel the night light's
	// brightness.
	nightHours, atNight *esphome.Select
	// nightStart and nightEnd are the night's custom hours, to the quarter hour.
	nightStart, nightEnd *esphome.Select
	// nightStyle is the red night clock's look.
	nightStyle *esphome.Select
	glowLevel  *esphome.Number
	// dimmestNum is the Dimmest setting in Home Assistant: how dark auto-brightness goes.
	dimmestNum *esphome.Number
	// nightMode is Night mode: Home Assistant turning the night on and off; nightShown what it last said.
	nightMode  *esphome.Switch
	nightShown bool
	nightSeen  bool // nightShown has been sent at least once
	// nightPubMu keeps publishing the Night mode switch to one caller at a time (showNightMode).
	nightPubMu sync.Mutex

	// wide is the Show 8's bigger, brighter panel, which glows harder at the same backlight.
	wide bool

	// slideshowIdleSince is when the screen last became the plain idle page (nothing else showing);
	// zero while it is not. Screensaver mode waits for this to run long enough before taking over.
	slideshowIdleSince time.Time

	// wifi is the Wi-Fi pages' state; wifiOpen shows them. wifiAt is when the status was last read.
	wifi     wifiState
	wifiOpen bool
	wifiAt   time.Time

	// quiet is a turn that was a screen command ("go home", "show the deck"): its words and reply
	// are not shown, so the screen moves at once. radioCue is when Home Assistant last named a
	// station as playing, which comes seconds before the stream does.
	quiet    bool
	radioCue time.Time

	// weatherArmed is a weather question in progress; weatherUntil is how long the forecast page
	// stays once the turn is over.
	weatherArmed bool
	weatherUntil time.Time

	// The calendar page (render_calendar.go): up until calUntil, on calMonth, as calDay's list when
	// that is set, or the coming days' agenda when calAgenda is, with calDetail's window open over it
	// when that is.
	calUntil  time.Time
	calMonth  time.Time
	calDay    time.Time
	calAgenda bool
	calDetail *hass.Event
	calScroll int // the day's list or the agenda, scrolled this many rows
	calKept   int // the agenda's scroll while one of its days is open, for Done to return to

	// The deck page (deck.go): up while deckUp, on deckPage, with deckPress the press being shown.
	deckUp    bool
	deckPage  int
	deckPress deckPress
	// deckOnScreen is whether the last frame drew the deck: a camera, the settings or a page asked
	// for by voice can be over it while it stays up, and then the touches are theirs. deckDrawn is
	// what that frame showed, so an unchanged deck isn't drawn again every second.
	deckOnScreen bool
	deckDrawn    string

	// popup is an event popped up on the screen (calendar_popup.go), until popupUntil or a tap;
	// popupNext is when the calendar is next looked at (what was shown is kept in the config).
	popup      *hass.Event
	popupUntil time.Time
	popupNext  time.Time
	popupQueue []hass.Event  // due while another was up: shown next
	pop        popupEntities // the pop-ups' settings in Home Assistant

	// The alert page (alerts.go): up until alertUntil, on alertIdx of the alerts at home, scrolled
	// alertScroll lines.
	alertUntil  time.Time
	alertIdx    int
	alertScroll int
	// radar is the rain map in place of the forecast, while the weather page is up.
	radar bool

	// away is the now-playing page put away by a swipe, and awayTrack/awayStation are what was playing
	// when it went: the next track brings it back, so a dismissal costs nothing and never strands the
	// buttons. awayPlaying is whether that track was playing at the last look, which is how a start
	// after a stop is told apart from the track simply going on.
	away        bool
	awayTrack   string
	awayStation string
	awayPlaying bool

	// strip is the music strip's setting in Home Assistant; stripKey/stripSince are the track the strip
	// is timing and when it started, stripFullUntil a tap on the strip's song bringing the full page
	// back, and showingStrip whether the last frame had it, for the touch handler.
	strip          *esphome.Select
	stripKey       string
	stripSince     time.Time
	stripFullUntil time.Time
	showingStrip   bool

	// themeSel is the theme in Home Assistant, and themeShown what it was last set to.
	themeSel   *esphome.Select
	themeShown string

	// callShown is the Call button on the clock as last drawn, and callees who the drawer's Call tab
	// listed: a tap acts on what was on the screen.
	callShown bool
	callees   []phone.Callee

	// favedKey is the track the star was last pressed for, so the star shows it was saved.
	favedKey string

	// showingPlaying and showingWord are what the last painted screen was: whether the now-playing
	// page was on it, and whether the footer was drawing the word for the music. The touch handler acts
	// on what is on the screen, rather than working the same thing out a second way and drifting from
	// it — which is how a tap out of an empty corner came to send a play/pause.
	showingPlaying bool
	showingWord    bool

	// ringPreview shows the ringing page silently until then.
	ringPreview time.Time

	// demoUntil puts placeholders where the sheet shows the owner's details (name, network,
	// address, SSH key names), for screenshots that are going to be published.
	demoUntil time.Time

	// The video page (video.go): its controls up until videoUntil; videoOnScreen the last frame having
	// drawn it, for the touch handler; videoTried the last video it showed, whose failure it says.
	// videoPainting and videoOver are the frame loop's alone: frames to paint after this frame, with
	// that of the canvas over them, and vp the frames themselves (video_shared.go).
	videoUntil    time.Time
	videoOnScreen bool
	videoTried    uint64
	videoPainting bool
	videoOver     image.Rectangle
	vp            videoPainter
	vask          askLatch
	videoLit      uint64 // the last video that lit the panel (frame)
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
	d.clockStyleSel = clockStyleSelect(d)
	setup.SetScreen(&setup.ScreenChoices{Styles: clockStyleOptions(), Current: clockStyleIndex, Choose: d.setClockStyle,
		Taps: clockTapOptions(), TapNow: clockTapIndex, ChooseTap: func(i int) { setClockTap(d.clockTap, i) }, Places: maxWorld,
		Swipe: func(on bool) { setStyleSwipe(d.styleSwipeSw, on) }})
	d.camTime = cameraTimeSelect()
	d.answerTime = answerTimeSelect()
	d.clockPos, d.dateCol = clockLayoutSelects(d.wake)
	d.turnStyle = turnStyleSelect(d.wake)
	d.clockTap = clockTapSelect()
	d.callBtn = callButtonSwitch(d.wake)
	d.styleSwipeSw = styleSwipeSwitch()
	d.weatherFx = weatherAnimationSwitch(d.wake)
	d.strip = stripSelect(d.wake)
	d.themeSel = themeSelect(d.wake)
	d.nightHours = nightHoursSelect(d)
	d.nightStart, d.nightEnd = nightEndSelect(d, true), nightEndSelect(d, false)
	d.nightStyle = nightStyleSelect(d)
	d.buildPopupEntities()
	d.atNight = atNightSelect(d)
	d.nightMode = nightModeSwitch(d)
	d.glowLevel = glowNumber(d)
	d.dimmestNum = dimmestNumber(d)
	d.lang = langSelect()
	voice.Changed.Listen(d.changed)
	media.Get().OnVolume.Listen(d.volumeMoved)
	ambient.Get().Lux.Listen(d.lux)
	touch.Get().Gestures.Listen(d.gesture)
	// A device with no address a while after boot gets the Wi-Fi page without being asked: a
	// fresh unit, or one carried to another house. The page ends the splash (frame), which would
	// otherwise wait for Home Assistant forever on a device that cannot reach it.
	go func() {
		time.Sleep(noAddressWait)
		if wifi.Available() && wifi.Current(context.Background()).Address == "" {
			d.mu.Lock()
			open := d.wifiOpen
			d.mu.Unlock()
			if !open {
				slog.Info("wifi: no address after boot, opening setup")
				d.openWifi()
			}
		}
	}()
	hastate.Get().Changed.Listen(func(u hastate.Update) {
		// The first value is the station that played last, sent when Home Assistant connects after
		// a start; only a change means a station is starting.
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
	phone.Get().Changed.Listen(func(phone.State) { d.wake() })
	security.Get().Changed.Listen(func(struct{}) { d.wake() })
	talkback.Get().Changed.Listen(func(struct{}) { d.wake() })
	alarm.Get().Changed.Listen(func(struct{}) { d.wake() })
	remind.Get().Changed.Listen(func(struct{}) { d.wake() })
	timer.Get().Changed.Listen(func(struct{}) { d.wake() })
	onMissed(d.wake)
	home.Get().Changed.Listen(func(struct{}) { d.wake() })
	d.watchRoom()
	dashboard.Get().Changed.Listen(func(struct{}) { d.wake() })
	dashboard.Get().Asked.Listen(d.dashboardAsked)
	deck.Get().Changed.Listen(d.deckChanged)
	video.Get().Changed.Listen(func(struct{}) { d.wake() })
	assistant.SetScreen(d.showPage)
	return d
}

func (d *Display) Name() string { return "screen" }

// turnShown is whether the turn's picture is the page: nothing drawn before it in render.draw has the
// screen. Only then does it need its fast frames.
func turnShown(s scene) bool {
	return s.eq != nil && s.call.Phase == phone.Idle && !s.ring.any() && !s.setupAsking && !s.bt.Pairing &&
		!s.showWifi && !s.showSheet && !s.showCamera && !s.showAlert && !s.showCalendar && !s.showRadar &&
		!s.showWeather && !s.showDash && !s.showDeck
}

// turnStyleSel is the Turn screen setting in Home Assistant.
func (d *Display) turnStyleSel() *esphome.Select { return d.turnStyle }

// clockTapSel is the Tap on the clock setting in Home Assistant.
func (d *Display) clockTapSel() *esphome.Select { return d.clockTap }

func (d *Display) Entities() []esphome.Entity {
	return []esphome.Entity{d.light, d.auto, d.clock, d.clockStyleSel, d.clockPos, d.dateCol, d.camTime, d.answerTime, d.turnStyle, d.callBtn, d.weatherFx, d.lang, d.strip, d.themeSel, d.nightHours, d.nightStart, d.nightEnd, d.nightMode, d.atNight, d.nightStyle, d.glowLevel, d.dimmestNum,
		d.pop.on, d.pop.lead, d.pop.chime, d.pop.allDay, d.clockTap, d.styleSwipeSw}
}

// Restore lights the panel the way it was left. Before the framebuffer is opened: the backlight is
// its own device.
func (d *Display) Restore(c config.Config) {
	setClock24(d.clock, c.Screen.Clock24)
	d.clockStyleSel.Set(clockStyles[clockStyleIndex()].label)
	d.camTime.Set(cameraTimes[cameraTimeIndex()].label)
	d.answerTime.Set(answerTimes[answerTimeIndex()].label)
	d.clockPos.Set(clockPositions[clockPositionIndex()].label)
	d.dateCol.Set(dateColors[dateColorIndex()].label)
	d.turnStyle.Set(turnStyles[turnStyleIndex()].label)
	d.clockTap.Set(clockTaps[clockTapIndex()].label)
	setCallButton(d.callBtn, c.Screen.CallButton)
	d.styleSwipeSw.Set(!c.Screen.NoStyleSwipe)
	setWeatherAnimation(d.weatherFx, !c.Screen.WeatherStill)
	d.strip.Set(stripOptions[stripIndex()])
	d.nightHoursChanged()
	d.atNight.Set(atNightOptions[atNightIndex()])
	d.nightStyle.Set(nightStyleOptions[nightStyleIndex()])
	d.popupSettingsChanged()
	d.glowLevel.Set(float32(d.glowSetting()))
	d.dimmestNum.Set(float32(dimmestSetting()))
	d.setAuto(c.Screen.Auto, false)
	d.apply(c.Screen.On, c.Screen.Brightness, false)
}

// command is Home Assistant changing the light. A bare "on" carries no brightness; the last one stays.
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

// apply sets the light's state: the ceiling, and whether the panel is lit at all.
func (d *Display) apply(on bool, pct int, save bool) {
	presence.Hush() // the screen's own light is about to change; the camera is not to take it for somebody
	pct = min(max(pct, 0), 100)
	d.mu.Lock()
	d.on, d.ceiling = on, pct
	// Somebody chose this, so the night no longer owns the screen: without this, a screen switched off
	// during the night was switched back on when the night ended.
	if save {
		d.nightDark = false
	}
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
		slog.Info("screen auto-brightness", "on", on)
	}
}

// relight works out the backlight from the ceiling, the room and whether the panel is on, and
// applies it. jump skips the smoothing, for a change the user just asked for.
func (d *Display) relight(jump bool) {
	if jump {
		presence.Hush() // a sudden change of the screen's light, not somebody
	}
	// One at a time from working out the level to writing it: the settle ticker, a reading and a
	// setting changed on the screen all relight, and a level worked out first must not land last.
	d.lightMu.Lock()
	defer d.lightMu.Unlock()
	d.mu.Lock()
	target := 0.0
	if d.on {
		lux, _, haveLux := ambient.Get().Current()
		target = dayBacklight(d.ceiling, d.autoOn && haveLux, lux, dimmest())
		if d.nightGlow {
			target = float64(d.glowBacklight()) // relight holds mu
		} else if (phone.Get().Busy() || video.Get().State().Active()) && nightNow(time.Now()) {
			target = math.Min(target, screen.BacklightMax/2) // a call at night: enough to see who it is
		}
	}
	// The light before an alarm takes the backlight over while it runs: it starts under anything the
	// room would otherwise ask for and ends at the screen's own brightness.
	if p := sunriseProgress(time.Now()); p > 0 && d.on {
		target = float64(d.ceiling) * screen.BacklightMax / 100 * sunriseLevel(p)
	}
	if jump || d.level == 0 {
		d.level = target
	} else {
		d.level += (target - d.level) * autoSmooth
	}
	// Close enough is the target itself: smoothing alone would stop up to half a step short, which
	// rounds a step under it (25% at the default dimmest settled on 7, not 8).
	if math.Abs(target-d.level) < 0.5 {
		d.level, d.settled = target, true
	} else {
		d.settled = false
	}
	level := int(math.Round(d.level))
	d.mu.Unlock()
	// A long press is the way up from the night light (gesture), and on the drawn dashboard what opens
	// a light's colors; this screen reports no holds otherwise. Every change to the night light comes
	// through here, and the dashboard's own change (dashScene) sets it too.
	d.applyHolds()

	if err := screen.SetBacklight(level); err != nil {
		slog.Warn("setting the backlight failed", "err", err)
	}
}

// dayBacklight is the backlight for a screen that is on, out of screen.BacklightMax: the Brightness
// setting (ceiling, in percent), scaled to the room when auto-brightness has a reading, never under
// the floor.
func dayBacklight(ceiling int, auto bool, lux, dark float64) float64 {
	target := float64(ceiling) * screen.BacklightMax / 100
	if auto {
		target *= allowed(lux, dark)
	}
	return math.Max(target, floor)
}

// allowed is the fraction of the ceiling a room this bright gets, dark the fraction a dark room gets.
// The curve starts at darkLux: these sensors read a dark room as 0, 1 or 2 depending on the unit, and a
// curve from 0 put a room at 1 lux a tenth of the ceiling above a room at 0, which is where a dark
// bedroom's panel stayed too bright (#78). Starting at 2 also keeps a reading that wavers between 1
// and 2 from stepping the panel up and down.
func allowed(lux, dark float64) float64 {
	f := dark + (1-dark)*math.Log10(math.Max(lux, darkLux)/darkLux)/math.Log10(brightLux/darkLux)
	return math.Min(math.Max(f, dark), 1)
}

// dimmest is the fraction of the ceiling auto-brightness allows a dark room.
func dimmest() float64 {
	return float64(dimmestSetting()) / 100
}

// dimmestSetting is the Dimmest setting in percent: the one set, kept in range however it got into
// the file, or the default.
func dimmestSetting() int {
	if v := config.Get().Screen.AutoDimmest; v > 0 {
		return min(v, 50)
	}
	return defaultDimmest
}

// dimmestSteps are the Dimmest row's steps on the screen: fine at the bottom, where a percent is the
// difference in a dark room.
var dimmestSteps = [...]int{1, 2, 3, 4, 5, 6, 8, 10, 12, 15, 20, 25, 30, 40, 50}

// stepDimmest moves the Dimmest setting one step on the screen and shows it at once.
func (d *Display) stepDimmest(by int) {
	d.setDimmest(nextDimmest(dimmestSetting(), by))
}

// nextDimmest is the step by steps from cur, which Home Assistant may have set between two steps.
func nextDimmest(cur, by int) int {
	i := 0
	for i < len(dimmestSteps)-1 && dimmestSteps[i] < cur {
		i++
	}
	if dimmestSteps[i] != cur {
		// Between two steps, as Home Assistant can set it: i is the higher one, which is a step up;
		// a step down is the lower one.
		if by < 0 {
			i--
		}
		by = 0
	}
	i = min(max(i+by, 0), len(dimmestSteps)-1)
	return dimmestSteps[i]
}

// setDimmest saves the Dimmest setting, tells Home Assistant and relights at once, so it can be set
// while looking at the screen in the dark.
func (d *Display) setDimmest(pct int) {
	if err := config.Set().Screen().AutoDimmest(pct); err != nil {
		slog.Error("saving auto-brightness dimmest failed", "err", err)
		return
	}
	if d.dimmestNum != nil {
		d.dimmestNum.Set(float32(dimmestSetting()))
	}
	slog.Info("auto-brightness dimmest", "percent", dimmestSetting())
	d.relight(true)
}

func dimmestNumber(d *Display) *esphome.Number {
	n := &esphome.Number{
		Base: esphome.Base{
			ObjectID: "screen_auto_brightness_dimmest",
			Name:     "Auto-brightness dimmest",
			Icon:     "mdi:brightness-4",
			Category: esphome.CategoryConfig,
		},
		Min: 1, Max: 50, Step: 1, Unit: "%",
		Mode: esphome.NumberBox,
	}
	// Rounded, and at least 1: a value under 1 would otherwise be 0, which means the default.
	n.OnCommand = func(v float32) { d.setDimmest(max(int(math.Round(float64(v))), 1)) }
	return n
}

// lux is a reading from the room, on the sensor's goroutine. Readings come only when the light
// changes, so this takes the first step and settle the rest.
func (d *Display) lux(float64) {
	d.mu.Lock()
	auto, on := d.autoOn, d.on
	d.mu.Unlock()
	if auto && on {
		d.relight(false)
	}
}

// changed is the conversation moving on. It runs on the conversation's goroutine, so it only
// records and wakes the loop.
func (d *Display) changed(s voice.State) {
	d.mu.Lock()
	newHeard := s.Heard != "" && s.Heard != d.view.Heard
	if s.Phase == "listening" && d.view.Phase != "listening" {
		d.quiet = false
	}
	d.view = s
	d.viewAt = time.Now()
	// A question about the weather brings the forecast page up once the answer is done, for a
	// while, and then the screen goes back to whatever it was showing.
	// Not when the device answers directly: its assistant knows where a question was about and puts
	// the page up itself (showPage), and a forecast for another town is not this one's.
	if newHeard && aboutWeather(s.Heard) && !config.Get().Brain.Direct() {
		d.weatherArmed = true
		d.radar = aboutRadar(s.Heard)
	}
	// "Show the front door": the camera goes up at once, while the assistant answers. Once per
	// sentence: the state repeats the transcript on every phase change.
	if newHeard {
		if entity := home.Get().MatchCamera(s.Heard); entity != "" {
			d.quiet = true
			go home.Get().ShowCamera(entity, cameraVoiceShow)
		}
		// "Go home": whatever page is up comes down, back to the clock, and music stops rather than
		// holding the now-playing page.
		if aboutGoingHome(s.Heard) {
			d.weatherArmed, d.weatherUntil = false, time.Time{}
			d.calUntil, d.calDetail = time.Time{}, nil
			d.closeAlert()
			d.sheet, d.quiet = false, true
			d.dash, d.deckUp = false, false
			go home.Get().HideCamera()
			go d.endMusic()
			slog.Info("screen: home by voice")
		}
	}
	if s.Phase == "idle" && d.weatherArmed {
		d.weatherArmed = false
		d.weatherUntil = time.Now().Add(weatherShow)
	}
	d.mu.Unlock()
	d.wake()
}

// showPage is the voice assistant putting a page up (feature/assistant): the forecast or the rain map
// once the answer has been said, as a question about the weather does, or the calendar now.
func (d *Display) showPage(page string) bool {
	switch page {
	case "weather", "radar":
		d.mu.Lock()
		d.weatherArmed, d.radar = true, page == "radar"
		d.mu.Unlock()
		d.wake()
		return true
	case "calendar":
		return d.OpenCalendar()
	}
	return false
}

// volumeMoved is the level changing on purpose; the screen shows it for a moment.
func (d *Display) volumeMoved(step int) {
	d.mu.Lock()
	d.volume, d.volAt = step, time.Now()
	d.mu.Unlock()
	d.wake()
}

// gesture is a finger on the panel. A dark screen only lights up; otherwise a tap is the action
// button and a vertical swipe the volume.
func (d *Display) gesture(g touch.Gesture) {
	d.mu.Lock()
	on := d.on
	d.mu.Unlock()
	slog.Info("touch", "gesture", g.String())
	d.mu.Lock()
	d.touchedAt = time.Now()
	d.mu.Unlock()

	if !on {
		if g.Kind == touch.Tap && d.ringing(time.Now()).any() {
			// A ring on a panel the night left dark: the tap stops it, and the panel stays dark.
			d.stopRing()
			return
		}
		if g.Kind == touch.Tap {
			d.apply(true, d.ceilingOrDefault(), true)
		}
		return
	}
	// A night light stays a night light when it is touched: bright light is the last thing somebody
	// asleep or just waking wants, and a hand in the dark is more likely a knock than a request. A long
	// press is the way to the full screen, as the first touch used to be; nothing else on a night
	// light does anything, since nobody can see what they are pressing on a screen this dim.
	d.mu.Lock()
	_, _, _, asking := video.Get().Asking()
	// Not on the video page either: its controls work at night as by day (the night light's rule is
	// the clock's, a hand in the dark more likely a knock than a request).
	glowing := d.nightGlow && !d.wifiOpen && !setup.Get().Waiting() && !asking && !d.videoOnScreen
	d.mu.Unlock()
	if glowing {
		if g.Kind == touch.Tap && d.ringing(time.Now()).any() {
			// An alarm or a timer rings over the night clock; a tap anywhere stops it, as the ringing
			// page's own button would.
			d.stopRing()
			d.wake()
			return
		}
		if g.Kind == touch.Hold {
			d.mu.Lock()
			d.nightGlow = false
			d.mu.Unlock()
			slog.Info("screen: night light lifted by a long press")
			d.relight(true)
			d.wake()
		}
		return
	}
	// A call: its page takes every tap.
	if st := phone.Get().State(); st.Phase != phone.Idle {
		if g.Kind == touch.Tap {
			// Unless something is ringing behind it. The call keeps its buttons; the rest of the
			// page stops the ring. Taps outside the buttons did nothing on this page before, so
			// answering and hanging up are untouched — and a timer that finishes mid-call used to
			// have no way out at all, since this page took every tap and the stop word was off.
			if rs := d.ringing(time.Now()); rs.any() && d.r != nil && !d.r.actionDecided(g.Y) {
				d.stopRing()
			} else {
				d.callTap(g.X, g.Y, st)
			}
		}
		d.wake()
		return
	}

	// A timer or an alarm ringing: its page takes every tap.
	if st := d.ringing(time.Now()); st.any() {
		if g.Kind == touch.Tap {
			d.ringTap(g.X, g.Y, st)
		}
		d.wake()
		return
	}

	// The PIN pad of the settings lock takes every tap while it is up, under a call and a ring.
	if d.pinGesture(g) {
		return
	}

	// A browser asking to be let in: its page takes every tap, and only the answers decide.
	if setup.Get().Waiting() {
		if g.Kind == touch.Tap {
			if d.r != nil {
				if allow, answered := d.r.askTap(g.X, g.Y); answered {
					answerSetup(allow)
				}
			}
		}
		d.wake()
		return
	}

	// A DLNA video asking to be shown, the same way; and then the video page, which takes every finger.
	if _, _, _, asking := video.Get().Asking(); asking {
		d.videoAskGesture(g)
		d.wake()
		return
	}
	if d.videoUp() {
		d.videoGesture(g)
		return
	}

	// The microphone open for an announcement: the strip along the bottom is the way to end it, a tap
	// sending what was said and a hold throwing it away. Only the strip — the rest of the screen is
	// the clock and the music, which an announcement deliberately does not take over.
	if announce.Get().Recording() && d.r != nil && onAnnounceStrip(g.X, g.Y, d.r.w, d.r.h) {
		switch g.Kind {
		case touch.Tap:
			go announce.Get().Finish()
		case touch.Hold:
			go announce.Get().Cancel()
		}
		d.wake()
		return
	}

	// One that arrived: a tap on the strip puts it away, the same as on the round face. It does not
	// have to be put away here — the clock and the music are carrying on behind it and it goes by
	// itself — but having to wait out something already heard is the same annoyance on any screen.
	if _, showing := announce.Get().Showing(); showing && g.Kind == touch.Tap &&
		d.r != nil && onAnnounceStrip(g.X, g.Y, d.r.w, d.r.h) {
		go announce.Get().Dismiss()
		d.wake()
		return
	}

	// A reminder: a tap on its card puts it away, here and on every device it went off on. Only the
	// card, as with the strip, so a finger meant for the music behind it still reaches the music.
	if g.Kind == touch.Tap && d.r != nil && d.r.popupTapped(image.Pt(g.X, g.Y)) {
		d.dismissPopup()
		d.wake()
		return
	}

	if _, showing := remind.Get().Showing(); showing && g.Kind == touch.Tap &&
		d.r != nil && image.Pt(g.X, g.Y).In(d.r.reminderBox()) {
		go remind.Get().Stop()
		d.wake()
		return
	}

	// The first-run card: any tap puts it away for good.
	if !config.Get().Screen.Welcomed {
		if g.Kind == touch.Tap {
			if err := config.Set().Screen().Welcomed(true); err != nil {
				slog.Warn("saving the welcome failed", "err", err)
			}
			slog.Info("first-run card put away")
			d.wake()
		}
		return
	}

	// The pairing page: a tap on a row pairs or connects it, the bar at the bottom ends the page.
	// A swipe from the right opens it from the clock.
	bt := btaudio.Get()
	if bt.Pairing() {
		switch g.Kind {
		case touch.Tap:
			if d.r == nil {
				return
			}
			st := bt.State()
			switch row := d.r.btRowAt(g.Y); {
			case row == btRows:
				bt.SetPairing(false)
			case row >= 0 && row < len(st.Devices) && !st.Devices[row].Busy:
				bt.Choose(st.Devices[row].Address)
			}
		case touch.SwipeUp:
			media.Get().Adjust(+1)
		case touch.SwipeDown:
			media.Get().Adjust(-1)
		case touch.SwipeRight:
			bt.SetPairing(false)
		}
		d.wake()
		return
	}

	// The deck: every finger is its while it is up.
	if d.deckShowing() {
		d.deckGesture(g)
		d.wake()
		return
	}

	// The dashboard: every finger is its, including the one that takes it away.
	d.mu.Lock()
	dashUp := d.dashShowing
	d.mu.Unlock()
	if dashUp {
		d.dashGesture(g)
		d.wake()
		return
	}

	// A live camera: a tap takes it down, unless it lands on the sound's control — that silences what
	// the camera is saying and leaves the view up, which is the whole use of it at a doorbell — or on
	// Talk, which starts or ends talking through the camera.
	if v, up := home.Get().Camera(); up {
		if g.Kind == touch.Tap {
			if d.r != nil && d.r.cameraTalkTapped(image.Pt(g.X, g.Y)) {
				go talkback.Get().Toggle(v.Entity)
			} else if d.r != nil && d.r.cameraSoundTapped(image.Pt(g.X, g.Y)) {
				// Silence it, or ask for it again: the control is a toggle, and the view stays either way.
				home.Get().ToggleCameraSound()
			} else {
				home.Get().HideCamera()
			}
		}
		d.wake()
		return
	}

	// The Wi-Fi pages take every tap while they are up.
	d.mu.Lock()
	wifiOpen := d.wifiOpen
	d.mu.Unlock()
	if wifiOpen {
		if g.Kind == touch.Tap && d.r != nil {
			d.wifiTap(g.X, g.Y)
		}
		d.wake()
		return
	}

	// The drawer, Cameras and Radio: while it is in, every finger is its.
	d.mu.Lock()
	drawerIn := d.drawer
	d.mu.Unlock()
	if drawerIn {
		d.drawerGesture(g)
		d.wake()
		return
	}

	// The settings screen: taps land on what it drew, vertical swipes scroll it.
	d.mu.Lock()
	sheet := d.sheet
	d.mu.Unlock()
	if sheet {
		// Vertical swipes do nothing here: the swipe that opened the sheet keeps reporting notches
		// until the finger lifts, and those must not turn into volume steps. The Volume row has
		// buttons instead.
		switch {
		case d.r == nil:
		case g.Kind == touch.Tap:
			d.mu.Lock()
			d.openedBy = image.Point{-1, -1}
			d.mu.Unlock()
			d.nextTap(g.X, g.Y)
		case g.Kind == touch.SwipeUp || g.Kind == touch.SwipeDown:
			d.sheetSwipe(g)
		}
		d.wake()
		return
	}

	// The alert page takes its own taps and swipes while it is up; it is drawn over the calendar, so it
	// is asked first.
	if d.alertUp() {
		d.alertGesture(g)
		d.wake()
		return
	}
	if d.calendarUp() {
		d.calendarGesture(g)
		d.wake()
		return
	}
	switch g.Kind {
	case touch.Tap:
		d.mu.Lock()
		weatherUp := time.Now().Before(d.weatherUntil)
		idle := d.view.Phase == "idle"
		d.mu.Unlock()
		// A finished turn's words: a tap puts them away. It used to start another turn, which is not
		// what a hand put on an answer to clear it means.
		if !weatherUp && d.answerUp(time.Now()) {
			d.clearAnswer()
			return
		}
		// The alert badge on the clock opens the alert, and a pill over the rain map opens its alert.
		// Both sit in the top band too.
		if idle && !weatherUp && d.r != nil && d.r.badgeTapped(image.Pt(g.X, g.Y)) && d.OpenAlert(0) {
			return
		}
		d.mu.Lock()
		onRadar := d.radar
		d.mu.Unlock()
		if weatherUp && onRadar && d.r != nil {
			if i := d.r.pillTapped(image.Pt(g.X, g.Y)); i >= 0 && d.OpenAlert(i) {
				return
			}
		}
		// The Call button and the music strip are drawn over the clock, so a tap on them is theirs even
		// where a clock style's date or weather lies under them.
		overButtons := false
		if d.r != nil {
			d.mu.Lock()
			call, strip := d.callShown, d.showingStrip
			d.mu.Unlock()
			at := image.Pt(g.X, g.Y)
			overButtons = call && at.In(d.r.callButtonRect().Inset(-d.r.s(12))) || strip && at.In(d.r.stripRect())
		}
		// The weather on the home screen opens the forecast, the page a weather question brings up. It
		// sits in the top band, so it is looked for before that band's rule below.
		if idle && !weatherUp && !overButtons && d.r != nil && d.r.weatherTapped(image.Pt(g.X, g.Y)) {
			slog.Info("screen: forecast by touch")
			d.ShowWeather(false)
			return
		}
		// The date under the clock opens the calendar, once this device shows one.
		if idle && !weatherUp && !overButtons && d.r != nil && d.r.dateTapped(image.Pt(g.X, g.Y)) && d.OpenCalendar() {
			slog.Info("screen: calendar by touch")
			return
		}
		// The Dashboard's next events open the agenda: the coming days' events as one list.
		if idle && !weatherUp && !overButtons && d.r != nil && d.r.nextTapped(image.Pt(g.X, g.Y)) && d.OpenAgenda() {
			slog.Info("screen: agenda by touch")
			return
		}
		// A short swipe from the top edge that never made a notch arrives as a tap; it must not
		// start a turn. The top band is the sheet's, taps there do nothing.
		if g.Y < topEdge {
			return
		}
		if weatherUp {
			// The weather page: its button turns between the forecast and the rain map, and keeps the
			// page up a while longer; a tap anywhere else puts it away.
			d.mu.Lock()
			if d.r != nil && g.X >= 0 && image.Pt(g.X, g.Y).In(d.r.weatherButton()) {
				d.radar = !d.radar
				d.weatherUntil = time.Now().Add(weatherShow)
			} else {
				d.weatherUntil, d.radar = time.Time{}, false
			}
			d.mu.Unlock()
			d.wake()
			return
		}
		// The footer's word for the music is the way back to the page: it shows what is playing, so a
		// tap made to look at the music never leaves you looking at the clock, and it answers whether
		// the track is playing or paused. While it was paused only, a page swiped away during a song had
		// no way back at all. It brings the page back and does nothing else: playing or pausing is the
		// page's own button, and a tap made to look at the song should not stop it.
		d.mu.Lock()
		showing, away, inStrip, word := d.showingPlaying, d.away, d.showingStrip, d.showingWord
		d.mu.Unlock()
		// Only while the word is really drawn, and only when the page is really away: this is the way back
		// to it, so taking a tap out of the bottom-right corner of the clock is worth it only when there
		// is both something to come back to and something drawn there. Nothing playing leaves the corner
		// free, which is where a tap meant for a voice turn lands. Not with the strip up, either: the
		// word is not drawn then, and its corner is the strip's.
		if d.r != nil && idle && word && !showing && away && !inStrip &&
			image.Pt(g.X, g.Y).In(d.r.playingButton()) {
			d.mu.Lock()
			d.away, d.awayTrack, d.awayStation, d.awayPlaying = false, "", "", false
			d.mu.Unlock()
			d.wake()
			return
		}
		// The Call button, when it is on the clock: the list of devices and contacts to call.
		d.mu.Lock()
		callShown := d.callShown
		d.mu.Unlock()
		if idle && callShown && d.r != nil && image.Pt(g.X, g.Y).In(d.r.callButtonRect().Inset(-d.r.s(12))) {
			d.openDrawer(drawerCall)
			return
		}
		d.mu.Lock()
		strip := d.showingStrip
		d.mu.Unlock()
		if idle && strip && d.r != nil && image.Pt(g.X, g.Y).In(d.r.stripRect()) {
			// The strip: its buttons do what they say, the X ends the music, and the song brings the
			// full page back for a while. Anywhere else on the strip is nothing, so a thumb that
			// misses a button does not start a turn under it.
			back, play, next, closeX := d.r.stripButtons()
			at := image.Pt(g.X, g.Y)
			switch {
			case at.In(closeX):
				go d.endMusic()
			case at.In(back):
				transport(media.TransportPrevious)
			case at.In(play):
				transport(media.TransportToggle)
			case at.In(next):
				transport(media.TransportNext)
			case at.In(d.r.stripSong()):
				d.mu.Lock()
				d.stripFullUntil = time.Now().Add(stripFull)
				d.away, d.awayTrack, d.awayStation, d.awayPlaying = false, "", "", false
				d.mu.Unlock()
				d.wake()
			}
			return
		}
		if idle && showing {
			// The now-playing screen: each button does what it says, and a tap anywhere else is
			// play/pause, because that is what a hand put on a screen like this means.
			if d.r != nil {
				back, _, next := d.r.transportButtons()
				at := image.Pt(g.X, g.Y)
				switch {
				case at.In(d.r.doneButton()):
					go d.endMusic()
				case at.In(d.r.favButton()):
					go d.favorite()
				case at.In(back):
					transport(media.TransportPrevious)
				case at.In(next):
					transport(media.TransportNext)
				default:
					transport(media.TransportToggle)
				}
				return
			}
			transport(media.TransportToggle)
			return
		}
		// The clock itself: a voice turn, unless the Tap on the clock setting says the dashboard, or
		// nothing. A turn under way still takes the tap either way, as the way to stop or answer it,
		// and with no dashboard set up a tap meant for one starts a turn rather than doing nothing.
		if idle {
			switch config.Get().Screen.ClockTap {
			case "nothing":
				return
			case "dashboard":
				if d.openDashboard() {
					slog.Info("screen: dashboard by a tap on the clock")
					return
				}
			case "deck":
				if d.openDeck() {
					return
				}
			}
		}
		voice.Get().Action()
	case touch.SwipeLeft:
		// From the right edge it brings the drawer in, on the tab it was last on; across the clock it is
		// the next clock style.
		if d.r != nil && g.X >= d.r.w-d.r.drawerEdge() {
			d.mu.Lock()
			tab := d.drawerTab
			d.mu.Unlock()
			d.openDrawer(tab)
			return
		}
		if d.styleSwipe() {
			d.stepClockStyle(+1)
		}
	case touch.SwipeUp:
		// From the bottom edge of the clock it is the deck, once there is one; anywhere else, and on the
		// pages the clock's swipes also reach (now playing, the weather), it is the volume.
		if d.fromBottomEdge(g.Y) && d.onClock() && d.openDeck() {
			return
		}
		media.Get().Adjust(+1)
	case touch.SwipeDown:
		// From the top edge it is the sheet; anywhere else it is the volume.
		if g.Y < topEdge {
			d.mu.Lock()
			d.openedBy = image.Pt(g.X, g.Y)
			d.mu.Unlock()
			d.showSheet(true)
			return
		}
		media.Get().Adjust(-1)
	case touch.SwipeRight:
		// From the left edge it brings the dashboard up, the drawer's gesture mirrored.
		if d.r != nil && g.X < d.r.drawerEdge() && d.openDashboard() {
			return
		}
		// Across the clock it is the clock style before.
		if d.styleSwipe() {
			d.stepClockStyle(-1)
			return
		}
		// Right puts the now-playing page away until the track changes. It is the one gesture left on that
		// page that cannot be taken for play or pause, which a tap there has to be. It is not the drawer's
		// way out: the drawer has closed on this swipe since long before there was a page to put away,
		// because while it is in every finger belongs to drawerGesture.
		d.mu.Lock()
		sheet, showing, idle := d.sheet, d.showingPlaying, d.view.Phase == "idle"
		d.mu.Unlock()
		_, camera := home.Get().Camera()
		if sheet || camera || !idle || !showing {
			return
		}
		rd := home.Get().Radio()
		// Whether the music is playing, not whether the page says so: a carried stream that has stopped
		// still has a page, and a dismissal made on one of those is over too.
		playing, _ := media.Get().ScreenState()
		d.mu.Lock()
		d.away, d.awayTrack, d.awayStation, d.awayPlaying = true, rd.Title, rd.Now, playing
		d.mu.Unlock()
		d.wake()
	}
}

// styleSwipe is whether a swipe left or right lands on the clock itself, where it turns the clock
// style: idle, the clock page on the screen (not now playing, the weather, the screensaver or the
// light before an alarm), no sheet, camera view or finished turn's words over it.
func (d *Display) styleSwipe() bool {
	d.mu.Lock()
	sheet, idle, up := d.sheet, d.view.Phase == "idle", d.clockUp
	d.mu.Unlock()
	_, camera := home.Get().Camera()
	return styleSwipeOn() && idle && up && !sheet && !camera && d.onClock() && !d.answerUp(time.Now())
}

// favorite saves what is playing to favorites, and marks the star once it is saved.
func (d *Display) favorite() {
	rd := home.Get().Radio()
	if err := home.Get().FavoriteNow(); err != nil {
		slog.Warn("saving to favorites failed", "err", err)
		return
	}
	d.mu.Lock()
	d.favedKey = rd.Title + "\x00" + rd.Now
	d.mu.Unlock()
	d.wake()
}

// endMusic is "go home" and the now-playing screen's Done: the music stops, a held track is let go,
// and the page goes back to the clock.
func (d *Display) endMusic() {
	// Nothing playing is nothing to ask: "go home" on an idle clock with a remote merely holding the
	// speaker sent a stop to Music Assistant's queue when it was the last remote. What a stop means for
	// whoever has the music is home's to decide: a stream this device did not start is asked to stop, and
	// its own is ended rather than paused.
	if home.Following() {
		// Another room's player: Done puts it away until its next track, and leaves it playing.
		home.Get().DismissFollowed()
	} else if playing, paused := media.Get().ScreenState(); playing || paused {
		home.Get().Stop()
	}
	// And the page goes, as a swipe puts it away: a track somebody else is holding paused is still a
	// page. It comes back for the next track, and after a stop the next start counts as one, so the
	// music just ended is not playing however the page went.
	rd := home.Get().Radio()
	d.mu.Lock()
	d.away, d.awayTrack, d.awayStation, d.awayPlaying = true, rd.Title, rd.Now, false
	d.mu.Unlock()
	d.wake()
}

// putAway is whether the now-playing page is staying out of the way. A swipe puts it away and it stays
// away only for the track it was put away on: a dismissal is temporary without needing a timer, and the
// page comes back for the next song. Nothing playing at all clears it, so what comes next is not hidden
// by a gesture made about something else.
//
// A start after a stop is a next song as much as a different one is, which is why awayPlaying is
// remembered: the quiet while that track is stopped keeps the page away, and playing it again brings the
// page back. Reading the track's name alone made restarting the station somebody had just put away look
// like the same track going on, so the one deliberate press that should never leave you looking at the
// clock was the one that did.
func (d *Display) putAway(rd home.Radio, wanted, playing bool) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !wanted || !d.away || rd.Title != d.awayTrack || rd.Now != d.awayStation {
		d.away, d.awayTrack, d.awayStation, d.awayPlaying = false, "", "", false
		return false
	}
	switch {
	case playing && !d.awayPlaying:
		d.away, d.awayTrack, d.awayStation = false, "", ""
		return false
	case !playing:
		d.awayPlaying = false
	}
	return true
}

// nowPlaying is whether the idle screen should be what is playing rather than the clock: something
// on the speaker, and a name to put on the page.
//
// A stream this player did not start says so itself - Music Assistant over Sendspin - and it is the
// reason the page exists at all, so it is asked first: its audio never passes through this player's own
// stream, which is what Playing() reports on.
func (d *Display) nowPlaying() bool {
	if media.Get().ExternalPlaying() || home.Following() {
		return true
	}
	if _, _, _, ok := media.Get().Held(); ok {
		return true
	}
	if !home.Get().Radio().Configured {
		return false
	}
	playing, paused := media.Get().Playing()
	if playing || paused {
		return true
	}
	// Home Assistant named a station a moment ago: the stream is on its way, show the page now.
	d.mu.Lock()
	cued := time.Since(d.radioCue) < radioCueFor
	d.mu.Unlock()
	return cued
}

// radioCueFor is how long the now-playing page is shown on Home Assistant's word alone, before
// the stream itself has to be playing to keep it.
const radioCueFor = 20 * time.Second

func (d *Display) showSheet(on bool) {
	if on && security.Locked() {
		// The settings lock: the PIN pad first, and the sheet once the PIN is right.
		openPIN(func() { d.setSheet(true) })
		d.wake()
		return
	}
	d.setSheet(on)
}

// setSheet puts the sheet up or takes it down, past the lock: showSheet asks for the PIN first.
func (d *Display) setSheet(on bool) {
	d.mu.Lock()
	d.sheet = on
	d.restartArm, d.picker, d.cardScroll, d.pickScroll, d.colors = time.Time{}, "", 0, 0, false
	d.mu.Unlock()
	slog.Info("settings sheet", "open", on)
	d.wake()
}

// ShowWeather puts the weather page up, the forecast or the rain map, as a question would.
func (d *Display) ShowWeather(radar bool) {
	d.mu.Lock()
	d.weatherUntil, d.radar, d.sheet = time.Now().Add(weatherShow), radar, false
	d.calUntil, d.calDetail = time.Time{}, nil // the forecast comes up over the calendar, not under it
	d.closeAlert()                             // and over an alert
	d.mu.Unlock()
	d.wake()
}

func (d *Display) ceilingOrDefault() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.ceiling == 0 {
		return config.DefaultScreenBrightness
	}
	return d.ceiling
}

// night puts the screen out inside the night window once nothing has happened for a while, and
// brings it back when the window ends. It reports true when it changed the screen, so the frame
// is redrawn from the new state.
func (d *Display) night(now time.Time, on bool, view voice.State) bool {
	in := nightNow(now)
	d.showNightMode(in)
	d.mu.Lock()
	dark, touched, viewAt, wifiUp := d.nightDark, d.touchedAt, d.viewAt, d.wifiOpen
	d.mu.Unlock()
	if !in {
		d.mu.Lock()
		glowing := d.nightGlow
		d.nightGlow = false
		d.mu.Unlock()
		if glowing {
			slog.Info("screen: night over, night light off")
			d.relight(true)
		}
	}
	switch {
	case in && on:
		// Only a call lifts the night light, and only to half brightness (relight): somebody has to
		// see who it is. The light before an alarm lifts it too, since that light is what was asked
		// for. Everything else stays at the night light's level: an alarm or a timer rings over the
		// night clock, and the words of an answer, an announcement or a reminder are shown dim, the
		// clock coming back after, rather than the main screen at full brightness for somebody asleep
		// or just waking.
		// The Wi-Fi page too: it is the one page that fixes a device with no network, and a clock
		// that has never been set (no network, no time) can put a new device in its night.
		// And a browser asking to be let in to the setup page: its Allow has to be seen and pressed,
		// and whoever is asking is standing at the device.
		// A talk through a camera is a conversation at the door: the screen it needs stays lit.
		// And a video: somebody asked for it to be watched.
		lift := phone.Get().Busy() || talkback.Get().Busy() || sunriseProgress(now) > 0 || wifiUp || setup.Get().Waiting()
		// A video playing keeps the screen as it is: lit at the night light's level if it came on at
		// night (frame), not put out under it.
		active := view.Phase != "idle" || now.Sub(touched) < nightIdle || now.Sub(viewAt) < nightIdle ||
			video.Get().State().Active()
		// Something playing is not somebody using the screen. At night it is rain or music to sleep
		// to, and it kept a guest room's screen at full brightness all night.
		if !lift && active {
			// Left as it is: a night light stays one, and a screen a long press brought up stays up
			// until the room has been quiet a while.
			return false
		}
		if lift {
			// A call at night is seen at half brightness (relight), and the light before an alarm at
			// its own; neither at the night light's.
			d.mu.Lock()
			glowing := d.nightGlow
			d.nightGlow = false
			d.mu.Unlock()
			if glowing {
				d.relight(true)
				return true
			}
			return false
		}
		if config.Get().Screen.NightLight {
			d.mu.Lock()
			already := d.nightGlow
			d.nightGlow = true
			d.mu.Unlock()
			if already {
				return false
			}
			slog.Info("screen: night, down to a night light")
			d.relight(true)
			return true
		}
		slog.Info("screen: night, going dark")
		d.mu.Lock()
		// Dark, not a night light: a glow left set (a video lit the screen at the night light's level)
		// would have the next tap light it at that level, with only a long press doing anything.
		d.nightDark, d.nightGlow = true, false
		d.mu.Unlock()
		d.apply(false, d.ceilingOrDefault(), false)
		return true
	case !in && dark && !on:
		slog.Info("screen: night over, back on")
		d.mu.Lock()
		d.nightDark = false
		d.mu.Unlock()
		d.apply(true, d.ceilingOrDefault(), false)
		return true
	case !in && dark:
		d.mu.Lock()
		d.nightDark = false
		d.mu.Unlock()
	}
	return false
}

// nightIdle is how long the panel stays lit after a finger or a turn during the night.
const nightIdle = 90 * time.Second

// inNight is whether now falls in the window, which may cross midnight.

// ---- Wi-Fi pages ----

// OpenWifi shows the Wi-Fi pages; demo puts the keyboard up for a made-up network, for a look at
// the layout from afar. CloseWifi takes them down.
func (d *Display) OpenWifi(demo bool) {
	d.openWifi()
	if demo {
		d.mu.Lock()
		d.wifi.pick = &wifi.Network{SSID: "Example Network", Secured: true}
		d.wifi.text = "correct horse"
		d.mu.Unlock()
		d.wake()
	}
}

func (d *Display) CloseWifi() { d.closeWifi() }

// openWifi shows the network list and starts a scan.
func (d *Display) openWifi() {
	d.mu.Lock()
	d.wifiOpen = true
	d.wifi = wifiState{scanning: true}
	glowing, dark := d.nightGlow, d.nightDark
	d.nightGlow, d.nightDark = false, false
	d.mu.Unlock()
	// Over the night, as a call is: the page has to be seen and pressed.
	if dark {
		d.apply(true, d.ceilingOrDefault(), false)
	} else if glowing {
		d.relight(true)
	}
	wifi.SettingUp(true)
	slog.Info("wifi page", "open", true)
	go d.refreshWifi()
	go d.wifiScan()
	d.wake()
}

func (d *Display) closeWifi() {
	d.mu.Lock()
	d.wifiOpen = false
	d.wifi = wifiState{}
	d.mu.Unlock()
	wifi.SettingUp(false)
	slog.Info("wifi page", "open", false)
	d.wake()
}

func (d *Display) refreshWifi() {
	st := wifi.Current(context.Background())
	d.mu.Lock()
	d.wifi.status = st
	d.mu.Unlock()
	d.wake()
}

func (d *Display) wifiScan() {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	nets, err := wifi.Scan(ctx)
	d.mu.Lock()
	d.wifi.scanning = false
	if err != nil {
		d.wifi.err = "scan failed: " + err.Error()
	} else {
		d.wifi.nets, d.wifi.err = nets, ""
	}
	d.mu.Unlock()
	d.wake()
}

// wifiTap is a finger on the Wi-Fi pages.
func (d *Display) wifiTap(x, y int) {
	d.mu.Lock()
	w := d.wifi
	d.mu.Unlock()
	if w.pick != nil {
		d.wifiKey(d.r.keyAt(x, y, w.symbols))
		return
	}
	h := d.r.wifiListHit(x, y)
	switch {
	case h.done:
		d.closeWifi()
	case h.rescan:
		d.mu.Lock()
		d.wifi.scanning, d.wifi.err = true, ""
		d.mu.Unlock()
		go d.wifiScan()
	case h.row >= 0:
		start, end, more := pageWith(len(w.nets), w.page, wifiRows)
		if more && h.row == wifiRows-1 {
			d.mu.Lock()
			d.wifi.page++
			d.mu.Unlock()
			return
		}
		if start+h.row >= end {
			return
		}
		n := w.nets[start+h.row]
		if !n.Secured {
			d.wifiJoin(n, "")
			return
		}
		d.mu.Lock()
		d.wifi.pick, d.wifi.text, d.wifi.shift, d.wifi.symbols, d.wifi.err = &n, "", false, false, ""
		d.mu.Unlock()
	}
}

// wifiKey is a key on the passphrase keyboard.
func (d *Display) wifiKey(k string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.wifi.busy != "" {
		return
	}
	switch k {
	case "":
	case "shift":
		d.wifi.shift = !d.wifi.shift
	case "symbols":
		d.wifi.symbols = !d.wifi.symbols
	case "space":
		d.wifi.text += " "
	case "backspace":
		if n := len(d.wifi.text); n > 0 {
			d.wifi.text = d.wifi.text[:n-1]
		}
	case "cancel":
		d.wifi.pick, d.wifi.text = nil, ""
	case "join":
		n := *d.wifi.pick
		text := d.wifi.text
		d.mu.Unlock()
		d.wifiJoin(n, text)
		d.mu.Lock()
	default:
		if d.wifi.shift && !d.wifi.symbols {
			k = strings.ToUpper(k)
			d.wifi.shift = false
		}
		d.wifi.text += k
	}
}

// wifiJoin joins a network in the background and shows how it went.
func (d *Display) wifiJoin(n wifi.Network, passphrase string) {
	d.mu.Lock()
	d.wifi.busy, d.wifi.err = "Connecting to "+n.SSID+"…", ""
	d.mu.Unlock()
	d.wake()
	go func() {
		err := wifi.Join(context.Background(), n.SSID, passphrase)
		d.mu.Lock()
		d.wifi.busy = ""
		if err != nil {
			d.wifi.err = err.Error()
			slog.Warn("wifi: join failed", "ssid", n.SSID, "err", err)
		} else {
			d.wifi.pick, d.wifi.text = nil, ""
			slog.Info("wifi: joined", "ssid", n.SSID)
		}
		d.mu.Unlock()
		d.refreshWifi()
	}()
}

// answerShots hands a copy of the canvas to whoever asked for a screenshot.
func (d *Display) answerShots() {
	for {
		select {
		case ch := <-d.shots:
			var img *image.RGBA
			if d.r != nil {
				img = image.NewRGBA(d.r.dst.Rect)
				copy(img.Pix, d.r.dst.Pix)
			}
			ch <- img
		default:
			return
		}
	}
}

// Screenshot is the next frame drawn, for a look at the panel from afar.
func (d *Display) Screenshot(ctx context.Context) (*image.RGBA, error) {
	ch := make(chan *image.RGBA, 1)
	select {
	case d.shots <- ch:
	default:
		return nil, errors.New("too many screenshot requests")
	}
	d.wake()
	select {
	case img := <-ch:
		if img == nil {
			return nil, errors.New("the screen is not up")
		}
		return img, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// SetTheme switches to a preset by name and saves it.
func (d *Display) SetTheme(name string) {
	if err := config.Set().Screen().Theme(themes[themeIndex(name)].name); err != nil {
		slog.Warn("saving the theme failed", "err", err)
	}
	d.wake()
}

// Demo puts placeholders in for the owner's details for a while, for published screenshots.
func (d *Display) Demo(for_ time.Duration) {
	d.mu.Lock()
	d.demoUntil = time.Now().Add(for_)
	d.mu.Unlock()
	d.wake()
}

// OpenSheet puts the settings sheet up on the named tab, or takes it down for "off". It reports whether
// the name meant anything.
func (d *Display) OpenSheet(name string) bool {
	if strings.EqualFold(name, "off") {
		d.showSheet(false)
		return true
	}
	switch strings.ToLower(name) {
	case "cameras":
		d.showSheet(false)
		d.openDrawer(drawerCameras)
		return true
	case "radio":
		d.showSheet(false)
		d.openDrawer(drawerRadio)
		return true
	case "dashboard":
		d.showSheet(false)
		d.closeDrawer()
		return d.openDashboard()
	}
	cat, ok := catByName(name)
	if !ok {
		return false
	}
	open := func() {
		d.closeDrawer()
		d.mu.Lock()
		d.sheet, d.cat, d.picker, d.restartArm = true, cat, "", time.Time{}
		d.draft, d.cardScroll, d.pickScroll = nil, 0, 0
		d.mu.Unlock()
		d.wake()
	}
	if security.Locked() {
		// Asked for from Home Assistant or by voice, the settings are behind the lock all the same.
		openPINRemote(open)
		d.wake()
		return true
	}
	open()
	return true
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
	d.r = newRenderer(dev.Canvas())
	w, h := dev.Size()
	d.mu.Lock()
	d.wide = w > 1000 || h > 1000
	d.mu.Unlock()
	d.logo = newSplash(w, h)
	fw, fh := dev.FrameSize()
	video.Get().UseScreen(video.Screen{W: w, H: h, Rotated: dev.Rotated(), PixFmt: dev.PixFmt()}, fw, fh)
	d.mu.Lock()
	d.booting, d.started = true, time.Now()
	d.mu.Unlock()
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

// Run redraws the screen until ctx is canceled: on the second while idle, faster while a turn is
// on or the volume is showing, and at once when something changes.
func (d *Display) Run(ctx context.Context) error {
	go d.settle(ctx)
	for {
		wait := d.frame()
		if d.videoPainting {
			// The video's frames, until the frame loop has to look again.
			d.paintVideo(ctx, wait, d.videoOver)
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

// frame draws what the moment calls for and says how long until the next one is due.
func (d *Display) frame() time.Duration {
	d.videoPainting, d.vp.dark = false, false
	d.mu.Lock()
	on, view, at := d.on, d.view, d.viewAt
	volume, volAt := d.volume, d.volAt
	d.mu.Unlock()

	now := time.Now()
	d.popupTick(now)
	ring := d.ringing(now)
	call := phone.Get().State()
	_, reminding := remind.Get().Showing()
	night := nightNow(now)
	if !on && (call.Phase != phone.Idle || (!night && (ring.any() || reminding))) {
		// A call lights a dark panel, at night to half brightness (relight): its page is how it is
		// answered. By day a ring and a reminder do too, the ring's page being how it is stopped and a
		// reminder being its words. At night they leave the panel dark: "stop" or a tap ends a ring.
		d.apply(true, d.ceilingOrDefault(), false)
		on = true
	}
	if !on && d.popupUp() != nil && !nightNow(now) {
		// A pop-up lights a dark panel by day. At night it waits there, dark, until the screen is woken.
		d.apply(true, d.ceilingOrDefault(), false)
		on = true
	}
	if vs := video.Get().State(); vs.Active() && vs.ID != d.videoLit && (vs.Phase != video.Asking || !nightNow(now)) {
		// A video starting lights a dark panel, day or night: somebody asked for it to be watched. Once
		// per video, so a screen turned off while one plays stays off. A DLNA video asking first lights it
		// only by day: anybody on the network can ask, and at night the question waits out unseen.
		d.videoLit = vs.ID
		if !on {
			// At night at the night light's level, as a turn's words are: a video asked for at 2 a.m.
			// is not the room lit up.
			// With no night light set, it is the night's half brightness (relight), as for a call.
			if night && config.Get().Screen.NightLight {
				d.mu.Lock()
				d.nightGlow, d.nightDark = true, false
				d.mu.Unlock()
			}
			d.apply(true, d.ceilingOrDefault(), false)
			on = true
		}
	}
	if !on && sunriseProgress(now) > 0 {
		// The light before an alarm: the panel comes on dim and rises, so the room is lit before the
		// sound starts. relight decides how much of the screen's brightness it is using.
		d.apply(true, d.ceilingOrDefault(), false)
		on = true
	}
	if d.night(now, on, view) {
		// The panel put out: nothing to draw. The glow lifted or lowered: this frame shows the change,
		// rather than the old page for a minute at the new brightness.
		d.mu.Lock()
		lit := d.on
		d.mu.Unlock()
		if !lit {
			return time.Minute
		}
		on = lit // the night may have just turned the panel back on: this frame is its first
	}
	d.mu.Lock()
	sheetUp, wifiUp := d.sheet, d.wifiOpen
	d.mu.Unlock()
	busy := view.Phase != "idle" || call.Phase != phone.Idle || ring.any() || reminding || sheetUp || pinIsOpen() ||
		sunriseProgress(now) > 0 || d.popupUp() != nil || setup.Get().Waiting() || wifiUp || video.Get().State().Active()
	if d.awayTick(now, on, busy, night) {
		d.mu.Lock()
		on = d.on
		d.mu.Unlock()
	}
	if !config.Get().Screen.Welcomed {
		d.r.welcome(scene{now: now})
		if err := d.dev.Present(); err != nil {
			slog.Warn("presenting the frame failed", "err", err)
		}
		d.answerShots()
		return time.Second
	}
	if !on && !video.Get().State().Active() {
		d.vp.drop() // a video that ended while the panel was dark: its frame goes with it
	}
	if !on && video.Get().State().Frames {
		// A dark panel with a video playing: its frames are still taken as they fall due, so its sound
		// goes on (the decoder makes both), and nothing is drawn.
		video.Get().Covered(false)
		d.videoPainting, d.vp.dark, d.videoOver = true, true, image.Rectangle{}
		return videoRecheck
	}
	if !on {
		// Dark panel: nothing to draw, and nothing to redraw until told, or until the night ends.
		d.answerShots()
		if config.Get().Screen.Night != "" {
			return time.Minute
		}
		return time.Hour
	}
	t := current()
	applyTheme(t)
	d.syncThemeSelect(t.name)

	d.mu.Lock()
	booting, started := d.booting, d.started
	if booting && now.Sub(started) >= splashMin && voice.Get().Ready() {
		d.booting, booting = false, false
		slog.Info("splash done", "after", now.Sub(started).Round(time.Millisecond))
	}
	// A ring ends the splash whatever else is or is not ready. Waiting on Home Assistant's voice
	// pipeline has no timeout, so a device that never reaches it stays on the logo forever — and an
	// alarm going off behind a logo is a screen that will not say what is making the noise or where
	// to press to stop it. Nothing the splash is waiting for is needed to draw a ringing page.
	if booting && ring.any() {
		d.booting, booting = false, false
		slog.Info("splash cut short: something is ringing", "after", now.Sub(started).Round(time.Millisecond))
	}
	// So does the Wi-Fi page: it opens by itself on a device with no address, which is a device whose
	// splash would otherwise wait for a Home Assistant it cannot reach, over the one page that fixes it.
	if booting && d.wifiOpen {
		d.booting, booting = false, false
		slog.Info("splash cut short: Wi-Fi setup", "after", now.Sub(started).Round(time.Millisecond))
	}
	// And a device with no Home Assistant access: one that left the Home Assistant it had, or never
	// had one. Home Assistant may still add it, but nothing says it will, and the clock, the alarms
	// and the settings - the way to the setup page among them - were all behind the logo.
	if booting && now.Sub(started) >= noHomeAssistantWait && !hass.Get().Ready() {
		d.booting, booting = false, false
		slog.Info("splash cut short: no Home Assistant access", "after", now.Sub(started).Round(time.Millisecond))
	}
	d.mu.Unlock()
	if booting {
		d.r.drawSplash(d.logo, now.Sub(started))
		if err := d.dev.Present(); err != nil {
			slog.Warn("presenting the frame failed", "err", err)
		}
		return 80 * time.Millisecond
	}

	s := scene{now: now, phase: view.Phase, heard: view.Heard, reply: view.Reply, since: at, ring: ring, call: call}
	s.snooze = config.Get().Alarms.Snooze()
	s.alarms = alarm.Get().View(now)
	s.timers = timer.Get().List(now)
	s.glance = home.Get().Glance()
	if view.Phase == "idle" && (view.Heard != "" || view.Reply != "") && now.Sub(at) < linger() {
		s.phase = "lingering"
	}
	d.mu.Lock()
	quiet := d.quiet
	d.mu.Unlock()
	if quiet && (s.phase == "thinking" || s.phase == "replying" || s.phase == "lingering") {
		// A screen command: the screen it asked for is the answer, not the words.
		s.phase, s.heard, s.reply = "idle", "", ""
	}
	if equalizerOn() && (s.phase == "listening" || s.phase == "thinking" || s.phase == "replying" || s.phase == "lingering") {
		s.eq = eqFor(s.phase, nightNow(now), now)
		s.eq.wave = waveOn()
	}
	// A stream this player is carrying is the room's when it is what is being heard: the page names it,
	// and says what it is doing, though the audio never passes through this player's own stream. Both,
	// not just playing: the page tests paused first, so a station left paused underneath would label
	// somebody else's track as Paused - and a remote merely holding the speaker, with a station playing
	// underneath it, is not the room's at all (see media.Player.Carried). A remote's track paused from
	// here and since let go is shown paused too (see media.Player.Held).
	s.playing, s.paused = media.Get().ScreenState()
	s.muted, _ = mute.Get().Muted()
	if !volAt.IsZero() && now.Sub(volAt) < volumeShow {
		s.volume, s.showVolume = volume, true
	}
	s.bt = btaudio.Get().State()
	d.mu.Lock()
	s.showSheet = d.sheet
	s.pin = pinNow(now)
	relockOnClose(s.showSheet)
	s.showWifi, s.wifi = d.wifiOpen, d.wifi
	restartArm := d.restartArm
	wifiAt := d.wifiAt
	d.mu.Unlock()
	if (s.showSheet || s.showWifi) && now.Sub(wifiAt) > 5*time.Second && wifi.Available() {
		d.mu.Lock()
		d.wifiAt = now
		d.mu.Unlock()
		go d.refreshWifi()
	}
	if s.showSheet {
		s.sheet = d.gather(s, restartArm)
		// The category gather already took under the lock, rather than d.cat again: the sheet is
		// opened on a category from whatever goroutine took the tap or the voice command, and
		// reading it unlocked here is a race that would also let the frame disagree with itself.
		if s.sheet.cat == catSecurity {
			s.security = security.Get().State()
		}
		d.mu.Lock()
		demo := now.Before(d.demoUntil)
		d.mu.Unlock()
		if demo {
			s.sheet.name, s.sheet.wifi, s.sheet.address = "Kitchen", "HomeWiFi  ·  192.168.1.50", "192.168.1.50"
			s.sheet.wifiName, s.sheet.weather, s.sheet.demo = "HomeWiFi", "Home", true
			for i := range s.security.Keys {
				s.security.Keys[i] = "laptop"
			}
		}
		d.mu.Lock()
		if d.draft != nil && d.cat == catAlarms {
			c := *d.draft
			s.draft = &c
		}
		d.mu.Unlock()
	}
	s.camera, s.showCamera = home.Get().Camera()
	s.cameraSound, s.cameraSoundLive = home.Get().CameraSoundOn(), home.Get().CameraSoundLive()
	if s.showCamera {
		s.talkOffered, s.talk = talkback.Offered(s.camera.Entity), talkback.Get().State()
	}
	// Whether the idle screen wants to be what is playing. It is asked even when the page has been put
	// away, because the track is what brings it back, so the radio is read either way.
	wants := (s.phase == "idle") && d.nowPlaying()
	d.mu.Lock()
	s.showDrawer, s.drawerTab, s.drawerScroll, s.drawerPick, s.pickScroll = d.drawer && !s.showSheet && !s.showCamera, d.drawerTab, d.drawerScroll, d.drawerPick, d.pickScroll
	d.mu.Unlock()
	d.mu.Lock()
	s.demo = now.Before(d.demoUntil)
	s.redClock = d.nightGlow && config.Get().Screen.NightRed
	s.redStyle = config.Get().Screen.NightClockStyle
	d.mu.Unlock()
	if s.showDrawer && s.drawerTab == drawerCameras {
		s.cameras = home.Get().Cameras()
	}
	if s.showDrawer && s.drawerTab == drawerCall {
		s.callees = phone.Get().Callees()
		d.mu.Lock()
		d.callees = s.callees
		d.mu.Unlock()
	}
	if (s.showDrawer && s.drawerTab == drawerRadio) || wants {
		s.radio = home.Get().Radio()
		if s.radio.Followed {
			s.playing, s.paused = s.radio.Playing, s.radio.Paused
		}
	}
	s.nowPlaying = wants && !d.putAway(s.radio, wants, s.playing)
	d.mu.Lock()
	// Not under the sunrise light, which draws no strip: taps on a strip nobody can see would still act.
	if wants && d.stripDue(now, s.radio, s.nowPlaying) && sunriseProgress(now) == 0 {
		s.nowPlaying, s.strip = false, true
	}
	s.faved = d.favedKey != "" && d.favedKey == s.radio.Title+"\x00"+s.radio.Now
	if s.nowPlaying {
		s.lyric, s.hasLyric = home.Get().LyricNow(s.radio, now)
	}
	d.showingPlaying, d.showingStrip, d.showingWord = s.nowPlaying, s.strip, playingWord(s) != ""
	d.mu.Unlock()
	s.weather = home.Get().Weather()
	s.style = styleFactsFor(clockStyle(), now)
	s.style.named = d.styleName(now)
	d.calendarScene(&s, now)
	d.alertScene(&s, now)
	d.mu.Lock()
	s.showWeather = (s.phase == "idle" || s.phase == "lingering") && now.Before(d.weatherUntil)
	s.showRadar = s.showWeather && d.radar
	d.mu.Unlock()
	if s.showWeather {
		s.forecast = home.Get().Forecast()
		// Asked for while the forecast is up, so the rain map is there by the time its button is
		// pressed rather than starting then; it fetches only when due.
		s.radar = home.Get().Radar()
		if !s.showRadar {
			s.sky = skyNow(weatherNow(s.weather, s.forecast, s.now))
		}
	}
	if !s.showRadar {
		s.radar = home.RadarView{}
	}

	// boring is the plain idle page — the same set of pages draw() checks before falling through to
	// bigClock/nowPlaying. Background mode rides along with it; Screensaver only takes over once it
	// has held for the configured wait, tracked by how long it has run continuously.
	d.deckScene(&s, now)
	d.dashScene(&s, s.showSheet || s.showDrawer || ring.any() || call.Phase != phone.Idle)
	boring := s.phase == "idle" && call.Phase == phone.Idle && !ring.any() && !s.bt.Pairing &&
		!s.showWifi && !s.showSheet && !s.showCamera && !s.showRadar && !s.showWeather && !s.showCalendar && !s.showAlert && !s.nowPlaying &&
		!s.showDash && !s.showDeck
	// A browser waiting to be let in is a page of its own, over whatever is on the screen: asking for
	// the setup page is done from the settings screen, so the answer has to reach somebody who is
	// still standing in it. It was set only on the idle page once, and the press could not be given
	// without leaving settings first.
	s.setupAsking = setup.Get().Waiting()
	d.videoScene(&s, now)
	if s.showVideo {
		boring = false
	}

	// The announcement state is read every frame like the setup page's, for the same reason: the
	// drawer's button, the page an arriving one puts up and the line that says this microphone is
	// open all have to be right wherever the screen happens to be.
	s.announceReady = config.Get().Home.HouseWord != ""
	s.announceRecording = announce.Get().Recording()
	s.announcePeers = len(announce.Peers())
	s.announcement, s.showAnnouncement = announce.Get().Showing()
	s.reminder, s.showReminder = remind.Get().Showing()
	s.popup = d.popupUp()
	if s.reminder.From != config.Get().Device.Name {
		s.reminderFrom = s.reminder.From
	}
	s.missed = missedNote(now, false)

	if boring {
		s.sunrise, s.sunriseFace = sunriseProgress(now), config.Get().Alarms.SunriseFace
		// The weather art only where it will be seen: not under the night clock or the light before an
		// alarm, which take the whole screen, where composing it each second is work for nothing.
		artSeen := !s.redClock && s.sunrise == 0
		s.slideshow = home.Get().SlideshowBackground()
		if home.Get().SlideshowMode() == config.SlideshowBackground && artSeen {
			if art, fx := sceneArt(now, d.r.w, d.r.h); art != nil {
				s.slideshow, s.artFx = art, fx
			}
		}
		if s.slideshow == nil {
			s.slideshowTrouble = home.Get().SlideshowTrouble()
		}
	}
	d.mu.Lock()
	d.clockUp = boring && s.sunrise == 0 && s.slideshowScreensaver == nil && !s.redClock
	if !boring {
		d.slideshowIdleSince = time.Time{}
	} else if d.slideshowIdleSince.IsZero() {
		d.slideshowIdleSince = now
	}
	idleSince := d.slideshowIdleSince
	d.mu.Unlock()
	if boring && !idleSince.IsZero() && now.Sub(idleSince) >= home.Get().SlideshowIdleTimeout() {
		s.slideshowScreensaver = home.Get().SlideshowScreensaverPhoto()
		if home.Get().SlideshowMode() == config.SlideshowScreensaver && !s.redClock && s.sunrise == 0 {
			if art, fx := sceneArt(now, d.r.w, d.r.h); art != nil {
				s.slideshowScreensaver, s.artFx = art, fx
			}
		}
		s.slideshowOverlay = home.Get().SlideshowOverlay()
	}

	// The Call button is on the clock itself and only there: on any other page a tap where it would be
	// still does what that page does.
	s.callButton = callButton.Load() && s.phase == "idle" && !s.nowPlaying && !s.strip && !s.showWeather && !s.showCalendar && !s.showDeck &&
		s.sunrise == 0 && s.slideshowScreensaver == nil && !s.showDrawer && !s.showSheet
	d.mu.Lock()
	d.callShown = s.callButton
	d.mu.Unlock()

	if s.showVideo && s.videoLive {
		// The picture's own frames go to the panel (paintVideo); the canvas holds only the controls.
		d.r.draw(s)
		d.videoPainting, d.videoOver = true, d.r.videoOver
		d.deckDrawn = "" // the deck under it is drawn again once the video is gone
		d.answerVideoShots()
		return d.videoNext(now)
	}
	if !s.showVideo && !s.video.Active() {
		// Kept while the video is only covered: uncovered, paused, the frame it stopped on is put back.
		d.dropVideoFrame()
	}
	if key := deckFrameKey(s, ring.any(), call.Phase != phone.Idle); key != "" && key == d.deckDrawn {
		// The deck as it was last drawn, and nothing over it: drawing it again would only cost.
		d.answerShots()
		return 2 * time.Second
	} else {
		d.deckDrawn = key
	}
	d.r.draw(s)
	if err := d.dev.Present(); err != nil {
		slog.Warn("presenting the frame failed", "err", err)
	}
	d.answerShots()

	if d.r.flipBusy(time.Now()) {
		return flipFrame // a card is flipping: the night's flip clock, or the Flip clock style
	}
	if s.showCamera {
		return 250 * time.Millisecond // frames arrive as they are fetched; this keeps up
	}
	if s.showDash {
		return time.Second // a streamed picture wakes it as it arrives
	}
	if ring.any() || call.Phase != phone.Idle {
		return 500 * time.Millisecond
	}
	if s.bt.Pairing || s.showSheet || s.showWifi || s.showDrawer {
		return 500 * time.Millisecond
	}
	if s.showRadar && len(s.radar.Frames) > 1 {
		return radarStep
	}
	if s.showWeather && s.sky != fxNone && !s.setupAsking {
		return fxFrame
	}
	if d.r.artDrawn {
		return artFxFrame // rain or snow is falling over the weather art on the screen
	}
	if (s.slideshow != nil || s.slideshowScreensaver != nil) && home.Get().SlideshowTransitioning() {
		return home.SlideshowFrame
	}
	if turnShown(s) && !(s.phase == "lingering" && s.eq.quiet) {
		if s.eq.wave {
			return waveFrame
		}
		return eqFrame // the bars are moving
	}
	if s.showWeather || s.nowPlaying {
		wait := time.Until(now.Truncate(idleFrame).Add(idleFrame))
		if s.hasLyric && s.lyric.In > 0 && s.lyric.In < wait {
			wait = s.lyric.In + 20*time.Millisecond // the next line, as it is sung
		}
		return wait
	}
	if (s.phase == "idle" || s.phase == "lingering") && !s.showVolume {
		// On the next whole second, so the clock changes when the second does.
		return time.Until(now.Truncate(idleFrame).Add(idleFrame))
	}
	return activeFrame
}
