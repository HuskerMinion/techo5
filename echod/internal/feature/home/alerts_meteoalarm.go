package home

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"image/color"
	"log/slog"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Weather alerts from MeteoAlarm (meteoalarm.org), which carries the official warnings of the
// national weather services of Europe, in their languages and English, yellow, orange or red.
//
// Each country has one public Atom feed listing its warnings, one entry per region a warning names.
// Most countries name a region by its EMMA_ID ("ES842", the Cantabrian coast) or another code for it
// (France its NUTS3); a few (Switzerland, Sweden, the U.K....) give the area's outline instead. Home's
// regions are found from its location, among MeteoAlarm's own region outlines (alerts_regions.go),
// unless they are set by hand; an outline is matched against home itself. A warning at home is then
// read in full from its CAP document, which has its text in every language it was issued in.
//
// The feed is read every ten minutes (AlertsEveryMinutes): MeteoAlarm's terms ask that what is shown
// be never more than ten minutes behind meteoalarm.org (https://meteoalarm.org/en/live/page/terms-and-conditions,
// section 5). It cannot say it has not changed, and a big country's runs to megabytes, so a longer
// wait may be chosen. Listed warnings stay in the feed after they run out; those are left out by their
// expiry.

// Where MeteoAlarm is; a test points it at its own server.
var meteoAlarmFeeds = "https://feeds.meteoalarm.org/feeds/meteoalarm-legacy-atom-"

const (
	// meteoAlarmEvery is how often the feed is read unless set otherwise: MeteoAlarm's terms' longest delay.
	meteoAlarmEvery = 10 * time.Minute
	// capFetches caps how many CAP documents one refresh asks for; the rest are shown from the feed.
	capFetches = 12
	// coastKm is how far off home a coastal region (a sea area, numbered 8xx) still counts as home's.
	coastKm = 10
	// maxFeeds caps how many countries' feeds a home near a border reads.
	maxFeeds = 3
)

// meteoAlarmCountries are the countries MeteoAlarm has a feed for, by ISO code, and the feed's name.
var meteoAlarmCountries = map[string]string{
	"AD": "andorra", "AT": "austria", "BA": "bosnia-herzegovina", "BE": "belgium", "BG": "bulgaria",
	"CH": "switzerland", "CY": "cyprus", "CZ": "czechia", "DE": "germany", "DK": "denmark",
	"EE": "estonia", "ES": "spain", "FI": "finland", "FR": "france", "GB": "united-kingdom",
	"GR": "greece", "HR": "croatia", "HU": "hungary", "IE": "ireland", "IL": "israel",
	"IS": "iceland", "IT": "italy", "LT": "lithuania", "LU": "luxembourg", "LV": "latvia",
	"MD": "moldova", "ME": "montenegro", "MK": "republic-of-north-macedonia", "MT": "malta",
	"NL": "netherlands", "NO": "norway", "PL": "poland", "PT": "portugal", "RO": "romania",
	"RS": "serbia", "SE": "sweden", "SI": "slovenia", "SK": "slovakia", "UA": "ukraine",
}

// codeCountry is the country an EMMA_ID is in: its first two letters, but for the few that differ.
func codeCountry(code string) string {
	if len(code) < 2 {
		return ""
	}
	switch c := strings.ToUpper(code[:2]); c {
	case "EI":
		return "IE"
	case "UK":
		return "GB"
	case "EL":
		return "GR"
	default:
		return c
	}
}

// maRegion is one of MeteoAlarm's regions: its EMMA_ID, country, name, the other codes warnings may
// name it by (type, code), and its outline, longitude and latitude.
type maRegion struct {
	code, country, name string
	aliases             [][2]string
	rings               [][][2]float64
	box                 [4]float64 // west, south, east, north
}

// marine is whether a region is a sea area: MeteoAlarm numbers those from 800.
func (g maRegion) marine() bool { return len(g.code) == 5 && g.code[2] == '8' }

