//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"log/slog"
	"math"
	"strings"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/alarm"
	"github.com/HuskerMinion/techo5/echod/internal/feature/announce"
	"github.com/HuskerMinion/techo5/echod/internal/feature/btaudio"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	"github.com/HuskerMinion/techo5/echod/internal/feature/security"
	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
)

// The palette (walnut ground, amber accent, cream text, dim text, ember rules) is in sheet_widgets.go,
// shared with the Spot's settings screen; the theme in force sets it.

// shade is a translucent strip for text over a picture.
var shade = color.RGBA{0x00, 0x00, 0x00, 0x90}

// scene is one frame's worth of facts.
type scene struct {
	now     time.Time
	phase   string // idle, listening, thinking, replying, lingering
	heard   string
	reply   string
	since   time.Time
	playing bool
	paused  bool
	muted   bool

	// volume is shown while it moves: the step out of media.VolumeSteps.
	volume     int
	showVolume bool

	// bt is the Bluetooth audio state: the pairing page replaces everything while it is on, and a
	// connected device is named in the footer.
	bt btaudio.State

	// sheet is the settings sheet, drawn instead of everything else while showSheet is set.
	showSheet bool
	sheet     settings
	// security feeds the settings screen's Privacy & Security card.
	security security.State

	// call is the phone: while one rings, is placed or is up, its page is over everything.
	call phone.State

	// ring is a timer or an alarm sounding: the ringing page is over everything. snooze is its length.
	ring   ringState
	snooze int

	// alarms feeds the Alarms card and the next alarm on the clock; draft is the editor's alarm, when
	// one is open. timers are the running timers, soonest first.
	alarms alarm.View
	draft  *alarmDraft
	timers []timer.Countdown

	// showWifi is the Wi-Fi pages, over everything but pairing.
	showWifi bool
	wifi     wifiState

	// radio feeds the drawer's Radio side and the now-playing screen; weather is on the clock when known.
	radio   home.Radio
	weather home.Weather

	// showWeather is the forecast page, for a while after a weather question; nowPlaying is the
	// idle screen while the radio plays or sits paused.
	showWeather bool
	forecast    forecastDays
	// showRadar is the rain map in place of the forecast.
	showRadar  bool
	radar      home.RadarView
	nowPlaying bool

	// showDrawer is Cameras and Radio, in from the right over the idle page: drawerTab is which,
	// drawerScroll how far its list is scrolled, drawerPick a list of choices open over it, and
	// pickScroll how far that list is scrolled.
	showDrawer   bool
	drawerTab    int
	drawerScroll int
	drawerPick   string
	pickScroll   int

	// demo puts placeholders in for the owner's details, for screenshots that will be published.
	demo bool

	// showCamera is a live camera view, over everything but the sheet; cameras feeds the drawer.
	showCamera bool
	camera     home.CameraView
	cameras    []config.Camera

	// slideshow is Background mode's current photo, drawn under the idle clock in place of the
	// flat background; nil off that mode or before a first photo arrives.
	slideshow *image.RGBA

	// slideshowScreensaver is Screensaver mode's current photo, taking the whole screen once set
	// (the idle wait has already been checked); slideshowOverlay is its clock/date size.
	slideshowScreensaver *image.RGBA
	slideshowOverlay     string

	// slideshowTrouble is why the slideshow has no photo, once it has given up looking; it sits in
	// the footer so a folder that went away is visible rather than silently retried.
	slideshowTrouble string

	// announceReady is whether this house has a word set, without which announcements go nowhere;
	// announceRecording whether this device has its microphone open for one now; announcePeers how
	// many other devices are listening for them.
	announceReady     bool
	announceRecording bool
	announcePeers     int

	// announcement is one that arrived and is still being shown, with showAnnouncement saying so.
	announcement     announce.Message
	showAnnouncement bool

	// setupAsking is a browser waiting to be let into the setup page. It is said on the screen so
	// that a request for a press is never something only the browser knows about.
	setupAsking bool

	// sunrise is how far the light before an alarm has come, 0 to 1, and sunriseFace whether the sun
	// is drawn with a face on it.
	sunrise     float64
	sunriseFace bool
}

