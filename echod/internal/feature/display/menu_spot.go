//go:build spot

package display

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"slices"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// The ring menu is a dial: the items sit on a circle, the one at the top is chosen, drawn larger in
// its own color, and named in the middle. While it is open a finger dragged round the ring turns it;
// letting go snaps it to the nearest item; a tap on an item does it, and a tap in the middle does the
// one at the top.
//
// Volume turns the whole ring into a jog wheel: turning it clockwise raises the value a step per
// jogStep, a tap finishes. Settings opens the settings screen (sheet_spot.go).

const (
	dialR      = 165 // radius the items sit on
	dialHub    = 86  // inside this a tap is on the middle
	dialHit    = 42  // how near an item a tap has to land
	chosenRing = 44  // the chosen item's outline

	// jogStep is how far the ring turns per step of a value.
	jogStep = 15 * math.Pi / 180
)

type menuMode int

const (
	modeMain menuMode = iota
	modeVolume
	modeWeather
	modeCalendar
	modeRadio
	modeCameras
	modeContacts
)

// jogging is whether the mode turns the ring into a jog wheel.
func (m menuMode) jogging() bool {
	return m == modeVolume
}

type itemID string

const (
	itemTalk     itemID = "talk"
	itemCall     itemID = "call"
	itemMute     itemID = "mute"
	itemMusic    itemID = "music"
	itemVolume   itemID = "volume"
	itemWeather  itemID = "weather"
	itemCalendar itemID = "calendar"
	itemCamera   itemID = "camera"
	itemTimers   itemID = "timers"
	itemAnnounce itemID = "announce"
	itemSettings itemID = "settings"
	itemSleep    itemID = "sleep"

	itemDashboard itemID = "dashboard"
)

type menuItem struct {
	id    itemID
	color color.RGBA
}

// mainItems go clockwise from the top.
var mainItems = []menuItem{
	{itemTalk, color.RGBA{240, 98, 146, 255}},
	{itemCall, color.RGBA{46, 204, 113, 255}},
	{itemMute, color.RGBA{229, 72, 77, 255}},
	{itemMusic, color.RGBA{60, 203, 127, 255}},
	{itemVolume, color.RGBA{58, 160, 255, 255}},
	{itemWeather, color.RGBA{255, 196, 64, 255}},
	{itemCalendar, color.RGBA{88, 160, 214, 255}},
	{itemCamera, color.RGBA{60, 203, 127, 255}},
	{itemDashboard, color.RGBA{64, 214, 230, 255}},
	{itemTimers, color.RGBA{255, 176, 32, 255}},
	{itemAnnounce, color.RGBA{255, 122, 89, 255}},
	{itemSettings, color.RGBA{176, 150, 255, 255}},
	{itemSleep, color.RGBA{120, 140, 255, 255}},
}

// colBluetooth is Bluetooth's blue: the rim while pairing.
var colBluetooth = color.RGBA{0, 130, 252, 255}

// itemsFor is the dial a mode shows; nil for a mode that is not a dial.
func itemsFor(m menuMode) []menuItem {
	switch m {
	case modeMain:
		if len(home.Get().CalendarSources()) == 0 {
			// No calendar chosen: no calendar on the dial.
			return slices.DeleteFunc(slices.Clone(mainItems), func(m menuItem) bool { return m.id == itemCalendar })
		}
		return mainItems
	case modeCameras:
		return cameraItems(home.Get().Cameras())
	}
	return nil
}

// cameraItems is the cameras dial: one item per camera, in the list's order.
func cameraItems(cams []config.Camera) []menuItem {
	hues := []color.RGBA{{60, 203, 127, 255}, {58, 160, 255, 255}, {255, 196, 64, 255}, {240, 98, 146, 255}, {176, 150, 255, 255}, {64, 214, 230, 255}, {255, 120, 80, 255}}
	out := make([]menuItem, len(cams))
	for i := range cams {
		out[i] = menuItem{cameraItem(i), hues[i%len(hues)]}
	}
	return out
}