// regionsNear are the regions home's alerts are for, found from where it is: those it lies in, the
// sea areas just off it, and any within nearKm.
func regionsNear(all []maRegion, lat, lon float64, nearKm int) []maRegion {
	var out []maRegion
	for _, g := range all {
		reach := float64(nearKm)
		if g.marine() {
			reach = max(reach, coastKm)
		}
		if !g.boxNear(lat, lon, reach) {
			continue
		}
		if g.contains(lat, lon) || reach > 0 && g.kmFrom(lat, lon) <= reach {
			out = append(out, g)
		}
	}
	return out
}

func (g maRegion) boxNear(lat, lon, km float64) bool {
	dLat := km / 111
	dLon := km / (111 * math.Max(math.Cos(lat*math.Pi/180), 0.1))
	return lon >= g.box[0]-dLon && lon <= g.box[2]+dLon && lat >= g.box[1]-dLat && lat <= g.box[3]+dLat
}

func (g maRegion) contains(lat, lon float64) bool {
	in := false
	for _, r := range g.rings {
		if ringContains(r, lat, lon) {
			in = !in // a ring inside another is a hole; MeteoAlarm's outer rings do not nest, but holes would
		}
	}
	return in
}

func (g maRegion) kmFrom(lat, lon float64) float64 {
	d := math.Inf(1)
	for _, r := range g.rings {
		d = min(d, ringKm(r, lat, lon))
	}
	return d
}

// ringContains is whether a ring (longitude, latitude) holds a point, by counting its edges crossed.
func ringContains(r [][2]float64, lat, lon float64) bool {
	in := false
	for i, j := 0, len(r)-1; i < len(r); j, i = i, i+1 {
		a, b := r[i], r[j]
		if (a[1] > lat) != (b[1] > lat) && lon < (b[0]-a[0])*(lat-a[1])/(b[1]-a[1])+a[0] {
			in = !in
		}
	}
	return in
}

// ringKm is how far a point is from a ring's edge, in kilometers, on a flat map around the point.
func ringKm(r [][2]float64, lat, lon float64) float64 {
	kx := 111 * math.Cos(lat*math.Pi/180)
	d := math.Inf(1)
	for i := range r {
		a, b := r[i], r[(i+1)%len(r)]
		ax, ay := (a[0]-lon)*kx, (a[1]-lat)*111
		bx, by := (b[0]-lon)*kx, (b[1]-lat)*111
		dx, dy := bx-ax, by-ay
		t := 0.0
		if l := dx*dx + dy*dy; l > 0 {
			t = math.Max(0, math.Min(1, -(ax*dx+ay*dy)/l))
		}
		x, y := ax+t*dx, ay+t*dy
		d = min(d, math.Hypot(x, y))
	}
	return d
}

// homeArea is what home's alerts are matched against: its regions' codes, by their type ("EMMA_ID",
// "NUTS3"...), where home is, how far around it an outline still counts, and the countries to read.
type homeArea struct {
	codes     map[[2]string]bool
	lat, lon  float64
	nearKm    float64
	countries []string
	names     []string // the regions, for the log and the setup page
}

// areaFor is home's area: the regions set by hand when there are any, else the ones found from where
// home is. The countries are those regions', and Home Assistant's own.
func areaFor(all []maRegion, lat, lon float64, set []string, nearKm int, country string) homeArea {
	h := homeArea{codes: map[[2]string]bool{}, lat: lat, lon: lon, nearKm: float64(nearKm)}
	add := func(c string) {
		if _, ok := meteoAlarmCountries[c]; ok && !slices.Contains(h.countries, c) && len(h.countries) < maxFeeds {
			h.countries = append(h.countries, c)
		}
	}
	add(country)
	var found []maRegion
	if len(set) > 0 {
		for _, c := range set {
			c = strings.ToUpper(strings.TrimSpace(c))
			if c == "" {
				continue
			}
			h.codes[[2]string{"EMMA_ID", c}] = true
			add(codeCountry(c))
			name := c
			if i := slices.IndexFunc(all, func(g maRegion) bool { return g.code == c }); i >= 0 {
				found = append(found, all[i])
				name = c + " " + all[i].name
			}
			h.names = append(h.names, name)
		}
	} else {
		found = regionsNear(all, lat, lon, nearKm)
		for _, g := range found {
			h.codes[[2]string{"EMMA_ID", g.code}] = true
			h.names = append(h.names, g.code+" "+g.name)
			add(g.country)
		}
	}
	for _, g := range found {
		for _, a := range g.aliases {
			h.codes[a] = true
		}
	}
	return h
}