// renderer draws scenes onto one canvas. Faces are made once: parsing a font is cheap, but
// building a face at each size is not something to do per frame.
type renderer struct {
	paint            // the canvas, its size, and the settings screen's tap zones
	clock  font.Face // the big time
	big    font.Face // a large reading, like today's temperature
	ampm   font.Face
	title  font.Face // "Listening…"
	body   font.Face // transcript and reply
	small  font.Face // date, corner clock, footer
	tiny   font.Face
	margin int

	// base keeps the settings screen's unchanging part.
	base    *image.RGBA
	baseKey baseKey

	// shell keeps what every category of the settings screen shares, for as long as the theme holds.
	shell    *image.RGBA
	shellKey baseKey
}

func newRenderer(dst *image.RGBA) *renderer {
	r := &renderer{paint: paint{dst: dst, w: dst.Rect.Dx(), h: dst.Rect.Dy()}, margin: 40}
	bold, err := opentype.Parse(gobold.TTF)
	if err != nil {
		slog.Error("parsing the bold font failed", "err", err)
	}
	regular, err := opentype.Parse(goregular.TTF)
	if err != nil {
		slog.Error("parsing the regular font failed", "err", err)
	}
	face := func(f *opentype.Font, size float64) font.Face {
		if f == nil {
			return nil
		}
		fc, err := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
		if err != nil {
			slog.Error("making a font face failed", "size", size, "err", err)
			return nil
		}
		return fc
	}
	r.clock = face(bold, 230)
	r.big = face(bold, 100)
	r.ampm = face(bold, 56)
	r.title = face(bold, 48)
	r.body = face(regular, 42)
	r.small = face(regular, 34)
	r.tiny = face(regular, 26)
	return r
}

// draw composes a whole frame. Everything is repainted: the canvas is small and a full paint is
// simpler than tracking what changed.
func (r *renderer) draw(s scene) {
	draw.Draw(r.dst, r.dst.Rect, image.NewUniform(walnut), image.Point{}, draw.Src)

	if s.call.Phase != phone.Idle {
		r.callPage(s)
		return
	}
	if s.ring.any() {
		r.ringingPage(s)
		return
	}
	// A browser waiting to be let in: the answer is a tap here, since this device has no button for
	// it. Under a call and under a ringing alarm, both of which are somebody already being answered.
	if s.setupAsking {
		r.setupAskPage(s)
		return
	}
	if s.bt.Pairing {
		r.pairingPage(s)
		if s.showVolume {
			r.volumeBar(s)
		}
		return
	}
	if s.showWifi {
		r.wifiPage(s)
		if s.showVolume {
			r.volumeBar(s)
		}
		return
	}
	// The two strips sit over whatever is on the screen rather than taking it: the clock, the music
	// and the photos all carry on behind them. A call and a ringing alarm are above them, since both
	// are waiting on somebody.
	defer func() {
		switch {
		case s.announceRecording:
			r.recordingStrip(s)
		case s.showAnnouncement:
			r.announcementStrip(s)
		}
	}()

	if s.showSheet {
		r.settingsScreen(s)
		if s.showVolume {
			r.volumeBar(s)
		}
		return
	}
	if s.showCamera {
		r.cameraView(s, s.camera)
		if s.showVolume {
			r.volumeBar(s)
		}
		return
	}
	if s.showRadar {
		r.radarPage(s)
		if s.showVolume {
			r.volumeBar(s)
		}
		return
	}
	if s.showWeather {
		r.weatherPage(s)
		r.footer(s)
		if s.showVolume {
			r.volumeBar(s)
		}
		return
	}

	switch s.phase {
	case "listening":
		r.status(s, "Listening…", true)
	case "thinking":
		r.status(s, "Thinking…", true)
		r.words(s.heard, "", 200)
	case "replying", "lingering":
		r.cornerClock(s)
		r.words(s.heard, s.reply, 70)
	default:
		if s.sunrise > 0 {
			// The light before an alarm takes the whole screen: the panel is the lamp in the room, and
			// a screen filled with warm color is worth more than the backlight on its own.
			r.sunrisePage(s, s.sunrise, s.sunriseFace)
		} else if s.slideshowScreensaver != nil {
			r.slideshowScreensaverPage(s)
		} else if s.nowPlaying {
			r.nowPlaying(s)
		} else {
			if s.slideshow != nil {
				r.slideshowBackground(s.slideshow)
			}
			r.bigClock(s)
		}
	}
	r.footer(s)
	if s.showDrawer {
		r.drawer(s)
	}
	if s.showVolume {
		r.volumeBar(s)
	}
}