// initials is up to two capitals from a name: "Front door" is FD, "Deck" is D.
func initials(name string) string {
	out := ""
	for _, w := range strings.Fields(name) {
		if r := []rune(w); len(r) > 0 && len(out) < 2 {
			out += strings.ToUpper(string(r[0]))
		}
	}
	return out
}

// cameraItem is camera i's item, and cameraOf the camera an item is, or -1.
func cameraItem(i int) itemID { return itemID(fmt.Sprintf("cam:%d", i)) }

func cameraOf(id itemID) int {
	var i int
	if _, err := fmt.Sscanf(string(id), "cam:%d", &i); err != nil {
		return -1
	}
	return i
}

// dialItems is the dial a scene shows: the cameras dial from the scene's own list, so a preview can
// draw one.
func dialItems(s roundScene) []menuItem {
	if s.menuMode == modeCameras {
		return cameraItems(s.cameras)
	}
	return itemsFor(s.menuMode)
}

func indexOf(items []menuItem, id itemID) int {
	for i, it := range items {
		if it.id == id {
			return i
		}
	}
	return 0
}

var (
	colIcon       = color.RGBA{200, 206, 214, 255}
	colIconGround = color.RGBA{3, 4, 6, 255}
)

// itemAngle is where item i of n sits with the dial at rest, in radians clockwise from straight up.
func itemAngle(i, n int) float64 { return float64(i) * 2 * math.Pi / float64(n) }

// restFor is the dial rotation that puts item i of n at the top.
func restFor(i, n int) float64 { return -itemAngle(i, n) }

// itemPos is where item i of n is on the screen with the dial turned by rot.
func itemPos(i, n int, rot float64) (x, y float64) {
	a := itemAngle(i, n) + rot
	return center + dialR*math.Sin(a), center - dialR*math.Cos(a)
}

// topItem is the item of n nearest the top with the dial turned by rot.
func topItem(rot float64, n int) int {
	step := 2 * math.Pi / float64(n)
	i := int(math.Round(-rot/step)) % n
	if i < 0 {
		i += n
	}
	return i
}

// dialHitAt says what a tap at x, y is on: the middle, an item of n (its index), or neither.
func dialHitAt(x, y int, rot float64, n int) (item int, middle bool) {
	if math.Hypot(float64(x-center), float64(y-center)) < dialHub {
		return -1, true
	}
	best, bestD := -1, math.MaxFloat64
	for i := 0; i < n; i++ {
		ix, iy := itemPos(i, n, rot)
		if d := math.Hypot(float64(x)-ix, float64(y)-iy); d < dialHit && d < bestD {
			best, bestD = i, d
		}
	}
	return best, false
}

// fingerAngle is the direction of x, y from the center, clockwise from up.
func fingerAngle(x, y int) float64 { return math.Atan2(float64(x-center), -float64(y-center)) }

// wrapAngle brings a in (-π, π].
func wrapAngle(a float64) float64 {
	for a > math.Pi {
		a -= 2 * math.Pi
	}
	for a <= -math.Pi {
		a += 2 * math.Pi
	}
	return a
}

// nearestRest is the rotation for item i of n closest to the current one, so snapping never spins
// the long way round.
func nearestRest(rot float64, i, n int) float64 { return rot + wrapAngle(restFor(i, n)-rot) }

func itemName(s roundScene, id itemID) string {
	if i := cameraOf(id); i >= 0 {
		if i < len(s.cameras) {
			return s.cameras[i].Name
		}
		return "Camera"
	}
	switch id {
	case itemTalk:
		return "Talk"
	case itemCall:
		return "Call"
	case itemMute:
		if s.muted {
			return "Unmute"
		}
		return "Mute"
	case itemMusic:
		return "Music"
	case itemVolume:
		return "Volume"
	case itemWeather:
		return "Weather"
	case itemCalendar:
		return "Calendar"
	case itemCamera:
		return "Camera"
	case itemDashboard:
		if s.showDash {
			return "Clock"
		}
		return "Dashboard"
	case itemTimers:
		return "Timers"
	case itemAnnounce:
		return "Announce"
	case itemSettings:
		return "Settings"
	case itemSleep:
		return "Sleep"
	}
	return ""
}