// ---- the feed

// maFeed is a country's Atom feed, as much of it as is read.
type maFeed struct {
	Entries []maEntry `xml:"entry"`
}

type maEntry struct {
	Geocodes []struct {
		Name  string `xml:"valueName"`
		Value string `xml:"value"`
	} `xml:"geocode"`
	Polygons    []string `xml:"polygon"`
	AreaDesc    string   `xml:"areaDesc"`
	Event       string   `xml:"event"`
	Expires     string   `xml:"expires"`
	Onset       string   `xml:"onset"`
	Severity    string   `xml:"severity"`
	MessageType string   `xml:"message_type"`
	Sent        string   `xml:"sent"`
	Status      string   `xml:"status"`
	Identifier  string   `xml:"identifier"`
	Title       string   `xml:"title"`
	Links       []struct {
		Type string `xml:"type,attr"`
		Href string `xml:"href,attr"`
	} `xml:"link"`
}

func (e maEntry) capURL() string {
	for _, l := range e.Links {
		if l.Type == "application/cap+xml" {
			return l.Href
		}
	}
	return ""
}

// rings are the entry's outlines, longitude and latitude: CAP writes "lat,lon lat,lon ...".
func (e maEntry) rings() [][][2]float64 {
	var out [][][2]float64
	for _, p := range e.Polygons {
		var ring [][2]float64
		for _, pair := range strings.Fields(p) {
			la, lo, ok := strings.Cut(pair, ",")
			if !ok {
				continue
			}
			lat, err1 := strconv.ParseFloat(la, 64)
			lon, err2 := strconv.ParseFloat(lo, 64)
			if err1 == nil && err2 == nil {
				ring = append(ring, [2]float64{lon, lat})
			}
		}
		if len(ring) >= 3 {
			out = append(out, thin(ring, 0.01))
		}
	}
	return out
}

// at is whether the entry is for home: one of its codes is one of home's regions', or its outline holds
// home or comes within its reach.
func (h homeArea) at(e maEntry, rings [][][2]float64) bool {
	for _, g := range e.Geocodes {
		if h.codes[[2]string{strings.TrimSpace(g.Name), strings.ToUpper(strings.TrimSpace(g.Value))}] {
			return true
		}
	}
	for _, r := range rings {
		if ringContains(r, h.lat, h.lon) || h.nearKm > 0 && ringKm(r, h.lat, h.lon) <= h.nearKm {
			return true
		}
	}
	return false
}

func parseCAPTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(s))
	if err != nil {
		return time.Time{}
	}
	return t
}

// ---- a warning's CAP document

type capAlert struct {
	Identifier string    `xml:"identifier"`
	MsgType    string    `xml:"msgType"`
	Sent       string    `xml:"sent"`
	References string    `xml:"references"`
	Infos      []capInfo `xml:"info"`
}

type capInfo struct {
	Language    string `xml:"language"`
	Event       string `xml:"event"`
	Severity    string `xml:"severity"`
	Onset       string `xml:"onset"`
	Expires     string `xml:"expires"`
	SenderName  string `xml:"senderName"`
	Headline    string `xml:"headline"`
	Description string `xml:"description"`
	Instruction string `xml:"instruction"`
	Parameters  []struct {
		Name  string `xml:"valueName"`
		Value string `xml:"value"`
	} `xml:"parameter"`
}

// referenced are the identifiers of the warnings this one replaces: CAP lists them as
// "sender,identifier,sent", separated by spaces.
func (c *capAlert) referenced() []string {
	var out []string
	for _, r := range strings.Fields(c.References) {
		if parts := strings.Split(r, ","); len(parts) >= 2 {
			out = append(out, parts[1])
		}
	}
	return out
}

