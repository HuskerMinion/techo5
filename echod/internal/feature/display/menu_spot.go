//go:build spot

package display

import (
	"fmt"
	"image/color"
	"math"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// The ring menu is a dial: the items sit on a circle, the one at the top is chosen, drawn larger in
// its own colour, and named in the middle. While it is open a finger dragged round the ring turns it;
// letting go snaps it to the nearest item; a tap on an item does it, and a tap in the middle does the
// one at the top.
//
// Some items turn the whole ring into a jog wheel for a value (volume, brightness, the night hours):
// turning it clockwise raises the value a step per jogStep, a tap finishes.

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
	modeSettings
	modeVolume
	modeBrightness
	modeNightFrom
	modeNightTo
	modeInfo
	modeWeather
	modeBluetooth
	modeRadio
	modeCameras
	modeContacts
)

// jogging is whether the mode turns the ring into a jog wheel.
func (m menuMode) jogging() bool {
	return m == modeVolume || m == modeBrightness || m == modeNightFrom || m == modeNightTo
}

type itemID string

const (
	itemTalk       itemID = "talk"
	itemCall       itemID = "call"
	itemMute       itemID = "mute"
	itemMusic      itemID = "music"
	itemVolume     itemID = "volume"
	itemWeather    itemID = "weather"
	itemCamera     itemID = "camera"
	itemTimers     itemID = "timers"
	itemSettings   itemID = "settings"
	itemSleep      itemID = "sleep"
	itemBrightness itemID = "brightness"
	itemNight      itemID = "night"
	itemAuto       itemID = "auto_brightness"
	itemInfo       itemID = "info"
	itemRestart    itemID = "restart"
	itemBack       itemID = "back"
	itemBluetooth  itemID = "bluetooth"
	itemBTPair     itemID = "bt_pair"
	itemBTConnect  itemID = "bt_connect"
	itemBTForget   itemID = "bt_forget"
)

type menuItem struct {
	id     itemID
	colour color.RGBA
}

// mainItems and settingsItems go clockwise from the top.
var mainItems = []menuItem{
	{itemTalk, color.RGBA{240, 98, 146, 255}},
	{itemCall, color.RGBA{46, 204, 113, 255}},
	{itemMute, color.RGBA{229, 72, 77, 255}},
	{itemMusic, color.RGBA{60, 203, 127, 255}},
	{itemVolume, color.RGBA{58, 160, 255, 255}},
	{itemWeather, color.RGBA{255, 196, 64, 255}},
	{itemCamera, color.RGBA{60, 203, 127, 255}},
	{itemTimers, color.RGBA{255, 176, 32, 255}},
	{itemSettings, color.RGBA{176, 150, 255, 255}},
	{itemSleep, color.RGBA{120, 140, 255, 255}},
}

var settingsItems = []menuItem{
	{itemBrightness, color.RGBA{255, 204, 64, 255}},
	{itemNight, color.RGBA{150, 120, 255, 255}},
	{itemBluetooth, colBluetooth},
	{itemInfo, color.RGBA{58, 160, 255, 255}},
	{itemRestart, color.RGBA{255, 120, 80, 255}},
	{itemBack, color.RGBA{176, 184, 196, 255}},
}

// colBluetooth is Bluetooth's blue: its dial's items and the rim while pairing.
var colBluetooth = color.RGBA{0, 130, 252, 255}

var bluetoothItems = []menuItem{
	{itemBTPair, colBluetooth},
	{itemBTConnect, color.RGBA{60, 203, 127, 255}},
	{itemBTForget, color.RGBA{229, 72, 77, 255}},
	{itemBack, color.RGBA{176, 184, 196, 255}},
}