func itemHint(s roundScene, id itemID) string {
	if i := cameraOf(id); i >= 0 {
		if i < len(s.cameras) && s.cameras[i].Entity == s.camera.Entity {
			return "showing now"
		}
		return "tap to show"
	}
	switch id {
	case itemTalk:
		return "tap to ask"
	case itemAnnounce:
		switch {
		case s.announceRecording:
			return "speak now"
		case !s.announceReady:
			return "needs a house word"
		case s.announcePeers > 0:
			return "heard on " + devicesText(s.announcePeers)
		}
		return "no other devices found"
	case itemCall:
		switch {
		case s.contactCount > 0:
			return fmt.Sprintf("%d to call", s.contactCount)
		case !s.phoneReady && !s.houseReady:
			return "needs a house word"
		}
		return "nobody to call yet"
	case itemMute:
		if s.muted {
			return "microphone is off"
		}
		return "microphone is on"
	case itemMusic:
		switch {
		case s.playing:
			return "playing · pick a station"
		case s.paused:
			return "paused · pick a station"
		}
		return "radio stations"
	case itemVolume:
		return fmt.Sprintf("%d · tap, then turn", s.volume)
	case itemCamera:
		if s.muted {
			return "off while muted"
		}
		return "this Spot · hold for the list"
	case itemWeather:
		if l := weatherLine(s.weather); l != "" {
			return l
		}
		return "forecast"
	case itemCalendar:
		switch {
		case len(s.calToday) > 0:
			return spotWhen(s.calToday[0], s.now) + " · " + s.calToday[0].Summary
		case len(s.calNext) > 0:
			return comingDay(s.calNext[0].Start, s.now) + " · " + s.calNext[0].Summary
		}
		return "nothing coming up"
	case itemTimers:
		switch {
		case s.timerRinging:
			return "ringing · tap to stop"
		case len(s.timers) == 1:
			return clockDuration(s.timers[0].Left) + " left"
		case len(s.timers) > 1:
			return fmt.Sprintf("%s left · %d timers", clockDuration(s.timers[0].Left), len(s.timers))
		}
		return "none running · ask to set one"
	case itemSettings:
		return "display, sound, alarms…"
	case itemDashboard:
		switch {
		case s.showDash:
			return "back to the clock"
		case dashboard.Get().Mode() == config.DashboardOff:
			return "turn it on in Home Assistant"
		}
		return "Home Assistant's"
	case itemSleep:
		return "tap the screen to wake"
	}
	return ""
}

// menu draws whatever the open menu is showing.
func (r *roundRenderer) menu(s roundScene) {
	switch {
	case s.menuMode.jogging():
		r.jog(s)
	case s.menuMode == modeWeather && s.radarOn:
		r.radarFace(s)
	case s.menuMode == modeCalendar:
		r.calendarFace(s)
	case s.menuMode == modeWeather:
		bolt := r.weatherFace(s)
		r.sky(s.sky, s.now, r.dst.Rect, bolt)
	case s.menuMode == modeRadio:
		r.radioList(s)
	case s.menuMode == modeContacts:
		r.contactList(s)
	default:
		r.dial(s)
	}
}

