//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"log/slog"
	"math"
	"sync"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/alarm"
	"github.com/HuskerMinion/techo5/echod/internal/feature/announce"
	"github.com/HuskerMinion/techo5/echod/internal/feature/btaudio"
	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	"github.com/HuskerMinion/techo5/echod/internal/feature/remind"
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

	// The dashboard page, drawn instead of the clock while showDash is set: how it is shown, and
	// when streamed, what arrived.
	showDash   bool
	dashMode   config.DashboardMode
	dash       dashboard.View
	drawn      dashboard.Drawn
	dashScroll int
	dashAdjust dashAdjusting // a level a finger is sliding

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

	// redClock is the night light as the red clock alone, in redStyle (render_night.go).
	redClock bool
	redStyle string

	// demo puts placeholders in for the owner's details, for screenshots that will be published.
	demo bool

	// showCamera is a live camera view, over everything but the sheet; cameras feeds the drawer.
	showCamera bool
	camera     home.CameraView
	cameras    []config.Camera

	// callees are who the drawer's Call tab offers; callButton is the clock's Call button showing.
	callees    []phone.Callee
	callButton bool

	// sky is the forecast page's weather moving (weatherfx.go), fxNone for a still one.
	sky skyFx

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

	// missed is a ring that fell due while the device could not sound it, said in the footer.
	missed string

	// strip is the music in a strip at the foot of the clock page, rather than on its own page; faved
	// is the star having been pressed for what is playing.
	strip bool
	faved bool

	// announceReady is whether this house has a word set, without which announcements go nowhere;
	// announceRecording whether this device has its microphone open for one now; announcePeers how
	// many other devices are listening for them.
	announceReady     bool
	announceRecording bool
	announcePeers     int

	// announcement is one that arrived and is still being shown, with showAnnouncement saying so.
	announcement     announce.Message
	showAnnouncement bool

	// reminder is one going off, with showReminder saying so; reminderFrom is the device it was set
	// on when that was another one, and empty for this device's own.
	reminder     remind.Reminder
	showReminder bool
	reminderFrom string

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
	paint // the canvas, its size, and the settings screen's tap zones

	// flip is the night's flip clock: what its cards show, and a flip under way.
	flip flipState

	// weatherAt is where the home screen's weather was drawn in the frame last drawn, for a tap there
	// to open the forecast; empty when it was not drawn. Written while drawing, read by the touch
	// goroutine, so under its own lock.
	weatherMu sync.Mutex
	weatherAt image.Rectangle

	// weatherKept is the forecast page as last drawn, and weatherKey what it showed: while the sky
	// moves the page is drawn twelve times a second, and the page itself changes once a minute.
	weatherKept []byte
	weatherKey  string

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

// The two large answers at the foot of a page that demands one: Stop and Snooze, Decline and
// Answer, Not now and Allow. All three pages put them in the same place, so a finger learns one
// position, and all three draw them with the same rounded button. In the Show 5's pixels.
const (
	actionGap    = 24  // between the two
	actionBottom = 30  // under them
	actionHeight = 120 // how tall they are
	actionReach  = 20  // above them, a tap still decides nothing
	actionRadius = 28  // rounded enough to read as a button at this size
)

// actionBand is the top and bottom of those answers. Anchored to the foot of the screen rather than
// measured from the top, so a taller panel sets them under the words instead of stretching them
// down the page.
func (r *renderer) actionBand() (y0, y1 int) {
	y1 = r.h - r.s(actionBottom)
	return y1 - r.s(actionHeight), y1
}

// actionHalves splits the band into the two answers, left and right.
func (r *renderer) actionHalves() (left, right image.Rectangle) {
	y0, y1 := r.actionBand()
	half := r.s(actionGap) / 2
	return image.Rect(r.margin, y0, r.w/2-half, y1), image.Rect(r.w/2+half, y0, r.w-r.margin, y1)
}

