//go:build spot

package display

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"strings"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/announce"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
)

// The round panel: everything is laid out from its centre, and nothing may sit where the circle
// cuts it off.
const (
	side   = 480
	centre = side / 2
	rimOut = 236 // outer edge of the status ring
	rimIn  = 222 // inner edge
)

var (
	colBackground = color.RGBA{10, 13, 18, 255}
	colText       = color.RGBA{236, 240, 244, 255}
	colDim        = color.RGBA{128, 138, 150, 255}
	colTrack      = color.RGBA{34, 40, 48, 255}
	colListening  = color.RGBA{58, 160, 255, 255}
	colThinking   = color.RGBA{64, 214, 230, 255}
	colReplying   = color.RGBA{60, 203, 127, 255}
	colMuted      = color.RGBA{229, 72, 77, 255}
	colTimer      = color.RGBA{255, 176, 32, 255}

	// colAccent is the Show's Ember accent, for the AM/PM beside the time as the Show sets it.
	colAccent = color.RGBA{0xf0, 0x5a, 0x3c, 255}
)

// ampmGap is the space between the time and its AM/PM.
const ampmGap = 10

// timeLine draws the time centred on the face at baseline, with its AM/PM beside it in the accent
// the way the Show sets it; on a 24-hour clock the time stands alone.
func (r *roundRenderer) timeLine(now time.Time, baseline int) {
	hm := clockHM(now)
	r.centred(r.clock, hm, baseline, colText)
	if s := clockSuffix(now); s != "" {
		r.text(r.title, s, centre+r.width(r.clock, hm)/2+ampmGap, baseline, colAccent)
	}
}

type roundScene struct {
	now          time.Time
	phase        string // idle, listening, thinking, replying, lingering
	heard, reply string
	muted        bool
	playing      bool
	paused       bool
	volume       int
	maxVolume    int
	showVolume   bool
	timers       []timer.Countdown
	menuOpen     bool
	menuMode     menuMode
	menuSel      int     // the chosen item, at or turning to the top
	menuRot      float64 // the dial's rotation, radians clockwise

	timerRinging bool

	weather  home.Weather
	forecast forecastDays

	// nowPlaying is the idle face given to music; radio what it and the station list show;
	// radioSel the list's row in the middle; radar the rain map, radarOn the weather face turned to it.
	nowPlaying bool
	radio      home.Radio
	radioSel   int
	radar      home.RadarView
	radarOn    bool

	camera     home.CameraView
	showCamera bool
	cameraLive bool // the sensor is running

	btPairing bool

	// call is the phone: while one rings, is placed or is up, its face is over everything.
	call phone.State

	// ringing is a timer or an alarm sounding: its face is over everything but a call.
	ringing ringing

	// cameras is the camera list and cameraSel its row in the middle, while the list is open.
	cameras   []config.Camera
	cameraSel int

	// phoneReady is whether the phone is signed in; contacts are who Call lists (while it is open),
	// contactCount how many there are.
	phoneReady   bool
	contacts     []phone.Contact
	contactCount int
	contactTop   int // the first contact shown

	// slideshow is Background mode's current photo, drawn under the clock face in place of the flat
	// background. slideshowScreensaver is Screensaver mode's, taking the whole face; slideshowOverlay
	// is its clock size.
	slideshow            *image.RGBA
	slideshowScreensaver *image.RGBA
	slideshowOverlay     string

	// slideshowTrouble is why the slideshow has no photo, once it has given up looking.
	slideshowTrouble string

	// announceReady is whether this house has a word set, announceRecording whether this device is
	// taking an announcement now, announcePeers how many others are listening, and announcement one
	// that arrived and is still showing.
	announceReady     bool
	announceRecording bool
	announcePeers     int
	announcement      announce.Message
	showAnnouncement  bool

	// setupAsking is a browser waiting to be let into the setup page, said on the face so that a
	// request for a press is never something only the browser knows about.
	setupAsking bool

	// sunrise is how far the light before an alarm has come, 0 to 1, and sunriseFace whether the sun
	// is drawn with a face on it.
	sunrise     float64
	sunriseFace bool

	// sheetOpen is the settings screen up, sheetGrid its six categories rather than one; sheet what
	// it shows.
	sheetOpen, sheetGrid bool
	sheet                sheetView
}