// dial draws the ring menu over a dimmed face.
func (r *roundRenderer) dial(s roundScene) {
	items := dialItems(s)
	n := len(items)
	r.dim(244)
	r.ringAt(center, center, dialR-1, dialR+1, 0, 2*math.Pi, color.RGBA{62, 68, 78, 255})

	for i, it := range items {
		if i == s.menuSel {
			continue
		}
		x, y := itemPos(i, n, s.menuRot)
		r.icon(it.id, s, x, y, 18, 2.6, colIcon)
	}
	if s.menuSel >= 0 && s.menuSel < n {
		it := items[s.menuSel]
		x, y := itemPos(s.menuSel, n, s.menuRot)
		r.discAt(x, y, chosenRing, colIconGround)
		r.ringAt(x, y, chosenRing-3.5, chosenRing, 0, 2*math.Pi, it.color)
		accent := it.color
		accent.A = 150
		r.ringAt(x, y, chosenRing+6, chosenRing+9, -0.3*math.Pi, 0.55*math.Pi, accent)
		r.icon(it.id, s, x, y, 22, 3.2, it.color)

		header := clockHM(s.now)
		switch s.menuMode {
		case modeCameras:
			header = "CAMERAS"
		}
		r.centered(r.label, header, 206, colDim)
		r.centered(r.title, itemName(s, it.id), 252, colText)
		r.centered(r.small, itemHint(s, it.id), 286, colDim)
	}
}

// jog draws the ring as a jog wheel for one value.
func (r *roundRenderer) jog(s roundScene) {
	r.clear()
	var title, value, hint string
	var frac float64
	col := color.RGBA{58, 160, 255, 255}
	switch s.menuMode {
	case modeVolume:
		title, value, hint = "VOLUME", fmt.Sprintf("%d", s.volume), "turn the ring · tap when done"
		if s.maxVolume > 0 {
			frac = float64(s.volume) / float64(s.maxVolume)
		}
	}
	frac = math.Min(math.Max(frac, 0), 1)
	// The value round the ring, from the bottom-left to the bottom-right, with a knob at its end.
	const from, span = 1.25 * math.Pi, 1.5 * math.Pi
	r.ringAt(center, center, 186, 200, from, from+span, color.RGBA{44, 50, 60, 255})
	r.ringAt(center, center, 186, 200, from, from+span*frac, col)
	kx, ky := center+193*math.Sin(from+span*frac), center-193*math.Cos(from+span*frac)
	r.discAt(kx, ky, 13, col)
	r.discAt(kx, ky, 6, colBackground)

	r.centered(r.label, title, 190, colDim)
	r.centered(r.clock, value, 282, colText)
	r.centered(r.small, hint, 330, colDim)
}