// volumeBar is the level, laid over the bottom of whatever is showing while it moves.
func (r *renderer) volumeBar(s scene) {
	top := r.h - 110
	draw.Draw(r.dst, image.Rect(0, top, r.w, r.h), image.NewUniform(walnut), image.Point{}, draw.Src)
	label := "Volume"
	r.text(r.small, label, r.margin, top+38, dim)
	full := r.w - 2*r.margin
	y0 := top + 56
	draw.Draw(r.dst, image.Rect(r.margin, y0, r.margin+full, y0+14), image.NewUniform(ember), image.Point{}, draw.Src)
	fill := full * min(max(s.volume, 0), media.VolumeSteps) / media.VolumeSteps
	draw.Draw(r.dst, image.Rect(r.margin, y0, r.margin+fill, y0+14), image.NewUniform(amber), image.Point{}, draw.Src)
	pct := fmt.Sprintf("%d%%", s.volume*100/media.VolumeSteps)
	r.text(r.small, pct, r.w-r.margin-r.width(r.small, pct), top+38, cream)
}

// timeAndDate draws the hour, AM/PM and date centred, with the hour's baseline at base and an
// optional suffix appended to the date line (an alarm note, on the ordinary idle page). Shared by
// bigClock and the screensaver's normal-size overlay, which wants the clock alone.
func (r *renderer) timeAndDate(now time.Time, base int, dateSuffix string) {
	hour := clockHM(now)
	ampm := clockSuffix(now)
	hw := r.width(r.clock, hour)
	aw := r.width(r.ampm, ampm)
	gap := 18
	if ampm == "" {
		gap = 0
	}
	x := (r.w - hw - gap - aw) / 2
	r.text(r.clock, hour, x, base, cream)
	r.text(r.ampm, ampm, x+hw+gap, base, amber)

	date := now.Format("Monday, January 2") + dateSuffix
	r.text(r.small, date, (r.w-r.width(r.small, date))/2, base+70, dim)
}

// bigClock is the idle screen: the time across the middle, the date beneath, and under that the running
// timers. With timers the clock moves up to make room. The next alarm, when it is within a day, follows
// the date.
func (r *renderer) bigClock(s scene) {
	base := r.h/2 + 60
	timers := false
	for _, t := range s.timers {
		timers = timers || t.Active
	}
	if timers {
		base -= 36
	}

	suffix := ""
	if next := s.alarms.Next; next != nil && next.At.Sub(s.now) < 24*time.Hour {
		what := "Alarm"
		if next.Snoozed {
			what = "Snoozed until"
		}
		suffix = "  ·  " + what + " " + clockText(next.At)
	}
	r.timeAndDate(s.now, base, suffix)
	if timers {
		r.timersLine(s, base+128)
	}

	r.weatherCorner(s)
}

// weatherCorner is the weather where it has always been, top left, with the drawing of it that the
// forecast page has had all along. The icon is read before the words are: a glance at the corner of
// a clock is not reading, and the screen already knew how to draw it.
func (r *renderer) weatherCorner(s scene) {
	w := s.weather
	if w.Temp == "" && w.Condition == "" {
		return
	}
	line := w.Temp
	if c := conditionWords(w.Condition); c != "" {
		if line != "" {
			line += "  ·  "
		}
		line += c
	}
	x := r.margin
	if w.Condition != "" && conditionWords(w.Condition) != "" {
		r.weatherIcon(w.Condition, r.margin+weatherMark/2, r.margin+15, weatherMark)
		x += weatherMark + 14
	}
	r.text(r.small, line, x, r.margin+26, dim)
}

// weatherMark is how big the corner's icon is: the height of the line it sits beside, so it reads as
// part of it rather than as a picture somebody put there.
const weatherMark = 46