type roundRenderer struct {
	paint                            // the canvas, and the settings screen's tap zones
	clock, title, body, small, label font.Face
	tiny                             font.Face
}

func newRoundRenderer(dst *image.RGBA) *roundRenderer {
	bold, err := opentype.Parse(gobold.TTF)
	if err != nil {
		panic(err)
	}
	regular, err := opentype.Parse(goregular.TTF)
	if err != nil {
		panic(err)
	}
	face := func(f *opentype.Font, size float64) font.Face {
		fc, err := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
		if err != nil {
			panic(err)
		}
		return fc
	}
	return &roundRenderer{
		paint: paint{dst: dst, w: side, h: side, fc: spotFaces(), round: true},
		clock: face(bold, 104),
		title: face(bold, 34),
		body:  face(regular, 26),
		small: face(regular, 24),
		label: face(bold, 20),
		tiny:  face(regular, 16),
	}
}

func (r *roundRenderer) draw(s roundScene) {
	draw.Draw(r.dst, r.dst.Rect, image.NewUniform(colBackground), image.Point{}, draw.Src)

	// Muted is drawn last, over whatever the face turns out to be: see mutedRim.
	defer func() {
		if s.muted {
			r.mutedRim()
		}
	}()

	if s.call.Phase != phone.Idle {
		r.callFace(s)
		return
	}
	if s.ringing.any() {
		r.ringFace(s)
		return
	}
	// A browser waiting to be let in: the answer is a tap here, since this device has no button for
	// it. Under a call and under a ringing alarm, both of which are somebody already being answered.
	if s.setupAsking {
		r.setupAskFace(s)
		return
	}
	// This device taking an announcement, then one that arrived: both take the face, since a circle
	// has no corner to put them in. Under a call and under a ringing alarm, which wait on somebody.
	if s.announceRecording {
		r.recordingFace(s)
		return
	}
	if s.showAnnouncement {
		r.announceFace(s)
		return
	}
	if s.sheetOpen {
		r.sheetFace(s)
		return
	}
	r.rim(s)
	switch {
	case s.showCamera && !s.showVolume:
		r.cameraView(s)
		r.rim(s) // the view clears the panel; the rim still says muted or listening
	case s.showVolume:
		r.volume(s)
	case s.phase == "listening" || s.phase == "thinking" || s.phase == "replying" || s.phase == "lingering":
		r.conversation(s)
	case s.nowPlaying:
		r.nowPlayingFace(s)
	case s.slideshowScreensaver != nil:
		r.slideshowScreensaverFace(s)
	default:
		if s.sunrise > 0 {
			// The light before an alarm takes the whole face: the panel is the lamp in the room.
			r.sunriseFace(s, s.sunrise, s.sunriseFace)
			break
		}
		if s.slideshow != nil {
			r.slideshowBackground(s.slideshow)
		}
		r.clockFace(s)
	}
	if s.menuOpen {
		r.menu(s)
	}
	if s.cameraLive {
		r.cameraDot()
	}
}

// rim is the status ring: red while muted, the conversation's color while one runs, the soonest
// timer's time left, or a quiet track.
// mutedRim says the microphones are cut, over the top of anything.
//
// This device has no mute light - its button is the keypad's power key and there is no lamp behind
// it - so the rim is the only place the state can be seen at all. It used to be drawn with the rest
// of the rim, which meant it disappeared behind a call, a ringing alarm, the settings sheet and an
// announcement: every face that takes the whole circle. An announcement was the worst of them,
// because a house full of devices announcing at each other is exactly when somebody reaches for the
// mute button and wants to know whether it took.
func (r *roundRenderer) mutedRim() {
	r.arc(rimIn, rimOut, 0, 2*math.Pi, colMuted)
}