// actionDecided is whether a tap this far down the screen is an answer at all. A tap on the words
// above decides nothing, so reading a page cannot answer it.
func (r *renderer) actionDecided(y int) bool {
	y0, _ := r.actionBand()
	return y >= y0-r.s(actionReach)
}

// drawnFor is the panel width every fixed size in this package is written in: the Echo Show 5's, in
// landscape. A wider panel scales them up rather than leaving the layout in one corner of it.
const drawnFor = 960

func newRenderer(dst *image.RGBA) *renderer {
	// Built in place: paint carries a mutex, so it must never be assembled elsewhere and copied in.
	r := &renderer{paint: paint{dst: dst, w: dst.Rect.Dx(), h: dst.Rect.Dy(), sNum: dst.Rect.Dx(), sDen: drawnFor}}
	r.margin = r.s(40)
	bold, err := opentype.Parse(gobold.TTF)
	if err != nil {
		slog.Error("parsing the bold font failed", "err", err)
	}
	regular, err := opentype.Parse(goregular.TTF)
	if err != nil {
		slog.Error("parsing the regular font failed", "err", err)
	}
	// Sizes are points at 72 DPI, which makes them pixels, so they scale with everything else.
	face := func(f *opentype.Font, size int) font.Face {
		if f == nil {
			return nil
		}
		fc, err := opentype.NewFace(f, &opentype.FaceOptions{Size: float64(r.s(size)), DPI: 72, Hinting: font.HintingFull})
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
	if r.scaled() {
		fc := sheetFacesAt(r.s)
		r.fc = &fc
	}
	return r
}

// draw composes a whole frame. Everything is repainted: the canvas is small and a full paint is
// simpler than tracking what changed.
func (r *renderer) draw(s scene) {
	r.setWeatherAt(image.Rectangle{})
	// The red night clock is the whole screen: nothing else, not even the header, is drawn over it.
	// Anything that needs somebody - a call, an alarm, a turn - has already lifted the night light,
	// and this with it.
	if s.redClock && s.call.Phase == phone.Idle && !s.ring.any() && !s.setupAsking {
		r.redClockPage(s)
		return
	}
	draw.Draw(r.dst, r.dst.Rect, image.NewUniform(walnut), image.Point{}, draw.Src)

	// Registered first so it runs last: the header goes over every page, including the ones below
	// that return early.
	defer r.header(s)

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
		// The card is in the middle and the strips along the bottom, so a reminder and an
		// announcement can both be up at once.
		if s.showReminder {
			r.reminderCard(s)
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
		r.weatherPageKept(s)
		// The bolt strikes in the gap between today's reading and the five days.
		r.sky(s.sky, s.now, r.dst.Rect, image.Rect(r.w/2-r.s(80), r.s(70), r.w/2-r.s(10), r.s(400)))
		r.footer(s)
		if s.showVolume {
			r.volumeBar(s)
		}
		return
	}
	if s.showDash {
		r.dashboardPage(s)
		if s.showVolume {
			r.volumeBar(s)
		}
		return
	}

	var behind *image.RGBA // the photo behind the idle page, when it has one
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
				behind = s.slideshow
			}
			r.readableOver(behind, walnut, slideshowWash, func() { r.bigClock(s) })
			if s.callButton {
				r.callButtonDraw()
			}
		}
	}
	// The footer's words sit on the photo too, when there is one.
	r.readableOver(behind, walnut, slideshowWash, func() { r.footer(s) })
	if s.strip && s.phase == "idle" && s.sunrise == 0 {
		r.musicStrip(s)
	}
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