// icon draws one item's line icon, centered at x, y, u half its size, w the stroke width.
func (r *roundRenderer) icon(id itemID, s roundScene, x, y, u, w float64, c color.RGBA) {
	if i := cameraOf(id); i >= 0 {
		// A camera is its initials, so the ring says which is where before one is chosen.
		name := ""
		if i < len(s.cameras) {
			name = initials(s.cameras[i].Name)
		}
		if name == "" {
			id = itemCamera
		} else {
			face := r.label
			if u > 20 {
				face = r.title
			}
			r.ringAt(x, y, u-w/2, u+w/2, 0, 2*math.Pi, c)
			r.text(face, name, int(x)-r.width(face, name)/2, int(y)+int(u*0.38), c)
			return
		}
	}
	switch id {
	case itemTalk:
		r.micIcon(x, y, u, w, c)
	case itemCall:
		// A handset: its two ends joined by a curve.
		r.ringAt(x+0.25*u, y+0.25*u, 0.85*u-w/2, 0.85*u+w/2, 1.1*math.Pi, 1.9*math.Pi, c)
		r.line(x-0.75*u, y-0.05*u, x-0.35*u, y+0.45*u, w*2.4, c)
		r.line(x-0.05*u, y-0.75*u, x+0.45*u, y-0.35*u, w*2.4, c)
	case itemMute:
		r.micIcon(x, y, u, w, c)
		r.line(x-0.85*u, y-0.85*u, x+0.85*u, y+0.85*u, w, c)
	case itemMusic:
		r.notesMark(x, y, u, c)
	case itemVolume:
		r.speakerIcon(x-0.2*u, y, u*0.9, w, c)
		r.ringAt(x+0.05*u, y, 0.45*u-w/2, 0.45*u+w/2, 0.25*math.Pi, 0.75*math.Pi, c)
		r.ringAt(x+0.05*u, y, 0.85*u-w/2, 0.85*u+w/2, 0.25*math.Pi, 0.75*math.Pi, c)
	case itemCamera:
		// A camera body with its lens.
		r.line(x-0.9*u, y-0.45*u, x+0.9*u, y-0.45*u, w, c)
		r.line(x-0.9*u, y+0.65*u, x+0.9*u, y+0.65*u, w, c)
		r.line(x-0.9*u, y-0.45*u, x-0.9*u, y+0.65*u, w, c)
		r.line(x+0.9*u, y-0.45*u, x+0.9*u, y+0.65*u, w, c)
		r.line(x-0.35*u, y-0.45*u, x-0.2*u, y-0.72*u, w, c)
		r.line(x-0.2*u, y-0.72*u, x+0.2*u, y-0.72*u, w, c)
		r.line(x+0.2*u, y-0.72*u, x+0.35*u, y-0.45*u, w, c)
		r.ringAt(x, y+0.1*u, 0.36*u-w/2, 0.36*u+w/2, 0, 2*math.Pi, c)
	case itemDashboard:
		// Four tiles, as a dashboard is.
		for _, o := range [][2]float64{{-1, -1}, {1, -1}, {-1, 1}, {1, 1}} {
			cx, cy := x+o[0]*0.45*u, y+o[1]*0.45*u
			b := image.Rect(int(cx-0.33*u), int(cy-0.33*u), int(cx+0.33*u), int(cy+0.33*u))
			r.roundFill(b, 0.12*u, c, c)
		}
	case itemCalendar:
		// A page of a calendar: its frame, the band across the top, and the two rings it hangs from.
		r.line(x-0.75*u, y-0.55*u, x+0.75*u, y-0.55*u, w*1.8, c)
		r.line(x-0.75*u, y-0.55*u, x-0.75*u, y+0.75*u, w, c)
		r.line(x+0.75*u, y-0.55*u, x+0.75*u, y+0.75*u, w, c)
		r.line(x-0.75*u, y+0.75*u, x+0.75*u, y+0.75*u, w, c)
		r.line(x-0.4*u, y-0.9*u, x-0.4*u, y-0.45*u, w, c)
		r.line(x+0.4*u, y-0.9*u, x+0.4*u, y-0.45*u, w, c)
		for _, o := range [][2]float64{{-0.35, 0.05}, {0.05, 0.05}, {0.45, 0.05}, {-0.35, 0.42}, {0.05, 0.42}} {
			r.discAt(x+o[0]*u, y+o[1]*u, 0.1*u, c)
		}
	case itemWeather:
		r.sunIcon(x+0.38*u, y-0.32*u, 0.62*u, w*0.8, c)
		r.cloud(x-0.12*u, y+0.22*u, 1.02*u, colIconGround)
		r.cloud(x-0.12*u, y+0.22*u, 0.8*u, c)
	case itemTimers:
		r.line(x-0.6*u, y-0.9*u, x+0.6*u, y-0.9*u, w, c)
		r.line(x-0.6*u, y+0.9*u, x+0.6*u, y+0.9*u, w, c)
		r.line(x-0.5*u, y-0.9*u, x+0.5*u, y+0.9*u, w, c)
		r.line(x+0.5*u, y-0.9*u, x-0.5*u, y+0.9*u, w, c)
		r.triangle(x-0.3*u, y+0.85*u, x+0.3*u, y+0.85*u, x, y+0.35*u, c)
	case itemSettings:
		r.ringAt(x, y, 0.42*u-w/2, 0.42*u+w/2, 0, 2*math.Pi, c)
		for k := 0; k < 8; k++ {
			a := float64(k) * math.Pi / 4
			r.line(x+0.62*u*math.Sin(a), y-0.62*u*math.Cos(a), x+0.95*u*math.Sin(a), y-0.95*u*math.Cos(a), w*1.6, c)
		}
		r.ringAt(x, y, 0.62*u-w/2, 0.62*u+w/2, 0, 2*math.Pi, c)
	case itemAnnounce:
		// A mast putting something out. The mic belongs to Talk and Mute and the speaker to Volume,
		// so this is neither: announcing is the one thing here that leaves the device.
		r.line(x, y-0.46*u, x-0.42*u, y+0.92*u, w, c)
		r.line(x, y-0.46*u, x+0.42*u, y+0.92*u, w, c)
		r.line(x-0.26*u, y+0.38*u, x+0.26*u, y+0.38*u, w, c)
		r.ringAt(x, y-0.46*u, 0, 0.13*u, 0, 2*math.Pi, c)
		for _, rad := range []float64{0.5, 0.8} {
			r.ringAt(x, y-0.46*u, rad*u-w/2, rad*u+w/2, 1.68*math.Pi, 2.32*math.Pi, c)
		}
	case itemSleep:
		r.moonIcon(x, y, u, w, c)
	}
}