func (r *roundRenderer) rim(s roundScene) {
	switch {
	case s.muted:
		r.mutedRim()
	case s.phase == "listening":
		pulse := 0.55 + 0.45*math.Sin(float64(s.now.UnixMilli())/180)
		r.arc(rimIn, rimOut, 0, 2*math.Pi, fade(colListening, pulse))
	case s.phase == "thinking":
		start := math.Mod(float64(s.now.UnixMilli())/300, 2*math.Pi)
		r.arc(rimIn, rimOut, 0, 2*math.Pi, colTrack)
		r.arc(rimIn, rimOut, start, start+math.Pi/2, colThinking)
	case s.phase == "replying":
		r.arc(rimIn, rimOut, 0, 2*math.Pi, colReplying)
	case s.btPairing:
		pulse := 0.45 + 0.55*math.Abs(math.Sin(float64(s.now.UnixMilli())/500))
		r.arc(rimIn, rimOut, 0, 2*math.Pi, fade(colBluetooth, pulse))
	case len(s.timers) > 0 && s.timers[0].Total > 0:
		t := s.timers[0]
		left := float64(t.Left) / float64(t.Total)
		r.arc(rimIn, rimOut, 0, 2*math.Pi, colTrack)
		r.arc(rimIn, rimOut, 0, 2*math.Pi*math.Min(math.Max(left, 0), 1), colTimer)
	default:
		r.arc(rimIn, rimOut, 0, 2*math.Pi, colTrack)
	}
}

func (r *roundRenderer) clockFace(s roundScene) {
	now := s.now
	r.timeLine(now, 240)
	r.centred(r.small, now.Format("Monday, January 2"), 290, colDim)

	line := 332
	if weatherLine(s.weather) != "" {
		r.clockWeather(s.weather, line)
		line += 38
	}
	if len(s.timers) > 0 {
		t := s.timers[0]
		r.centred(r.body, "Timer "+clockDuration(t.Left), line, colTimer)
		line += 34
	}
	if s.slideshowTrouble != "" {
		r.centred(r.small, s.slideshowTrouble, line, colDim)
		line += 34
	}
	switch {
	case s.setupAsking:
		r.centred(r.label, "A BROWSER IS ASKING", 118, colMuted)
	case s.muted:
		r.centred(r.label, "MICROPHONE OFF", 118, colMuted)
	case s.btPairing:
		r.centred(r.label, "BLUETOOTH PAIRING", 118, colBluetooth)
	case s.playing:
		r.centred(r.label, "PLAYING", 118, colDim)
	case s.paused:
		r.centred(r.label, "PAUSED", 118, colDim)
	}
}

func (r *roundRenderer) conversation(s roundScene) {
	title, col := "", colText
	switch s.phase {
	case "listening":
		title, col = "Listening", colListening
	case "thinking":
		title, col = "Thinking", colThinking
	}
	y := 150
	if title != "" {
		r.centred(r.title, title, y, col)
		y += 50
	}
	if s.heard != "" {
		y = r.paragraph(r.body, s.heard, y, colDim, 3)
		y += 12
	}
	if s.reply != "" && (s.phase == "replying" || s.phase == "lingering") {
		r.paragraph(r.body, s.reply, y, colText, 5)
	}
}

func (r *roundRenderer) volume(s roundScene) {
	frac := 0.0
	if s.maxVolume > 0 {
		frac = float64(s.volume) / float64(s.maxVolume)
	}
	// An inner arc for the level, from the bottom-left round to the bottom-right.
	const from, span = 1.25 * math.Pi, 1.5 * math.Pi
	r.arc(170, 190, from, from+span, colTrack)
	r.arc(170, 190, from, from+span*frac, colListening)
	r.centred(r.clock, fmt.Sprintf("%d", s.volume), 270, colText)
	r.centred(r.small, "VOLUME", 320, colDim)
}