// timeAndDate draws the hour, AM/PM and date centered, with the hour's baseline at base and an
// optional suffix appended to the date line (an alarm note, on the ordinary idle page). Shared by
// bigClock and the screensaver's normal-size overlay, which wants the clock alone.
func (r *renderer) timeAndDate(now time.Time, base int, dateSuffix string) {
	hour := clockHM(now)
	ampm := clockSuffix(now)
	hw := r.width(r.clock, hour)
	aw := r.width(r.ampm, ampm)
	gap := r.s(18)
	if ampm == "" {
		gap = 0
	}
	x := (r.w - hw - gap - aw) / 2
	r.text(r.clock, hour, x, base, cream)
	r.text(r.ampm, ampm, x+hw+gap, base, amber)

	date := now.Format("Monday, January 2") + dateSuffix
	r.text(r.small, date, (r.w-r.width(r.small, date))/2, base+r.s(70), dim)
}

// bigClock is the idle screen: the time across the middle, the date beneath, and under that the running
// timers. With timers the clock moves up to make room. The next alarm, when it is within a day, follows
// the date.
func (r *renderer) bigClock(s scene) {
	base := r.h/2 + r.s(60)
	timers := false
	for _, t := range s.timers {
		timers = timers || t.Active
	}
	if timers {
		base -= r.s(36)
	}
	if s.strip {
		// The music strip takes the foot of the panel; the clock and date move up out of its way.
		base -= r.s(30)
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
		r.timersLine(s, base+r.s(128))
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
		r.over.note(image.Rect(r.margin, r.margin+15-weatherMark/2, r.margin+weatherMark, r.margin+15+weatherMark/2))
		x += weatherMark + 14
	}
	r.text(r.small, line, x, r.margin+26, dim)
	// A tap on it opens the forecast: the icon and the words, with a finger's room around them.
	r.setWeatherAt(image.Rect(r.margin, r.margin+15-weatherMark/2, x+r.width(r.small, line), r.margin+15+weatherMark/2).Inset(-r.s(14)))
}

func (r *renderer) setWeatherAt(b image.Rectangle) {
	r.weatherMu.Lock()
	r.weatherAt = b
	r.weatherMu.Unlock()
}

// weatherTapped is whether a tap at p landed on the home screen's weather, as last drawn.
func (r *renderer) weatherTapped(p image.Point) bool {
	r.weatherMu.Lock()
	defer r.weatherMu.Unlock()
	return !r.weatherAt.Empty() && p.In(r.weatherAt)
}

// weatherMark is how big the corner's icon is: the height of the line it sits beside, so it reads as
// part of it rather than as a picture somebody put there.
const weatherMark = 46

// conditionWords is home.ConditionWords, by its old name here.
func conditionWords(c string) string { return home.ConditionWords(c) }

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
	r.text(r.tiny, d, r.w-r.margin-r.width(r.tiny, d), r.margin+r.s(54), dim)
}

