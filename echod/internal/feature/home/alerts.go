package home

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Weather alerts from the U.S. National Weather Service (api.weather.gov, public domain): the ones in
// force at home, for the clock's badge and the pills over the rain map, and the ones nearby with their
// shapes, outlined on the map. Only in the U.S., only on a device with a screen.
//
// A warning drawn around a storm (tornado, severe thunderstorm, flash flood) comes with its own
// polygon. The rest (wind, heat, winter, fog...) name the forecast zones or counties they cover, whose
// shapes are asked for once and kept on disk, since they never change. Those are fetched for the
// alerts at home and for any severe one nearby; a minor alert two counties over is left off the map
// rather than costing a dozen fetches.
//
// Most live alerts are "Update" messages to an earlier one; they count, and a "Cancel" does not.

// Alert is one alert as the screen shows it.
type Alert struct {
	ID          string
	Event       string // "Wind Advisory"
	Severity    string // Extreme, Severe, Moderate, Minor, Unknown
	Sender      string // "NWS Mount Holly NJ"
	Area        string // the counties or zones, as the NWS lists them
	Description string
	Instruction string
	Onset       time.Time // when the event begins; zero when it already has, or the NWS does not say
	Ends        time.Time // when the event ends; zero when the NWS does not say
	Gone        time.Time // when the alert is over for the screen: Ends, else when this message runs out
	Color       color.RGBA
	Here        bool           // in force at home
	Storm       bool           // drawn around a storm, rather than whole zones
	Rings       [][][2]float64 // its outline(s), longitude and latitude; none when not drawn
}

// AlertView is what the screen shows: the alerts in force at home, most severe first (an update and
// the alert it replaces count once), and every alert nearby with a shape to outline, home's among them.
type AlertView struct {
	Here []Alert
	Near []Alert
}

const (
	alertsEvery = 3 * time.Minute
	// zoneFetches caps how many zone shapes one refresh asks for; the rest come on the next.
	zoneFetches = 40
)

// Where the NWS is; a test points it at its own server.
var nwsAPI = "https://api.weather.gov"

type alertState struct {
	mu      sync.Mutex
	view    AlertView
	fetched time.Time
	busy    bool
	lat     float64
	lon     float64
	state   string // home's state, from the NWS, for asking about it and its neighbors
	// outside is until when the NWS is not asked again, after it said it has no forecast point here (a
	// 404): until home moves, or a few hours, in case the 404 was the NWS's mistake and not the place.
	outside time.Time
	zones   map[string][][][2]float64
}

// inUS is whether a place is where the NWS issues alerts: the lower 48, Alaska, Hawaii, Puerto Rico
// and the Virgin Islands. The boxes are rough (the lower 48's holds Toronto and Tijuana too), so Home
// Assistant's country decides where it is set, and a 404 from the NWS's /points settles the rest.
func inUS(lat, lon float64) bool {
	if !countryMayBeUS() {
		return false
	}
	return inLower48(lat, lon) ||
		lat >= 51 && lat <= 72 && (lon <= -141 || lon >= 172) || // Alaska, west of the Yukon
		lat >= 54.5 && lat <= 60 && lon > -141 && lon <= -130 || // and its panhandle
		lat >= 18.5 && lat <= 22.5 && lon >= -161 && lon <= -154 || // Hawaii
		lat >= 17.5 && lat <= 18.6 && lon >= -67.5 && lon <= -64.5 // Puerto Rico and the Virgin Islands
}

// Alerts is what was last fetched, and starts a fetch when one is due. Asked for with every picture
// the screen draws, so the badge follows the alerts without anyone opening a page.
func (f *Feature) Alerts() AlertView {
	if !hasScreen || !AlertsOn() {
		return AlertView{}
	}
	a := &f.alerts
	a.mu.Lock()
	due := !a.busy && (a.fetched.IsZero() || time.Since(a.fetched) > alertsEvery)
	if due {
		a.busy = true
	}
	v := a.view
	a.mu.Unlock()
	if due {
		go f.fetchAlerts()
	}
	// An alert that ended since the last fetch goes now, not at the next one: the screen may have been
	// dark, and fetches only happen while it draws.
	return v.without(time.Now())
}