// itemsFor is the dial a mode shows; nil for a mode that is not a dial.
func itemsFor(m menuMode) []menuItem {
	switch m {
	case modeMain:
		return mainItems
	case modeSettings:
		return settingsItems
	case modeBluetooth:
		return bluetoothItems
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
	return centre + dialR*math.Sin(a), centre - dialR*math.Cos(a)
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
	if math.Hypot(float64(x-centre), float64(y-centre)) < dialHub {
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

// fingerAngle is the direction of x, y from the centre, clockwise from up.
func fingerAngle(x, y int) float64 { return math.Atan2(float64(x-centre), -float64(y-centre)) }

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
	case itemCamera:
		return "Camera"
	case itemTimers:
		return "Timers"
	case itemSettings:
		return "Settings"
	case itemSleep:
		return "Sleep"
	case itemBrightness:
		return "Brightness"
	case itemNight:
		return "Night"
	case itemAuto:
		return "Auto brightness"
	case itemInfo:
		return "Info"
	case itemRestart:
		return "Restart"
	case itemBack:
		return "Back"
	case itemBluetooth:
		return "Bluetooth"
	case itemBTPair:
		if s.btPairing {
			return "Stop pairing"
		}
		return "Pair"
	case itemBTConnect:
		if s.btConnected != "" {
			return "Disconnect"
		}
		return "Connect"
	case itemBTForget:
		return "Forget"
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
	case itemCall:
		switch {
		case !s.phoneReady:
			return "phone not set up"
		case s.contactCount == 0:
			return "no contacts yet"
		}
		return fmt.Sprintf("%d contacts", s.contactCount)
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
		return "brightness, Bluetooth, info"
	case itemSleep:
		return "tap the screen to wake"
	case itemBrightness:
		if s.autoOn {
			return fmt.Sprintf("auto, up to %d%%", s.brightness)
		}
		return fmt.Sprintf("%d%% · tap, then turn", s.brightness)
	case itemNight:
		return fmt.Sprintf("%d:00 to %d:00", s.nightFrom, s.nightTo)
	case itemAuto:
		if s.autoOn {
			return "on · follows the room"
		}
		return "off"
	case itemInfo:
		return "name, address, version"
	case itemRestart:
		if s.restartArmed {
			return "tap again to restart"
		}
		return "tap twice"
	case itemBack:
		if s.menuMode == modeBluetooth {
			return "settings"
		}
		return "main menu"
	case itemBluetooth:
		switch {
		case !s.btAvailable:
			return "not available"
		case s.btPairing:
			return "pairing…"
		case s.btConnected != "":
			return s.btConnected
		case s.btRemembered != "":
			return "not connected"
		}
		return "earbuds or a speaker"
	case itemBTPair:
		switch {
		case !s.btAvailable:
			return "Bluetooth is not up"
		case s.btPairing && s.btStatus != "":
			return s.btStatus
		case s.btPairing:
			return "pairing…"
		}
		return "put the speaker in pairing mode"
	case itemBTConnect:
		switch {
		case s.btConnected != "":
			return s.btConnected
		case s.btRemembered != "":
			return s.btRemembered
		}
		return "nothing paired yet"
	case itemBTForget:
		switch {
		case s.btRemembered == "":
			return "nothing to forget"
		case s.forgetArmed:
			return "tap again to forget " + s.btRemembered
		}
		return s.btRemembered + " · tap twice"
	}
	return ""
}

// menu draws whatever the open menu is showing.
func (r *roundRenderer) menu(s roundScene) {
	switch {
	case s.menuMode.jogging():
		r.jog(s)
	case s.menuMode == modeInfo:
		r.info(s)
	case s.menuMode == modeWeather && s.radarOn:
		r.radarFace(s)
	case s.menuMode == modeWeather:
		r.weatherFace(s)
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
	r.ringAt(centre, centre, dialR-1, dialR+1, 0, 2*math.Pi, color.RGBA{62, 68, 78, 255})

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
		r.ringAt(x, y, chosenRing-3.5, chosenRing, 0, 2*math.Pi, it.colour)
		accent := it.colour
		accent.A = 150
		r.ringAt(x, y, chosenRing+6, chosenRing+9, -0.3*math.Pi, 0.55*math.Pi, accent)
		r.icon(it.id, s, x, y, 22, 3.2, it.colour)

		header := clockHM(s.now)
		switch s.menuMode {
		case modeSettings:
			header = "SETTINGS"
		case modeBluetooth:
			header = "BLUETOOTH"
		case modeCameras:
			header = "CAMERAS"
		}
		r.centred(r.label, header, 206, colDim)
		r.centred(r.title, itemName(s, it.id), 252, colText)
		r.centred(r.small, itemHint(s, it.id), 286, colDim)
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
	case modeBrightness:
		title, value, hint = "BRIGHTNESS", fmt.Sprintf("%d%%", s.brightness), "turn · tap the middle when done"
		if s.autoOn {
			title = "BRIGHTNESS · AUTO UP TO"
		}
		frac, col = float64(s.brightness)/100, color.RGBA{255, 204, 64, 255}
	case modeNightFrom:
		title, value, hint = "NIGHT STARTS", fmt.Sprintf("%d:00", s.nightFrom), "turn the ring · tap for the end"
		frac, col = float64(s.nightFrom)/24, color.RGBA{150, 120, 255, 255}
	case modeNightTo:
		title, value, hint = "NIGHT ENDS", fmt.Sprintf("%d:00", s.nightTo), "turn the ring · tap to save"
		frac, col = float64(s.nightTo)/24, color.RGBA{150, 120, 255, 255}
	}
	frac = math.Min(math.Max(frac, 0), 1)
	// The value round the ring, from the bottom-left to the bottom-right, with a knob at its end.
	const from, span = 1.25 * math.Pi, 1.5 * math.Pi
	r.ringAt(centre, centre, 186, 200, from, from+span, color.RGBA{44, 50, 60, 255})
	r.ringAt(centre, centre, 186, 200, from, from+span*frac, col)
	kx, ky := centre+193*math.Sin(from+span*frac), centre-193*math.Cos(from+span*frac)
	r.discAt(kx, ky, 13, col)
	r.discAt(kx, ky, 6, colBackground)

	r.centred(r.label, title, 190, colDim)
	r.centred(r.clock, value, 282, colText)
	r.centred(r.small, hint, 330, colDim)
	if s.menuMode == modeBrightness {
		r.autoBox(s.autoOn)
	}
}

// autoBox is the brightness wheel's Auto check box: on, the screen follows the room up to the
// brightness set on the wheel.
func (r *roundRenderer) autoBox(on bool) {
	const label = "Auto"
	lw := r.width(r.title, label)
	box := 30.0
	x0 := float64(centre) - (box+14+float64(lw))/2
	y0 := float64(autoBoxY) - box/2
	col := color.RGBA{64, 214, 230, 255}
	if on {
		r.line(x0, y0+box/2, x0+box, y0+box/2, box, col)
		r.line(x0+7, y0+16, x0+13, y0+23, 4, colBackground)
		r.line(x0+13, y0+23, x0+24, y0+8, 4, colBackground)
	} else {
		r.line(x0, y0+1.5, x0+box, y0+1.5, 3, colDim)
		r.line(x0, y0+box-1.5, x0+box, y0+box-1.5, 3, colDim)
		r.line(x0+1.5, y0, x0+1.5, y0+box, 3, colDim)
		r.line(x0+box-1.5, y0, x0+box-1.5, y0+box, 3, colDim)
	}
	r.text(r.title, label, int(x0+box+14), autoBoxY+12, colText)
}

// autoBoxY is where the Auto check box sits on the brightness wheel; a tap near it toggles it.
const autoBoxY = 392

// onAutoBox is whether a tap at x, y is on the Auto check box.
func onAutoBox(x, y int) bool { return y > autoBoxY-40 && y < autoBoxY+40 && x > centre-110 && x < centre+110 }

// info names the device and says where it is on the network.
func (r *roundRenderer) info(s roundScene) {
	r.clear()
	r.icon(itemInfo, s, centre, 128, 22, 3.2, color.RGBA{58, 160, 255, 255})
	r.centred(r.title, s.infoName, 210, colText)
	r.centred(r.body, s.infoAddress, 252, colText)
	r.centred(r.small, s.infoVersion, 290, colDim)
	r.centred(r.small, "tap to go back", 352, colDim)
}

// icon draws one item's line icon, centred at x, y, u half its size, w the stroke width.
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
	case itemSleep, itemNight:
		r.moonIcon(x, y, u, w, c)
	case itemBrightness:
		r.sunIcon(x, y, u, w, c)
	case itemAuto:
		r.sunIcon(x, y, u, w, c)
		fw := r.width(r.label, "A")
		r.text(r.label, "A", int(x)-fw/2, int(y)+7, c)
	case itemInfo:
		r.ringAt(x, y, 0.85*u-w/2, 0.85*u+w/2, 0, 2*math.Pi, c)
		r.discAt(x, y-0.42*u, w*0.8, c)
		r.line(x, y-0.1*u, x, y+0.45*u, w, c)
	case itemRestart:
		r.ringAt(x, y, 0.7*u-w/2, 0.7*u+w/2, 0.15*math.Pi, 1.8*math.Pi, c)
		ex, ey := x+0.7*u*math.Sin(1.8*math.Pi), y-0.7*u*math.Cos(1.8*math.Pi)
		r.line(ex, ey, ex+0.42*u, ey-0.05*u, w, c)
		r.line(ex, ey, ex+0.05*u, ey+0.42*u, w, c)
	case itemBluetooth:
		r.btRune(x, y, u, w, c)
	case itemBTPair:
		r.btRune(x-0.3*u, y, 0.85*u, w, c)
		r.ringAt(x+0.05*u, y, 0.55*u-w/2, 0.55*u+w/2, 0.3*math.Pi, 0.7*math.Pi, c)
		r.ringAt(x+0.05*u, y, 0.95*u-w/2, 0.95*u+w/2, 0.3*math.Pi, 0.7*math.Pi, c)
	case itemBTConnect:
		// Headphones: the band and two cups.
		r.ringAt(x, y+0.1*u, 0.75*u-w/2, 0.75*u+w/2, 1.5*math.Pi, 2.5*math.Pi, c)
		r.line(x-0.75*u, y+0.1*u, x-0.75*u, y+0.45*u, w, c)
		r.line(x+0.75*u, y+0.1*u, x+0.75*u, y+0.45*u, w, c)
		r.line(x-0.62*u, y+0.25*u, x-0.62*u, y+0.8*u, w*2.2, c)
		r.line(x+0.62*u, y+0.25*u, x+0.62*u, y+0.8*u, w*2.2, c)
		if s.btConnected != "" {
			r.line(x-0.9*u, y-0.8*u, x+0.9*u, y+0.95*u, w, c)
		}
	case itemBTForget:
		r.ringAt(x, y, 0.8*u-w/2, 0.8*u+w/2, 0, 2*math.Pi, c)
		r.line(x-0.4*u, y-0.4*u, x+0.4*u, y+0.4*u, w, c)
		r.line(x+0.4*u, y-0.4*u, x-0.4*u, y+0.4*u, w, c)
	case itemBack:
		r.line(x+0.75*u, y, x-0.7*u, y, w, c)
		r.line(x-0.7*u, y, x-0.2*u, y-0.5*u, w, c)
		r.line(x-0.7*u, y, x-0.2*u, y+0.5*u, w, c)
	}
}

// btRune is the Bluetooth mark.
func (r *roundRenderer) btRune(x, y, u, w float64, c color.RGBA) {
	top, bot, mid := y-0.9*u, y+0.9*u, 0.45*u
	r.line(x, top, x, bot, w, c)
	r.line(x, top, x+0.5*u, y-mid, w, c)
	r.line(x+0.5*u, y-mid, x-0.5*u, y+mid, w, c)
	r.line(x, bot, x+0.5*u, y+mid, w, c)
	r.line(x+0.5*u, y+mid, x-0.5*u, y-mid, w, c)
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

// moonIcon is a filled crescent: a disc with the background's colour taken out of it up and to the
// right. Icons sit on a near-black ground, which is what bite is.
func (r *roundRenderer) moonIcon(x, y, u, w float64, c color.RGBA) {
	r.discAt(x, y, 0.8*u, c)
	r.discAt(x+0.42*u, y-0.34*u, 0.7*u, colIconGround)
}
