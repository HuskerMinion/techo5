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

// The drawer: a swipe in from the right edge brings Cameras and Radio over the clock, which dims
// behind it. They are things to use rather than settings, so they sit apart from the settings screen,
// drawn with its rows and controls.

const (
	drawerW = 560

	// drawerEdge is how far from the right edge a leftward swipe has to start to open the drawer.
	drawerEdge = 240

	drawerCameras = 0
	drawerRadio   = 1
)

var drawerTabs = []string{"Cameras", "Radio"}

func (r *renderer) drawer(s scene) {
	fc := faces()
	r.pending = r.pending[:0]
	r.dimAll(0.55)

	panel := image.Rect(r.w-cardIn-drawerW, cardIn, r.w-cardIn, r.h-cardIn)
	r.addZone(zone{r: image.Rect(0, 0, panel.Min.X-4, r.h), kind: zoneDone}) // the dimmed clock closes it
	r.roundShadow(panel, cardRad, 26, 8, shadowAlpha()*1.2)
	r.roundFill(panel, cardRad, surface(3), surface(2))
	r.roundHighlight(panel, cardRad)

	// Cameras | Radio, a segmented switch with the open one raised.
	seg := image.Rect(panel.Min.X+22, panel.Min.Y+18, panel.Min.X+22+320, panel.Min.Y+64)
	r.roundFill(seg, 23, surface(1), surface(1))
	r.roundStroke(seg, 23, 1, ember)
	half := seg.Dx() / 2
	for i, name := range drawerTabs {
		b := image.Rect(seg.Min.X+i*half+4, seg.Min.Y+4, seg.Min.X+(i+1)*half-4, seg.Max.Y-4)
		fg, face := lerp(dim, cream, 0.3), fc.nav
		if i == s.drawerTab {
			r.roundShadow(b, 19, 8, 3, shadowAlpha()*0.8)
			r.roundFill(b, 19, shift(amber, 14), shift(amber, -14))
			r.roundHighlight(b, 19)
			fg, face = onAccent(), fc.navBold
		}
		r.text(face, name, b.Min.X+(b.Dx()-r.width(face, name))/2, b.Min.Y+b.Dy()/2+9, fg)
		r.addZone(zone{r: image.Rect(seg.Min.X+i*half, seg.Min.Y-8, seg.Min.X+(i+1)*half, seg.Max.Y+8), kind: zoneTab, opt: i})
	}

	// Close, a round button with an ×.
	c := image.Rect(panel.Max.X-22-46, seg.Min.Y, panel.Max.X-22, seg.Max.Y)
	r.roundShadow(c, 23, 6, 2, shadowAlpha()*0.5)
	r.roundFill(c, 23, surface(7), surface(5))
	r.roundHighlight(c, 23)
	cx, cy := float64(c.Min.X+23), float64(c.Min.Y+23)
	r.aaLine(cx-7, cy-7, cx+7, cy+7, 2.6, cream)
	r.aaLine(cx-7, cy+7, cx+7, cy-7, 2.6, cream)
	r.addZone(zone{r: c.Inset(-10), kind: zoneDone})
	r.rule(panel.Min.X+22, panel.Max.X-22, panel.Min.Y+headerH-2, 1)

	list := image.Rect(panel.Min.X, panel.Min.Y+headerH, panel.Max.X, panel.Max.Y-8)
	rows, note := drawerRows(s)
	if len(rows) == 0 && note != "" {
		y := list.Min.Y + 50
		for _, line := range r.wrap(fc.value, note, panel.Dx()-2*rowIn) {
			r.text(fc.value, line, panel.Min.X+rowIn, y, dim)
			y += 36
		}
	}
	under := slices.Clone(r.dst.Pix)
	maxScroll := r.rowList(panel, list, rows, s.drawerScroll, surface(2), under)

	pickMax := 0
	if s.drawerPick != "" {
		if p, ok := pickerFor(s.drawerPick, s); ok {
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
	if s.drawerTab == drawerCameras {
		if len(s.cameras) == 0 {
			return nil, "No cameras yet. Add them with the home_cameras action in Home Assistant."
		}
		var rows []settingRow
		for i, c := range s.cameras {
			rows = append(rows, settingRow{id: "cam:" + strconv.Itoa(i), label: c.Name,
				sub: "Or say \"show " + strings.ToLower(c.Name) + "\"", kind: ctlButton, button: "Show", rowTap: true})
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
			rows = append(rows, settingRow{id: id, label: "Stop the radio", kind: ctlButton, button: "Stop", rowTap: true})
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