// without is the view with the alerts over by now taken out.
func (v AlertView) without(now time.Time) AlertView {
	keep := func(as []Alert) []Alert {
		var out []Alert
		for _, a := range as {
			if a.Gone.IsZero() || a.Gone.After(now) {
				out = append(out, a)
			}
		}
		return out
	}
	return AlertView{Here: keep(v.Here), Near: keep(v.Near)}
}

var errAlertPanic = errors.New("reading the alerts failed")

// noPointFor is how long a 404 from the NWS's /points keeps it from being asked again.
const noPointFor = 6 * time.Hour

func (f *Feature) fetchAlerts() {
	a := &f.alerts
	v, err := f.readAlerts()
	a.mu.Lock()
	a.busy, a.fetched = false, time.Now()
	switch {
	case errors.Is(err, errAlertPanic):
		// Logged where it was caught, and tried after the usual wait: sooner, it would only come again.
	case err != nil:
		slog.Debug("alerts: fetch", "err", err)
		a.fetched = time.Now().Add(-alertsEvery + 45*time.Second) // sooner than a good fetch would
	default:
		changed := !sameAlerts(a.view, v)
		a.view = v
		if changed {
			slog.Info("alerts: at home", "count", len(v.Here), "nearby", len(v.Near), "events", eventNames(v.Here))
		}
	}
	a.mu.Unlock()
	f.Changed.Emit(struct{}{})
}

// readAlerts is buildAlerts, a panic in it made an error. Whatever an alert's text holds, a mistake
// in reading it must not take the daemon down with it: the NWS keeps an alert up for hours, and a
// crash would come back after every restart. It recovers outside a.mu, so no lock is left held.
func (f *Feature) readAlerts() (v AlertView, err error) {
	defer func() {
		if p := recover(); p != nil {
			slog.Error("alerts: reading them failed", "panic", p)
			v, err = AlertView{}, fmt.Errorf("%w: %v", errAlertPanic, p)
		}
	}()
	return f.buildAlerts()
}

func (f *Feature) buildAlerts() (AlertView, error) {
	lat, lon, err := homeLocation()
	if err != nil {
		return AlertView{}, err
	}
	return f.buildAlertsAt(lat, lon)
}