// param is a parameter's number, from MeteoAlarm's "2; yellow; Moderate" or "1; Wind": 0 when absent.
func (i capInfo) param(name string) int {
	for _, p := range i.Parameters {
		if p.Name == name {
			n, _ := strconv.Atoi(strings.TrimSpace(strings.SplitN(p.Value, ";", 2)[0]))
			return n
		}
	}
	return 0
}

// alertText is a warning's words in one language, as the service issued them.
type alertText struct {
	lang        string // "es", "en", "de": the language's own two letters
	event       string // "Aviso de vientos de nivel amarillo"
	headline    string // "Aviso de vientos de nivel amarillo. Interior norte de Castellón"
	sender      string
	description string
	instruction string
}

// The awareness levels: MeteoAlarm's 1 is green, no warning.
const (
	levelYellow = 2
	levelOrange = 3
	levelRed    = 4
)

// meteoAlarmColor is a level's color.
func meteoAlarmColor(level int) color.RGBA {
	switch level {
	case levelRed:
		return color.RGBA{230, 0, 0, 255}
	case levelOrange:
		return color.RGBA{255, 140, 0, 255}
	}
	return color.RGBA{255, 221, 0, 255}
}

// The awareness types, by MeteoAlarm's number, as the feed's titles name them.
var alertKinds = map[int]string{
	1: "wind", 2: "snow-ice", 3: "thunderstorm", 4: "fog", 5: "high-temperature", 6: "low-temperature",
	7: "coastalevent", 8: "forestfire", 9: "avalanches", 10: "rain", 12: "flooding", 13: "rain-flood",
}

// fromTitle reads the level and type from a feed entry's title, "Orange Coastalevent Warning issued
// for Spain - ...", for a warning whose CAP document could not be read.
func fromTitle(title string) (level, kind int) {
	f := strings.Fields(strings.ToLower(title))
	if len(f) < 2 {
		return 0, 0
	}
	switch f[0] {
	case "green":
		level = 1
	case "yellow":
		level = levelYellow
	case "orange":
		level = levelOrange
	case "red":
		level = levelRed
	}
	for n, k := range alertKinds {
		if f[1] == k || strings.ReplaceAll(f[1], "-", "") == strings.ReplaceAll(k, "-", "") {
			kind = n
		}
	}
	return level, kind
}

func levelSeverity(level int, severity string) string {
	switch level {
	case levelRed:
		return "Extreme"
	case levelOrange:
		return "Severe"
	case levelYellow:
		return "Moderate"
	}
	return severity
}

// langOf is a CAP language tag's own language: "es-ES" and "es" are both "es".
func langOf(tag string) string {
	l, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(tag)), "-")
	return l
}

// ---- putting it together

// meteoAlarm is the source's memory between refreshes: the CAP documents read, kept while their
// warnings are listed, and the area last matched, for the log.
type meteoAlarm struct {
	caps   map[string]*capAlert // nil for one that could not be had (a 404: it was replaced)
	names  string
	logged bool
}

var errFeed = errors.New("not a MeteoAlarm feed")

// readFeed is a country's feed. A feed that is not one (MeteoAlarm answers some with a page saying
// "Internal Server Error") is an error, not a country without warnings.
func readFeed(ctx context.Context, country string) ([]maEntry, error) {
	// No Accept: MeteoAlarm answers one naming the feed's own type with 406 Not Acceptable.
	b, err := getLimit(ctx, meteoAlarmFeeds+meteoAlarmCountries[country], "", 32<<20)
	if err != nil {
		return nil, err
	}
	var f maFeed
	if err := xml.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("%s: %w: %v", country, errFeed, err)
	}
	return f.Entries, nil
}

