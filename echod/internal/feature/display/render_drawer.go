//go:build !dot && !spot

package display

import (
	"fmt"
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

	// drawerEdgeBase is how far from the right edge a leftward swipe has to start to open the drawer,
	// and from the left edge a rightward one to bring the dashboard up. It was 240, a quarter of the
	// Show 5's width, and a swipe across the clock meant for its style kept opening one or the other
	// (#113); an eighth still catches a swipe that starts on the bezel.
	drawerEdgeBase = 120

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

func drawerTabLabels() []string {
	return []string{tr("cameras"), tr("drawer_radio"), tr("drawer_announce"), tr("drawer_call")}
}

// shownTabs are the tabs this screen offers: no Announce or Call tab where there is no microphone to
// talk with.
func shownTabs() []string {
	if !hasMicrophone() {
		return drawerTabs[:drawerAnnounce]
	}
	return drawerTabs
}

func (r *renderer) drawer(s scene) {
	fc := r.faces()
	r.pending = r.pending[:0]
	r.dimAll(0.55)

	panel := image.Rect(r.w-r.cardIn()-r.drawerW(), r.cardIn(), r.w-r.cardIn(), r.h-r.cardIn())
	r.addZone(zone{r: image.Rect(0, 0, panel.Min.X-r.s(4), r.h), kind: zoneDone}) // the dimmed clock closes it
	r.roundShadow(panel, r.cardRad(), r.sf(26), r.s(8), shadowAlpha()*1.2)
	r.roundFill(panel, r.cardRad(), surface(3), surface(2))
	r.roundHighlight(panel, r.cardRad())

	// Cameras | Radio, a segmented switch with the open one raised.
	pad := r.s(22)
	seg := image.Rect(panel.Min.X+pad, panel.Min.Y+r.s(18), panel.Min.X+pad+r.drawerSeg(), panel.Min.Y+r.s(64))
	segRad := r.sf(23)
	r.roundFill(seg, segRad, surface(1), surface(1))
	r.roundStroke(seg, segRad, r.sf(1), ember)
	half := seg.Dx() / len(shownTabs())
	inset, tabRad := r.s(4), r.sf(19)
	for i, name := range drawerTabLabels()[:len(shownTabs())] {
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
		for _, line := range r.wrapLines(fc.value, note, panel.Dx()-2*r.rowIn()) {
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
			if config.Get().Home.NoHACameras() {
				return nil, tr("drawer_no_cameras_disabled")
			}
			return nil, tr("drawer_no_cameras")
		}
		var rows []settingRow
		// Several cameras at once, when there are several to show.
		others := 0
		for _, c := range s.cameras {
			if c.Entity != home.LocalCamera {
				others++
			}
		}
		if others >= 2 {
			rows = append(rows, settingRow{id: "grid:0", label: tr("drawer_all_cameras"),
				sub: tr("drawer_camera_grid_hint"), kind: ctlButton, button: tr("ui_show"), rowTap: true})
		}
		for i, c := range s.cameras {
			name := c.Name
			if s.demo {
				// A camera's name can say whose room it looks into.
				name = demoCameras[i%len(demoCameras)]
			}
			rows = append(rows, settingRow{id: "cam:" + strconv.Itoa(i), label: name,
				sub: fmt.Sprintf(tr("drawer_show_camera"), strings.ToLower(name)), kind: ctlButton, button: tr("ui_show"), rowTap: true})
		}
		return rows, ""
	}

	rd := s.radio
	if !rd.Configured {
		return nil, tr("drawer_no_radio")
	}
	source := settingRow{label: tr("ui_stations"), kind: ctlValue, value: radioSourceText(rd.Source)}
	if rd.Sources > 1 {
		source.id, source.kind = "radiosource", ctlChoice
	}
	rows := []settingRow{source}
	stations := radioList(rd)
	switch {
	case rd.Loading:
		return append(rows, settingRow{label: tr("drawer_loading_stations"), kind: ctlValue}), ""
	case len(stations) == 0 && rd.Problem != "":
		return append(rows, settingRow{label: tr("drawer_station_error"), sub: rd.Problem, kind: ctlValue}), ""
	case len(stations) == 0 && rd.Source == config.RadioLocal:
		return append(rows, settingRow{label: tr("drawer_no_local"), sub: tr("drawer_try_popular"), kind: ctlValue}), ""
	case len(stations) == 0:
		return append(rows, settingRow{label: tr("drawer_empty_stations"), kind: ctlValue}), ""
	}
	for i, name := range stations {
		id := "st:" + strconv.Itoa(i)
		if name == "■ Stop" {
			// A stop in a grouped room stops the group, so the row says so rather than doing something
			// bigger than its words. The label stays what it is: what changes is who else it reaches.
			row := settingRow{id: id, label: tr("drawer_stop_radio"), kind: ctlButton, button: tr("stop"), rowTap: true}
			if rd.Grouped {
				row.sub = tr("drawer_group_stop")
			}
			rows = append(rows, row)
			continue
		}
		label := stationLabel(name)
		if s.demo {
			// Call letters and local station names say where the owner lives.
			label = "Station " + strconv.Itoa(i+1)
		}
		row := settingRow{id: id, label: label, kind: ctlButton, button: tr("play"), rowTap: true}
		switch {
		case name == rd.Now && rd.Playing:
			row.bold, row.sub, row.button = true, tr("drawer_playing_now"), tr("drawer_playing")
		case name == rd.Chosen && rd.Chosen != rd.Now:
			row.sub, row.button = tr("ui_starting"), tr("ui_starting_variant")
		}
		rows = append(rows, row)
	}
	return rows, ""
}

// callRows is the Call tab: the other devices in the house, then the phone's contacts.
func callRows(s scene) ([]settingRow, string) {
	if len(s.callees) == 0 {
		return nil, tr("drawer_no_calls")
	}
	var rows []settingRow
	for i, c := range s.callees {
		name, sub := c.Name, tr("drawer_phone")
		if c.Device {
			sub = tr("drawer_intercom")
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
			kind: ctlButton, button: tr("drawer_call"), rowTap: true})
	}
	return rows, ""
}

// demoRooms stand in for the devices' names in screenshots that will be published.
var demoRooms = []string{"Kitchen", "Office", "Living room", "Bedroom", "Garage"}

// demoCameras stand in for the owner's camera names in screenshots that will be published.
var demoCameras = []string{"Front door", "Driveway", "Backyard", "Porch", "Garage"}

// radioSourceText is presentation only; source IDs and HA options stay canonical.
func radioSourceText(source string) string {
	switch source {
	case config.RadioFavorites:
		return tr("drawer_favorites")
	case config.RadioLocal:
		return tr("drawer_local")
	case config.RadioPopular:
		return tr("drawer_popular")
	case config.RadioOwn:
		return tr("ui_on_this_device")
	}
	return home.SourceLabel(source)
}