// buildAlertsAt is buildAlerts for a home at lat, lon.
func (f *Feature) buildAlertsAt(lat, lon float64) (AlertView, error) {
	if !inUS(lat, lon) {
		return AlertView{}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	a := &f.alerts
	a.mu.Lock()
	if a.lat != lat || a.lon != lon {
		a.lat, a.lon, a.state, a.outside = lat, lon, "", time.Time{}
	}
	st, outside := a.state, time.Now().Before(a.outside)
	a.mu.Unlock()
	if outside {
		return AlertView{}, nil
	}
	// Home to three decimals, about a hundred meters: plenty for which county it is in.
	at := fmt.Sprintf("%.3f,%.3f", lat, lon)
	if st == "" {
		var pt struct {
			Properties struct {
				RelativeLocation struct {
					Properties struct {
						State string `json:"state"`
					} `json:"properties"`
				} `json:"relativeLocation"`
			} `json:"properties"`
		}
		if err := getJSON(ctx, nwsAPI+"/points/"+at, &pt); err != nil {
			if errors.Is(err, errNotFound) {
				slog.Info("alerts: the NWS has no forecast point here; asking again in a while", "after", noPointFor)
				a.mu.Lock()
				a.outside = time.Now().Add(noPointFor)
				a.mu.Unlock()
				return AlertView{}, nil
			}
			return AlertView{}, fmt.Errorf("home's state: %w", err)
		}
		st = pt.Properties.RelativeLocation.Properties.State
		a.mu.Lock()
		a.state = st
		a.mu.Unlock()
	}

	here, err := fetchAlertList(ctx, nwsAPI+"/alerts/active?status=actual&point="+at)
	if err != nil {
		return AlertView{}, err
	}
	hereIDs := map[string]bool{}
	for _, al := range here {
		hereIDs[al.ID] = true
	}
	near := here
	if areas := append([]string{st}, neighbors[st]...); st != "" {
		more, err := fetchAlertList(ctx, fmt.Sprintf("%s/alerts/active?status=actual&area=%s", nwsAPI, strings.Join(areas, ",")))
		if err != nil {
			// The alerts nearby are for the map; home's own still count without them.
			slog.Debug("alerts: nearby", "err", err)
			more = nil
		}
		near = more
		for _, al := range here { // a marine or offshore alert at home may not be in the states' list
			if !slices.ContainsFunc(near, func(n rawAlert) bool { return n.ID == al.ID }) {
				near = append(near, al)
			}
		}
	}

	var v AlertView
	fetches := 0
	for _, raw := range near {
		al := raw.alert()
		al.Here = hereIDs[al.ID]
		switch {
		case len(raw.rings) > 0:
			al.Rings, al.Storm = raw.rings, true
		case al.Here || al.Severity == "Severe" || al.Severity == "Extreme":
			for _, z := range raw.Properties.AffectedZones {
				rings, fetched, err := f.zoneShape(ctx, z, fetches < zoneFetches)
				if fetched {
					fetches++
				}
				if err != nil {
					slog.Debug("alerts: zone shape", "zone", z, "err", err)
				}
				al.Rings = append(al.Rings, rings...)
			}
		}
		if al.Here || len(al.Rings) > 0 && nearHome(al.Rings, lat, lon) {
			v.Near = append(v.Near, al)
		}
	}
	sort.SliceStable(v.Near, func(i, j int) bool { return alertBefore(v.Near[i], v.Near[j]) })
	for _, al := range v.Near {
		if al.Here {
			v.Here = append(v.Here, al)
		}
	}
	return v, nil
}

// alertBefore orders alerts most severe first, a storm's own warning before a zone's of the same.
func alertBefore(a, b Alert) bool {
	if ra, rb := severityRank(a.Severity), severityRank(b.Severity); ra != rb {
		return ra < rb
	}
	return a.Storm && !b.Storm
}

func severityRank(s string) int {
	switch s {
	case "Extreme":
		return 0
	case "Severe":
		return 1
	case "Moderate":
		return 2
	case "Minor":
		return 3
	}
	return 4
}

// nearHome is whether any ring comes within the rain map's reach of home, about 400 km.
func nearHome(rings [][][2]float64, lat, lon float64) bool {
	for _, r := range rings {
		for _, p := range r {
			dy := (p[1] - lat) * 111
			dx := (p[0] - lon) * 111 * math.Cos(lat*math.Pi/180)
			if dx*dx+dy*dy < 400*400 {
				return true
			}
		}
	}
	return false
}

// rawAlert is one feature of the NWS's alert list.
type rawAlert struct {
	ID       string `json:"id"`
	Geometry *struct {
		Type        string          `json:"type"`
		Coordinates json.RawMessage `json:"coordinates"`
	} `json:"geometry"`
	Properties struct {
		Event         string    `json:"event"`
		Severity      string    `json:"severity"`
		SenderName    string    `json:"senderName"`
		AreaDesc      string    `json:"areaDesc"`
		Description   string    `json:"description"`
		Instruction   string    `json:"instruction"`
		MessageType   string    `json:"messageType"`
		Onset         time.Time `json:"onset"`
		Expires       time.Time `json:"expires"`
		Ends          time.Time `json:"ends"`
		AffectedZones []string  `json:"affectedZones"`
		References    []struct {
			ID string `json:"@id"`
		} `json:"references"`
	} `json:"properties"`
	rings [][][2]float64
}

func (r rawAlert) alert() Alert {
	p := r.Properties
	gone := p.Ends
	if gone.IsZero() {
		gone = p.Expires
	}
	onset := p.Onset
	if !onset.After(time.Now()) {
		onset = time.Time{} // under way: the page says when it ends, not when it began
	}
	return Alert{ID: r.ID, Event: p.Event, Severity: p.Severity, Sender: p.SenderName, Area: p.AreaDesc,
		Description: tidyText(p.Description), Instruction: tidyText(p.Instruction),
		Onset: onset, Ends: p.Ends, Gone: gone, Color: AlertColor(p.Event, p.Severity)}
}

// tidyText joins the NWS's hard-wrapped lines into paragraphs and turns "* WHAT..." into "What: ".
func tidyText(s string) string {
	paras := strings.Split(strings.ReplaceAll(s, "\r", ""), "\n\n")
	for i, p := range paras {
		p = strings.Join(strings.Fields(p), " ")
		if strings.HasPrefix(p, "* ") {
			if head, rest, ok := strings.Cut(p[2:], "..."); ok && head != "" && head == strings.ToUpper(head) {
				p = strings.ToUpper(head[:1]) + strings.ToLower(head[1:]) + ": " + rest
			}
		}
		paras[i] = p
	}
	return strings.TrimSpace(strings.Join(paras, "\n"))
}

func fetchAlertList(ctx context.Context, url string) ([]rawAlert, error) {
	var list struct {
		Features []rawAlert `json:"features"`
	}
	if err := getJSON(ctx, url, &list); err != nil {
		return nil, fmt.Errorf("alerts: %w", err)
	}
	now := time.Now()
	// An update names the alerts it replaces; while both are listed only the update counts.
	replaced := map[string]bool{}
	for _, a := range list.Features {
		for _, r := range a.Properties.References {
			replaced[r.ID] = true
		}
	}
	out := list.Features[:0]
	for _, a := range list.Features {
		if replaced[a.ID] {
			continue
		}
		// Gone when canceled, or when the event is over: Ends where the NWS gives it, since Expires is
		// only when this message runs out, often a day before the event does.
		p := a.Properties
		until := p.Ends
		if until.IsZero() {
			until = p.Expires
		}
		if p.MessageType == "Cancel" || !until.IsZero() && until.Before(now) {
			continue
		}
		if a.Geometry != nil {
			a.rings = parseRings(a.Geometry.Type, a.Geometry.Coordinates)
		}
		out = append(out, a)
	}
	return out, nil
}

// parseRings reads a GeoJSON Polygon or MultiPolygon's outer rings, thinned to what a map at this
// scale can show: about a kilometer between points.
func parseRings(kind string, coords json.RawMessage) [][][2]float64 {
	var polys [][][][2]float64
	switch kind {
	case "Polygon":
		var p [][][2]float64
		if json.Unmarshal(coords, &p) == nil {
			polys = [][][][2]float64{p}
		}
	case "MultiPolygon":
		_ = json.Unmarshal(coords, &polys)
	}
	var out [][][2]float64
	for _, p := range polys {
		if len(p) == 0 {
			continue
		}
		out = append(out, thin(p[0], 0.01))
	}
	return out
}

func thin(ring [][2]float64, step float64) [][2]float64 {
	if len(ring) < 8 {
		return ring
	}
	out := [][2]float64{ring[0]}
	for _, p := range ring[1:] {
		last := out[len(out)-1]
		if math.Abs(p[0]-last[0]) >= step || math.Abs(p[1]-last[1]) >= step {
			out = append(out, p)
		}
	}
	return out
}

// zoneDir is where zone shapes are kept between restarts (a test moves it).
var zoneDir = func() string { return filepath.Join(mapDir, "nws-zones") }

// zoneShape is a zone's outline, from memory, from disk, or (when may) from the NWS. fetched reports
// whether it cost a fetch.
func (f *Feature) zoneShape(ctx context.Context, url string, may bool) (rings [][][2]float64, fetched bool, err error) {
	id := url[strings.LastIndex(url, "/")+1:]
	a := &f.alerts
	a.mu.Lock()
	if a.zones == nil {
		a.zones = map[string][][][2]float64{}
	}
	rings, ok := a.zones[id]
	a.mu.Unlock()
	if ok {
		return rings, false, nil
	}
	file := filepath.Join(zoneDir(), id+".json")
	if b, err := os.ReadFile(file); err == nil && json.Unmarshal(b, &rings) == nil {
		a.mu.Lock()
		a.zones[id] = rings
		a.mu.Unlock()
		return rings, false, nil
	}
	if !may {
		return nil, false, nil
	}
	var zone struct {
		Geometry *struct {
			Type        string          `json:"type"`
			Coordinates json.RawMessage `json:"coordinates"`
		} `json:"geometry"`
	}
	if err := getJSON(ctx, url, &zone); err != nil {
		return nil, true, err
	}
	if zone.Geometry != nil {
		rings = parseRings(zone.Geometry.Type, zone.Geometry.Coordinates)
	}
	a.mu.Lock()
	a.zones[id] = rings
	a.mu.Unlock()
	if b, err := json.Marshal(rings); err == nil {
		_ = os.MkdirAll(zoneDir(), 0o755)
		_ = os.WriteFile(file, b, 0o644)
	}
	return rings, true, nil
}

// getJSON reads one of the NWS's answers. A list of a region's alerts with their polygons runs to a
// few megabytes on a stormy day, more than a map tile may be.
func getJSON(ctx context.Context, url string, into any) error {
	b, err := getLimit(ctx, url, "application/geo+json", 16<<20)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, into)
}