func (r *roundRenderer) micIcon(x, y, u, w float64, c color.RGBA) {
	hw := 0.34 * u // half the capsule's width
	top, bot := y-0.62*u, y+0.02*u
	r.ringAt(x, top, hw-w/2, hw+w/2, 1.5*math.Pi, 2.5*math.Pi, c) // upper half
	r.ringAt(x, bot, hw-w/2, hw+w/2, 0.5*math.Pi, 1.5*math.Pi, c) // lower half
	r.line(x-hw, top, x-hw, bot, w, c)
	r.line(x+hw, top, x+hw, bot, w, c)
	r.ringAt(x, y-0.1*u, 0.64*u-w/2, 0.64*u+w/2, 0.5*math.Pi, 1.5*math.Pi, c) // the holder
	r.line(x, y+0.54*u, x, y+0.86*u, w, c)
	r.line(x-0.36*u, y+0.86*u, x+0.36*u, y+0.86*u, w, c)
}

func (r *roundRenderer) speakerIcon(x, y, u, w float64, c color.RGBA) {
	bx0, bx1, by := x-0.95*u, x-0.5*u, 0.28*u
	r.line(bx0, y-by, bx1, y-by, w, c)
	r.line(bx0, y+by, bx1, y+by, w, c)
	r.line(bx0, y-by, bx0, y+by, w, c)
	r.line(bx1, y-by, x-0.02*u, y-0.72*u, w, c)
	r.line(bx1, y+by, x-0.02*u, y+0.72*u, w, c)
	r.line(x-0.02*u, y-0.72*u, x-0.02*u, y+0.72*u, w, c)
}

func (r *roundRenderer) sunIcon(x, y, u, w float64, c color.RGBA) {
	r.ringAt(x, y, 0.4*u-w/2, 0.4*u+w/2, 0, 2*math.Pi, c)
	for k := 0; k < 8; k++ {
		a := float64(k) * math.Pi / 4
		r.line(x+0.62*u*math.Sin(a), y-0.62*u*math.Cos(a), x+0.92*u*math.Sin(a), y-0.92*u*math.Cos(a), w, c)
	}
}

// moonIcon is a filled crescent: a disc with the background's color taken out of it up and to the
// right. Icons sit on a near-black ground, which is what bite is.
func (r *roundRenderer) moonIcon(x, y, u, w float64, c color.RGBA) {
	r.discAt(x, y, 0.8*u, c)
	r.discAt(x+0.42*u, y-0.34*u, 0.7*u, colIconGround)
}