// buildMeteoAlarm is the alerts for a home at lat, lon from MeteoAlarm.
func (f *Feature) buildMeteoAlarm(ctx context.Context, lat, lon float64) (AlertView, error) {
	h := config.Get().Home
	area := areaFor(loadRegions(), lat, lon, h.AlertRegions, h.AlertsNearKm, homeCountry())
	if len(area.countries) == 0 {
		f.alerts.mu.Lock()
		if !f.alerts.ma.logged {
			f.alerts.ma.logged = true
			slog.Info("alerts: MeteoAlarm has no region or country for home", "country", homeCountry())
		}
		f.alerts.mu.Unlock()
		return AlertView{}, nil
	}
	now := time.Now()

	// One alert per CAP document: a warning is listed once for each region it names.
	type found struct {
		entry maEntry
		areas []string
		rings [][][2]float64
	}
	var order []string
	byURL := map[string]*found{}
	var failed error
	read := 0
	for _, c := range area.countries {
		entries, err := readFeed(ctx, c)
		if err != nil {
			// A neighbor's feed that fails leaves home's own: only when none could be read is it an error.
			slog.Debug("alerts: meteoalarm feed", "country", c, "err", err)
			failed = err
			continue
		}
		read++
		for _, e := range entries {
			if !strings.EqualFold(e.Status, "Actual") || strings.EqualFold(e.MessageType, "Cancel") {
				continue
			}
			if exp := parseCAPTime(e.Expires); !exp.IsZero() && !exp.After(now) {
				continue
			}
			if lv, _ := fromTitle(e.Title); lv == 1 {
				continue // green: nothing to be aware of
			}
			rings := e.rings()
			if !area.at(e, rings) {
				continue
			}
			key := e.capURL()
			if key == "" {
				key = e.Identifier
			}
			fd := byURL[key]
			if fd == nil {
				fd = &found{entry: e}
				byURL[key] = fd
				order = append(order, key)
			}
			if a := strings.TrimSpace(e.AreaDesc); a != "" && !slices.Contains(fd.areas, a) {
				fd.areas = append(fd.areas, a)
			}
			fd.rings = append(fd.rings, rings...)
		}
	}
	if read == 0 {
		return AlertView{}, fmt.Errorf("meteoalarm: %w", failed)
	}

	ma := &f.alerts.ma
	f.alerts.mu.Lock()
	old := ma.caps
	f.alerts.mu.Unlock()
	caps := map[string]*capAlert{}
	fetches := 0
	for _, key := range order {
		if !strings.HasPrefix(key, "http") {
			continue
		}
		if c, ok := old[key]; ok {
			caps[key] = c
			continue
		}
		if fetches >= capFetches {
			continue
		}
		fetches++
		c, err := readCAP(ctx, key)
		if err != nil && !errors.Is(err, errNotFound) {
			slog.Debug("alerts: meteoalarm warning", "url", key, "err", err)
			continue // asked for again next time
		}
		caps[key] = c
	}

	// A warning another one listed replaces counts once, as the newer.
	replaced := map[string]bool{}
	for _, c := range caps {
		if c != nil {
			for _, id := range c.referenced() {
				replaced[id] = true
			}
		}
	}

	var v AlertView
	for _, key := range order {
		fd := byURL[key]
		e := fd.entry
		if replaced[strings.TrimSpace(e.Identifier)] {
			continue
		}
		al, ok := meteoAlarmAlert(key, e, fd.areas, fd.rings, caps[key], now)
		if ok {
			v.Near = append(v.Near, al)
		}
	}
	sort.SliceStable(v.Near, func(i, j int) bool { return alertBefore(v.Near[i], v.Near[j]) })
	v.Here = v.Near

	names := strings.Join(area.names, ", ")
	f.alerts.mu.Lock()
	ma.caps = caps
	if names != ma.names || !ma.logged {
		ma.names, ma.logged = names, true
		slog.Info("alerts: MeteoAlarm regions for home", "countries", strings.Join(area.countries, ","), "regions", names)
	}
	f.alerts.mu.Unlock()
	return v, nil
}

func readCAP(ctx context.Context, url string) (*capAlert, error) {
	b, err := getLimit(ctx, url, "", 2<<20) // no Accept, as for the feed
	if err != nil {
		return nil, err
	}
	var c capAlert
	if err := xml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", url, err)
	}
	return &c, nil
}

