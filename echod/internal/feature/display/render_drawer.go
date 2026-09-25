//go:build !dot && !spot

package display

import (
	"image"
	"slices"
	"strconv"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// The drawer: a swipe in from the right edge brings Cameras, Radio, Announce and Call over the clock,
// which dims behind it. They are things to use rather than settings, so they sit apart from the settings screen,
// drawn with its rows and controls.

// The lengths here are the ones this drawer was drawn at on a Show 5, and they go through paint.s
// so the panel it lands on decides the real size. The tab strip in particular was a fixed 400 wide
// while its labels scaled with the type, so on a Show 8 "Cameras" and "Announce" grew out of their
// pills.
const (
	drawerWBase = 620

	// drawerEdgeBase is how far from the right edge a leftward swipe has to start to open the drawer.
	drawerEdgeBase = 240

	// drawerSegBase is the width of the Cameras/Radio/Announce/Call strip.
	drawerSegBase = 520 // wide enough for "Announce" when it is the chosen one

	drawerCameras  = 0
	drawerRadio    = 1
	drawerAnnounce = 2
	drawerCall     = 3
)

func (p *paint) drawerW() int    { return p.s(drawerWBase) }
func (p *paint) drawerEdge() int { return p.s(drawerEdgeBase) }
func (p *paint) drawerSeg() int  { return p.s(drawerSegBase) }

var drawerTabs = []string{"Cameras", "Radio", "Announce", "Call"}

func (r *renderer) drawer(s scene) {
	fc := r.faces()
	r.pending = r.pending[:0]
	r.dimAll(0.55)

	panel := image.Rect(r.w-r.cardIn()-r.drawerW(), r.cardIn(), r.w-r.cardIn(), r.h-r.cardIn())
	r.addZone(zone{r: image.Rect(0, 0, panel.Min.X-r.s(4), r.h), kind: zoneDone}) // the dimmed clock closes it
	r.roundShadow(panel, r.cardRad(), 26, 8, shadowAlpha()*1.2)
	r.roundFill(panel, r.cardRad(), surface(3), surface(2))
	r.roundHighlight(panel, r.cardRad())

	// Cameras | Radio, a segmented switch with the open one raised.
	pad := r.s(22)
	seg := image.Rect(panel.Min.X+pad, panel.Min.Y+r.s(18), panel.Min.X+pad+r.drawerSeg(), panel.Min.Y+r.s(64))
	segRad := r.sf(23)
	r.roundFill(seg, segRad, surface(1), surface(1))
	r.roundStroke(seg, segRad, r.sf(1), ember)
	half := seg.Dx() / len(drawerTabs)
	inset, tabRad := r.s(4), r.sf(19)
	for i, name := range drawerTabs {
		b := image.Rect(seg.Min.X+i*half+inset, seg.Min.Y+inset, seg.Min.X+(i+1)*half-inset, seg.Max.Y-inset)
		fg, face := lerp(dim, cream, 0.3), fc.nav
		if i == s.drawerTab {
			r.roundShadow(b, tabRad, r.sf(8), r.s(3), shadowAlpha()*0.8)
			r.roundFill(b, tabRad, shift(amber, 14), shift(amber, -14))
			r.roundHighlight(b, tabRad)
			fg, face = onAccent(), fc.navBold
		}
		// No shrinking to fit here: the strip scales with the type, so a name that fits its pill on
		// one panel fits it on the other. Shrinking instead of scaling was what made these look wrong.
		r.text(face, name, b.Min.X+(b.Dx()-r.width(face, name))/2, b.Min.Y+b.Dy()/2+r.s(9), fg)
		r.addZone(zone{r: image.Rect(seg.Min.X+i*half, seg.Min.Y-r.s(8), seg.Min.X+(i+1)*half, seg.Max.Y+r.s(8)), kind: zoneTab, opt: i})
	}

	// Close, a round button with an ×.
	side := r.s(46)
	c := image.Rect(panel.Max.X-pad-side, seg.Min.Y, panel.Max.X-pad, seg.Max.Y)
	r.roundShadow(c, segRad, r.sf(6), r.s(2), shadowAlpha()*0.5)
	r.roundFill(c, segRad, surface(7), surface(5))
	r.roundHighlight(c, segRad)
	cx, cy := float64(c.Min.X)+float64(c.Dx())/2, float64(c.Min.Y)+float64(c.Dy())/2
	arm, pen := r.sf(7), r.sf(26)/10 // 2.6 at the size this was drawn for
	r.aaLine(cx-arm, cy-arm, cx+arm, cy+arm, pen, cream)
	r.aaLine(cx-arm, cy+arm, cx+arm, cy-arm, pen, cream)
	r.addZone(zone{r: c.Inset(-r.s(10)), kind: zoneDone})
	r.rule(panel.Min.X+pad, panel.Max.X-pad, panel.Min.Y+r.headerH()-r.s(2), 1)

	list := image.Rect(panel.Min.X, panel.Min.Y+r.headerH(), panel.Max.X, panel.Max.Y-r.s(8))
	rows, note := drawerRows(s)
	if len(rows) == 0 && note != "" {
		y := list.Min.Y + r.s(50)
		for _, line := range r.wrap(fc.value, note, panel.Dx()-2*r.rowIn()) {
			r.text(fc.value, line, panel.Min.X+r.rowIn(), y, dim)
			y += r.s(36)
		}
	}
	under := slices.Clone(r.dst.Pix)
	maxScroll := r.rowList(panel, list, rows, s.drawerScroll, surface(2), under)

	pickMax := 0
	if s.drawerPick != "" {
		if p, ok := pickerFor(s.drawerPick, s.view()); ok {
			pickMax = r.picker(p, s.pickScroll)
		}
	}

	r.zmu.Lock()
	r.zones, r.pending = r.pending, r.zones
	r.cardMax, r.pickMax = maxScroll, pickMax
	r.zmu.Unlock()
}

// drawerRows are the open tab's rows, and a note to show when it has none.
func drawerRows(s scene) ([]settingRow, string) {
	if s.drawerTab == drawerAnnounce {
		return announceRows(s)
	}
	if s.drawerTab == drawerCall {
		return callRows(s)
	}
	if s.drawerTab == drawerCameras {
		if len(s.cameras) == 0 {
			return nil, "No cameras yet. Set a Home Assistant token to list them all, or pick some with the home_cameras action."
		}
		var rows []settingRow
		for i, c := range s.cameras {
			name := c.Name
			if s.demo {
				// A camera's name can say whose room it looks into.
				name = demoCameras[i%len(demoCameras)]
			}
			rows = append(rows, settingRow{id: "cam:" + strconv.Itoa(i), label: name,
				sub: "Or say \"show " + strings.ToLower(name) + "\"", kind: ctlButton, button: "Show", rowTap: true})
		}
		return rows, ""
	}

	rd := s.radio
	if !rd.Configured {
		return nil, "No stations yet. Give the device a Home Assistant token (the home_assistant action) " +
			"for local and popular stations, or add your own with home_radio."
	}
	source := settingRow{label: "Stations", kind: ctlValue, value: home.SourceLabel(rd.Source)}
	if rd.Sources > 1 {
		source.id, source.kind = "radiosource", ctlChoice
	}
	rows := []settingRow{source}
	stations := radioList(rd)
	switch {
	case rd.Loading:
		return append(rows, settingRow{label: "Asking Home Assistant for stations…", kind: ctlValue}), ""
	case len(stations) == 0 && rd.Problem != "":
		return append(rows, settingRow{label: "Could not list the stations", sub: rd.Problem, kind: ctlValue}), ""
	case len(stations) == 0 && rd.Source == config.RadioLocal:
		return append(rows, settingRow{label: "No stations within 100 km", sub: "Try Popular worldwide", kind: ctlValue}), ""
	case len(stations) == 0:
		return append(rows, settingRow{label: "No stations in this list yet", kind: ctlValue}), ""
	}
	for i, name := range stations {
		id := "st:" + strconv.Itoa(i)
		if name == "■ Stop" {
			// A stop in a grouped room stops the group, so the row says so rather than doing something
			// bigger than its words. The label stays what it is: what changes is who else it reaches.
			row := settingRow{id: id, label: "Stop the radio", kind: ctlButton, button: "Stop", rowTap: true}
			if rd.Grouped {
				row.sub = "all rooms in the group"
			}
			rows = append(rows, row)
			continue
		}
		label := stationLabel(name)
		if s.demo {
			// Call letters and local station names say where the owner lives.
			label = "Station " + strconv.Itoa(i+1)
		}
		row := settingRow{id: id, label: label, kind: ctlButton, button: "Play", rowTap: true}
		switch {
		case name == rd.Now && rd.Playing:
			row.bold, row.sub, row.button = true, "Playing now", "Playing"
		case name == rd.Chosen && rd.Chosen != rd.Now:
			row.sub, row.button = "Starting…", "Starting"
		}
		rows = append(rows, row)
	}
	return rows, ""
}

// callRows is the Call tab: the other devices in the house, then the phone's contacts.
func callRows(s scene) ([]settingRow, string) {
	if len(s.callees) == 0 {
		return nil, "Nobody to call yet. Give every device the same house word on its setup page to call " +
			"between rooms, or sign the phone in (the phone_account action) for your contacts."
	}
	var rows []settingRow
	for i, c := range s.callees {
		name, sub := c.Name, "Phone"
		if c.Device {
			sub = "Intercom, in the house"
		}
		if s.demo {
			// A device's name can say whose room it is, and a contact's is somebody's name.
			if c.Device {
				name = demoRooms[i%len(demoRooms)]
			} else {
				name = "Contact " + strconv.Itoa(i+1)
			}
		}
		rows = append(rows, settingRow{id: "call:" + strconv.Itoa(i), label: name, sub: sub,
			kind: ctlButton, button: "Call", rowTap: true})
	}
	return rows, ""
}

// demoRooms stand in for the devices' names in screenshots that will be published.
var demoRooms = []string{"Kitchen", "Office", "Living room", "Bedroom", "Garage"}

// demoCameras stand in for the owner's camera names in screenshots that will be published.
var demoCameras = []string{"Front door", "Driveway", "Backyard", "Porch", "Garage"}