func sameAlerts(a, b AlertView) bool {
	if len(a.Here) != len(b.Here) || len(a.Near) != len(b.Near) {
		return false
	}
	for i := range a.Near {
		if a.Near[i].ID != b.Near[i].ID {
			return false
		}
	}
	return true
}

func eventNames(as []Alert) string {
	var n []string
	for _, a := range as {
		n = append(n, a.Event)
	}
	return strings.Join(n, ", ")
}

// AlertColor is the NWS's own map color for an event, or one by its severity for an event not listed.
func AlertColor(event, severity string) color.RGBA {
	if c, ok := eventColors[event]; ok {
		return c
	}
	switch severity {
	case "Extreme":
		return color.RGBA{255, 0, 0, 255}
	case "Severe":
		return color.RGBA{255, 140, 0, 255}
	case "Moderate":
		return color.RGBA{255, 215, 0, 255}
	}
	return color.RGBA{0, 200, 150, 255}
}

// eventColors are the colors weather.gov's maps use for the common events.
var eventColors = map[string]color.RGBA{
	"Tornado Warning":             {255, 0, 0, 255},
	"Tornado Watch":               {255, 255, 0, 255},
	"Severe Thunderstorm Warning": {255, 165, 0, 255},
	"Severe Thunderstorm Watch":   {219, 112, 147, 255},
	"Flash Flood Warning":         {139, 0, 0, 255},
	"Flash Flood Watch":           {46, 139, 87, 255},
	"Flood Warning":               {0, 255, 0, 255},
	"Flood Advisory":              {0, 255, 127, 255},
	"Flood Watch":                 {46, 139, 87, 255},
	"Coastal Flood Warning":       {34, 139, 34, 255},
	"Coastal Flood Advisory":      {124, 252, 0, 255},
	"High Wind Warning":           {218, 165, 32, 255},
	"Wind Advisory":               {210, 180, 140, 255},
	"Heat Advisory":               {255, 127, 80, 255},
	"Excessive Heat Warning":      {199, 21, 133, 255},
	"Winter Storm Warning":        {255, 105, 180, 255},
	"Winter Storm Watch":          {70, 130, 180, 255},
	"Winter Weather Advisory":     {123, 104, 238, 255},
	"Blizzard Warning":            {255, 69, 0, 255},
	"Ice Storm Warning":           {139, 0, 139, 255},
	"Dense Fog Advisory":          {112, 128, 144, 255},
	"Red Flag Warning":            {255, 20, 147, 255},
	"Freeze Warning":              {72, 61, 139, 255},
	"Frost Advisory":              {100, 149, 237, 255},
	"Hurricane Warning":           {220, 20, 60, 255},
	"Tropical Storm Warning":      {178, 34, 34, 255},
	"Special Weather Statement":   {255, 228, 181, 255},
	"Air Quality Alert":           {128, 128, 128, 255},
}