// meteoAlarmAlert is one warning as the screen shows it, from its feed entry and, when it could be
// read, its CAP document; false when it is not one to show (green, or over).
func meteoAlarmAlert(id string, e maEntry, areas []string, rings [][][2]float64, c *capAlert, now time.Time) (Alert, bool) {
	level, kind := fromTitle(e.Title)
	severity := strings.TrimSpace(e.Severity)
	onset, ends, issued := parseCAPTime(e.Onset), parseCAPTime(e.Expires), parseCAPTime(e.Sent)
	var texts []alertText
	if c != nil {
		if t := parseCAPTime(c.Sent); !t.IsZero() {
			issued = t
		}
		for _, in := range c.Infos {
			if n := in.param("awareness_level"); n > 0 {
				level = n
			}
			if n := in.param("awareness_type"); n > 0 {
				kind = n
			}
			if s := strings.TrimSpace(in.Severity); s != "" {
				severity = s
			}
			if t := parseCAPTime(in.Onset); !t.IsZero() {
				onset = t
			}
			if t := parseCAPTime(in.Expires); !t.IsZero() {
				ends = t
			}
			event, headline := strings.TrimSpace(in.Event), strings.TrimSpace(in.Headline)
			if event == "" {
				event = headline
			}
			texts = append(texts, alertText{lang: langOf(in.Language), event: event, headline: headline, sender: strings.TrimSpace(in.SenderName),
				description: tidyMeteoAlarm(in.Description), instruction: tidyMeteoAlarm(in.Instruction)})
		}
	}
	if len(texts) == 0 {
		// The feed's own words: its event is in English for most countries.
		texts = []alertText{{lang: "en", event: strings.TrimSpace(e.Event)}}
	}
	if level == 1 || !ends.IsZero() && !ends.After(now) {
		return Alert{}, false
	}
	if !onset.After(now) {
		onset = time.Time{} // under way
	}
	al := Alert{ID: id, Source: "MeteoAlarm", Severity: levelSeverity(level, severity),
		Area: strings.Join(areas, ", "), Onset: onset, Ends: ends, Gone: ends,
		Color: meteoAlarmColor(level), Here: true, Level: level, Kind: kind, Rings: rings, Issued: issued, texts: texts}
	al = al.inLanguage("en")
	return al, true
}

// tidyMeteoAlarm joins a CAP text's lines into paragraphs: a blank line, or a line of its own, is a
// new paragraph; the rest were wrapped.
func tidyMeteoAlarm(s string) string {
	var paras []string
	for _, p := range strings.Split(strings.ReplaceAll(s, "\r", ""), "\n") {
		if p = strings.Join(strings.Fields(p), " "); p != "" {
			paras = append(paras, p)
		}
	}
	return strings.Join(paras, "\n")
}

// inLanguage is the alert in a screen language: the service's words in that language when it issued
// it in that language, else in English, else in the first it came in. Nothing is translated here.
func (a Alert) inLanguage(lang string) Alert {
	if len(a.texts) == 0 {
		return a
	}
	pick := a.texts[0]
	want := langOf(lang)
	if i := slices.IndexFunc(a.texts, func(t alertText) bool { return t.lang == want }); i >= 0 {
		pick = a.texts[i]
	} else if i := slices.IndexFunc(a.texts, func(t alertText) bool { return t.lang == "en" }); i >= 0 {
		pick = a.texts[i]
	}
	a.Event, a.Headline = pick.event, ""
	if pick.headline != pick.event {
		a.Headline = pick.headline // its whole headline, on the page above the text
	}
	a.Description, a.Instruction = pick.description, pick.instruction
	// MeteoAlarm's terms: the issuing service named, and a link to meteoalarm.org.
	a.Sender = "EUMETNET – MeteoAlarm · meteoalarm.org"
	if pick.sender != "" {
		a.Sender = pick.sender + " · meteoalarm.org"
	}
	return a
}