// conditionWords turns Home Assistant's weather state into words for the screen.
func conditionWords(c string) string {
	switch c {
	case "", "unknown", "unavailable":
		return ""
	case "clear-night":
		return "Clear"
	case "partlycloudy":
		return "Partly cloudy"
	case "lightning-rainy":
		return "Thunderstorms"
	case "snowy-rainy":
		return "Sleet"
	case "exceptional":
		return "Severe"
	}
	// "sunny", "cloudy", "rainy", "pouring", "fog", "hail", "snowy", "windy", "lightning"…
	return strings.ToUpper(c[:1]) + c[1:]
}

// cornerClock keeps the time in view while words have the screen.
func (r *renderer) cornerClock(s scene) {
	t := clockText(s.now)
	r.text(r.small, t, r.w-r.margin-r.width(r.small, t), r.margin+26, dim)
}

// cornerClockDated is the corner clock with the day under it, for a screen somebody looks at for
// minutes rather than glances at: while music plays the big clock is gone, and the date went with it.
func (r *renderer) cornerClockDated(s scene) {
	r.cornerClock(s)
	d := s.now.Format("Mon, Jan 2")
	r.text(r.tiny, d, r.w-r.margin-r.width(r.tiny, d), r.margin+54, dim)
}

// status is a phase title with an indicator that breathes while the device waits.
func (r *renderer) status(s scene, title string, breathe bool) {
	r.cornerClock(s)
	r.text(r.title, title, r.margin, 120, amber)
	if breathe {
		// A bar under the title, its length rising and falling with a period of 1.6 s.
		t := float64(s.now.Sub(s.since).Milliseconds()) / 1600
		f := 0.55 + 0.45*math.Sin(2*math.Pi*t)
		full := r.w - 2*r.margin
		draw.Draw(r.dst, image.Rect(r.margin, 140, r.margin+full, 146), image.NewUniform(ember), image.Point{}, draw.Src)
		draw.Draw(r.dst, image.Rect(r.margin, 140, r.margin+int(float64(full)*f), 146), image.NewUniform(amber), image.Point{}, draw.Src)
	}
}

// words lays out what was heard, dimmed, and the reply beneath it, starting at top and stopping at
// the footer. A long reply is shrunk one step before being cut.
func (r *renderer) words(heard, reply string, top int) {
	y := top
	maxW := r.w - 2*r.margin
	bottom := r.h - 70
	if heard != "" {
		for _, line := range r.wrap(r.small, "“"+heard+"”", maxW) {
			if y+40 > bottom {
				break
			}
			r.text(r.small, line, r.margin, y+30, dim)
			y += 42
		}
		y += 18
	}
	if reply == "" {
		return
	}
	face, lineH := r.body, 52
	lines := r.wrap(face, reply, maxW)
	if len(lines)*lineH > bottom-y {
		face, lineH = r.small, 42
		lines = r.wrap(face, reply, maxW)
	}
	for i, line := range lines {
		if y+lineH > bottom {
			if i > 0 {
				r.text(face, "…", r.margin, y, cream)
			}
			break
		}
		r.text(face, line, r.margin, y+lineH-12, cream)
		y += lineH
	}
}

// footer is the bottom edge: what is playing, and whether the microphones are cut.
func (r *renderer) footer(s scene) {
	y := r.h - 24
	switch {
	case s.setupAsking:
		r.text(r.tiny, "setup: a browser is asking to be let in", r.margin, y, amber)
	case s.muted:
		r.text(r.tiny, "microphone off", r.margin, y, amber)
	case s.slideshowTrouble != "":
		r.text(r.tiny, s.slideshowTrouble, r.margin, y, dim)
	}
	var right string
	switch {
	case s.playing:
		right = "♪ playing"
	case s.paused:
		right = "♪ paused"
	}
	if s.bt.Connected != "" {
		if right != "" {
			right += "  ·  "
		}
		right += "BT " + s.bt.Connected
	}
	if right != "" {
		r.text(r.tiny, right, r.w-r.margin-r.width(r.tiny, right), y, dim)
	}
}