// arc fills the ring between radii r0 and r1 from angle a0 to a1, clockwise from straight up, with
// a one-pixel soft edge on both circles.
func (r *roundRenderer) arc(r0, r1 float64, a0, a1 float64, c color.RGBA) {
	r.ringAt(centre, centre, r0, r1, a0, a1, c)
}

// ringAt is arc about any centre.
func (r *roundRenderer) ringAt(cx, cy, r0, r1 float64, a0, a1 float64, c color.RGBA) {
	full := a1-a0 >= 2*math.Pi-1e-9
	b := r.dst.Rect
	outer, inner := r1+1, r0-1
	for y := max(int(cy-r1)-1, b.Min.Y); y <= min(int(cy+r1)+1, b.Max.Y-1); y++ {
		dy := float64(y) + 0.5 - cy
		if math.Abs(dy) > outer {
			continue
		}
		// Only this row's stretch of the ring: from its outer edge in to the hole, on each side, rather
		// than every pixel of the square round it.
		xo := math.Sqrt(outer*outer-dy*dy) + 1
		spans := [][2]int{{int(cx - xo), int(cx + xo)}}
		if inner > 1 && math.Abs(dy) < inner-1 {
			xi := math.Sqrt(inner*inner-dy*dy) - 1
			spans = [][2]int{{int(cx - xo), int(cx - xi)}, {int(cx+xi) + 1, int(cx + xo)}}
		}
		for _, sp := range spans {
			for x := max(sp[0], b.Min.X); x <= min(sp[1], b.Max.X-1); x++ {
				dx := float64(x) + 0.5 - cx
				d := math.Hypot(dx, dy)
				if d < r0-1 || d > r1+1 {
					continue
				}
				if !full {
					a := math.Atan2(dx, -dy)
					if a < 0 {
						a += 2 * math.Pi
					}
					lo := math.Mod(a0, 2*math.Pi)
					if lo < 0 {
						lo += 2 * math.Pi
					}
					rel := a - lo
					if rel < 0 {
						rel += 2 * math.Pi
					}
					if rel > a1-a0 {
						continue
					}
				}
				cover := math.Min(math.Min(d-(r0-1), (r1+1)-d), 1)
				r.blend(x, y, c, cover)
			}
		}
	}
}

func (r *roundRenderer) discAt(cx, cy, rad float64, c color.RGBA) {
	for y := int(cy - rad - 1); y <= int(cy+rad+1); y++ {
		for x := int(cx - rad - 1); x <= int(cx+rad+1); x++ {
			if !(image.Point{x, y}.In(r.dst.Rect)) {
				continue
			}
			d := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy)
			if d > rad+1 {
				continue
			}
			r.blend(x, y, c, math.Min(rad+1-d, 1))
		}
	}
}

// line strokes a segment w wide with round ends.
func (r *roundRenderer) line(x0, y0, x1, y1, w float64, c color.RGBA) {
	hw := w / 2
	dx, dy := x1-x0, y1-y0
	l2 := dx*dx + dy*dy
	for y := int(math.Min(y0, y1) - hw - 1); y <= int(math.Max(y0, y1)+hw+1); y++ {
		for x := int(math.Min(x0, x1) - hw - 1); x <= int(math.Max(x0, x1)+hw+1); x++ {
			if !(image.Point{x, y}.In(r.dst.Rect)) {
				continue
			}
			px, py := float64(x)+0.5, float64(y)+0.5
			t := 0.0
			if l2 > 0 {
				t = math.Min(math.Max(((px-x0)*dx+(py-y0)*dy)/l2, 0), 1)
			}
			d := math.Hypot(px-(x0+t*dx), py-(y0+t*dy))
			r.blend(x, y, c, math.Min(hw+0.5-d, 1))
		}
	}
}