// status is a phase title with an indicator that breathes while the device waits.
func (r *renderer) status(s scene, title string, breathe bool) {
	r.cornerClock(s)
	r.text(r.title, title, r.margin, r.s(120), amber)
	if breathe {
		// A bar under the title, its length rising and falling with a period of 1.6 s.
		t := float64(s.now.Sub(s.since).Milliseconds()) / 1600
		f := 0.55 + 0.45*math.Sin(2*math.Pi*t)
		full := r.w - 2*r.margin
		draw.Draw(r.dst, image.Rect(r.margin, r.s(140), r.margin+full, r.s(146)), image.NewUniform(ember), image.Point{}, draw.Src)
		draw.Draw(r.dst, image.Rect(r.margin, r.s(140), r.margin+int(float64(full)*f), r.s(146)), image.NewUniform(amber), image.Point{}, draw.Src)
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

// headerH is the strip at the very top of the panel that the header keeps itself inside.
//
// Most pages begin their own content at the margin — the weather corner, the corner clock, the
// ringing page's title all start there — so the header sits above them and no page has to make room
// for it. Two pages do draw this high: the weather page's art and the settings sheet's card. The
// weather page is drawn over, which is what the pill is for; the sheet is left alone entirely.
const headerH = 36

// header is the top edge: what is true of the device whatever page is showing.
//
// It is drawn last and over everything, including a call and a ringing alarm. Those are not
// exceptions to tell somebody about a cut microphone — they are the moments it matters most, since a
// ringing alarm that cannot hear "stop" looks exactly like one that can.
//
// The strip is filled before the words are drawn, so the line stays readable over a photo, a camera
// view or a sunrise. On an ordinary page that fill is the background and nothing shows but the words.
func (r *renderer) header(s scene) {
	// The settings sheet is the one page that says this better itself: it has a Microphone row,
	// reading "Muted" or "Listening", with the switch that changes it. A strip over the top of the
	// sheet would say the same thing worse and cut the top off its card.
	if s.showSheet {
		return
	}

	// The microphone is the only thing here so far. It is a switch so the next one has somewhere
	// obvious to go, and so two of them can never overprint each other.
	var line string
	switch {
	case s.muted:
		line = "microphone off"
	default:
		return
	}

	// A pill behind the words rather than a band across the panel. The weather page and the sunrise
	// draw art that reaches the top edge, and a full-width fill cuts a slot through it; a pill only
	// takes the space the words need, and on a plain page it is the background and does not show.
	w := r.width(r.tiny, line)
	pad := r.s(14)
	x := (r.w - w) / 2
	r.roundButton(image.Rect(x-pad, r.s(4), x+w+pad, r.s(32)), float64(r.s(14)), walnut)
	r.text(r.tiny, line, x, r.s(25), amber)
}

// playingWord is what the footer says about the music: empty when nothing is playing.
func playingWord(s scene) string {
	switch {
	case s.strip:
		return "" // the strip says it, and the footer is under it
	case s.playing:
		return "♪ playing"
	case s.paused:
		return "♪ paused"
	}
	return ""
}

// callButtonRect is the clock's Call button: a round one in the bottom-left corner, in the gap between
// the date and the footer's line. At 76 it reached up into the date on a Show 5, where a long date
// line starts near the left edge and a tap on its first letters opened a call; 56 fits under it, and
// the tap target is still wider than the button (display.go takes touches 12 around it).
func (r *renderer) callButtonRect() image.Rectangle {
	side := r.s(56)
	return image.Rect(r.margin, r.h-r.s(44)-side, r.margin+side, r.h-r.s(44))
}

// callButtonDraw draws it: the call green, with a handset, raised off whatever is behind it.
func (r *renderer) callButtonDraw() {
	b := r.callButtonRect()
	rad := float64(b.Dx()) / 2
	green := color.RGBA{0x2e, 0xa0, 0x4f, 0xff}
	r.roundShadow(b, rad, r.sf(10), r.s(3), shadowAlpha())
	r.roundFill(b, rad, shift(green, 16), shift(green, -12))
	r.roundHighlight(b, rad)
	icon := r.s(32)
	r.mdiIcon("phone", b.Min.X+(b.Dx()-icon)/2, b.Min.Y+(b.Dy()-icon)/2, 32, cream)
}

// playingButton is where the footer's word for the music sits: the right end of the bottom edge. The
// whole of it is the target, because the word moves when a connected device is named beside it.
func (r *renderer) playingButton() image.Rectangle {
	return image.Rect(r.w-r.s(280), r.h-r.s(42), r.w, r.h-r.s(4))
}

// footer is the bottom edge: what is playing, and what the page underneath is having trouble with.
//
// The microphone used to be here. It moved to the header, because the footer is drawn by two of the
// pages and the microphone is true on all of them.
func (r *renderer) footer(s scene) {
	y := r.h - 24
	switch {
	case s.setupAsking:
		r.text(r.tiny, "setup: a browser is asking to be let in", r.margin, y, amber)
	case s.missed != "":
		r.text(r.tiny, s.missed, r.margin, y, amber)
	case s.slideshowTrouble != "":
		r.text(r.tiny, s.slideshowTrouble, r.margin, y, dim)
	}
	right := playingWord(s)
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