// triangle fills a triangle, with a soft edge.
func (r *roundRenderer) triangle(ax, ay, bx, by, cx, cy float64, c color.RGBA) {
	edge := func(px, py, x0, y0, x1, y1 float64) float64 {
		ex, ey := x1-x0, y1-y0
		return ((px-x0)*ey - (py-y0)*ex) / math.Hypot(ex, ey)
	}
	sign := 1.0
	if edge(cx, cy, ax, ay, bx, by) < 0 {
		sign = -1
	}
	for y := int(math.Min(ay, math.Min(by, cy))) - 1; y <= int(math.Max(ay, math.Max(by, cy)))+1; y++ {
		for x := int(math.Min(ax, math.Min(bx, cx))) - 1; x <= int(math.Max(ax, math.Max(bx, cx)))+1; x++ {
			if !(image.Point{x, y}.In(r.dst.Rect)) {
				continue
			}
			px, py := float64(x)+0.5, float64(y)+0.5
			d := math.Min(sign*edge(px, py, ax, ay, bx, by), math.Min(sign*edge(px, py, bx, by, cx, cy), sign*edge(px, py, cx, cy, ax, ay)))
			r.blend(x, y, c, math.Min(d+0.5, 1))
		}
	}
}

// clear paints the canvas the icon ground.
func (r *roundRenderer) clear() {
	draw.Draw(r.dst, r.dst.Rect, image.NewUniform(colIconGround), image.Point{}, draw.Src)
}

// dim darkens the whole canvas by alpha.
func (r *roundRenderer) dim(alpha uint8) {
	draw.Draw(r.dst, r.dst.Rect, image.NewUniform(color.RGBA{0, 0, 0, alpha}), image.Point{}, draw.Over)
}

func (r *roundRenderer) blend(x, y int, c color.RGBA, cover float64) {
	if cover <= 0 {
		return
	}
	i := r.dst.PixOffset(x, y)
	a := cover * float64(c.A) / 255
	p := r.dst.Pix[i : i+4 : i+4]
	p[0] = uint8(float64(p[0])*(1-a) + float64(c.R)*a)
	p[1] = uint8(float64(p[1])*(1-a) + float64(c.G)*a)
	p[2] = uint8(float64(p[2])*(1-a) + float64(c.B)*a)
	p[3] = 255
}

func (r *roundRenderer) text(face font.Face, s string, x, baseline int, c color.Color) {
	d := &font.Drawer{Dst: r.dst, Src: image.NewUniform(c), Face: face, Dot: fixed.P(x, baseline)}
	d.DrawString(s)
}

func (r *roundRenderer) width(face font.Face, s string) int {
	return font.MeasureString(face, s).Round()
}

// centred2 is centred about x rather than the middle of the panel.
func (r *roundRenderer) centred2(face font.Face, s string, x, baseline int, c color.Color) {
	r.text(face, s, x-r.width(face, s)/2, baseline, c)
}

func (r *roundRenderer) centred(face font.Face, s string, baseline int, c color.Color) {
	if s == "" {
		return
	}
	r.text(face, s, centre-r.width(face, s)/2, baseline, c)
}

// paragraph wraps s to the width of the circle at each line's height and draws up to maxLines,
// centred. It returns the baseline after the last line.
func (r *roundRenderer) paragraph(face font.Face, s string, baseline int, c color.Color, maxLines int) int {
	lineH := face.Metrics().Height.Round() + 4
	words := strings.Fields(s)
	for n := 0; n < maxLines && len(words) > 0; n++ {
		avail := chord(baseline-10) - 56
		line := words[0]
		k := 1
		for ; k < len(words); k++ {
			try := line + " " + words[k]
			if r.width(face, try) > avail {
				break
			}
			line = try
		}
		words = words[k:]
		if n == maxLines-1 && len(words) > 0 {
			line += "…"
		}
		r.centred(face, line, baseline, c)
		baseline += lineH
	}
	return baseline
}

// chord is the width of the circle at height y.
func chord(y int) int {
	dy := float64(y - centre)
	if math.Abs(dy) >= centre {
		return 0
	}
	return int(2 * math.Sqrt(float64(centre*centre)-dy*dy))
}

func clockDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h, m, s := int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

func fade(c color.RGBA, k float64) color.RGBA {
	return color.RGBA{c.R, c.G, c.B, uint8(float64(c.A) * math.Min(math.Max(k, 0), 1))}
}
