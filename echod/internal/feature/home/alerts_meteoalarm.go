package home

import (
	"bufio"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"image/color"
	"io"
	"log/slog"
	"math"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Weather alerts from MeteoAlarm (meteoalarm.org), which carries the official warnings of the
// national weather services of Europe, in their languages and English, yellow, orange or red.
//
// Each country has one public Atom feed listing its warnings, one entry per region a warning names.
// Most countries name a region by its EMMA_ID ("ES842", the Cantabrian coast) or another code for it
// (France its NUTS3); a few (Switzerland, Sweden, the U.K....) give the area's outline instead. Home's
// regions are found from its location, among MeteoAlarm's own region outlines (alerts_regions.go);
// an outline is matched against home itself. A warning at home is then read in full from its CAP
// document, which has its text in every language it was issued in.
//
// The feed is read every ten minutes: MeteoAlarm's terms ask that what is shown be never more than
// ten minutes behind meteoalarm.org (https://meteoalarm.org/en/live/page/terms-and-conditions,
// section 5). It cannot say it has not changed, and a big country's runs to megabytes. Listed
// warnings stay in the feed after they run out; those are left out by their expiry.

// Where MeteoAlarm is; a test points it at its own server.
var meteoAlarmFeeds = "https://feeds.meteoalarm.org/feeds/meteoalarm-legacy-atom-"

const (
	// meteoAlarmEvery is how often the feed is asked about: the longest MeteoAlarm's terms let it lag.
	// The feed cannot say it has not changed, so it is asked for its length first, and read again
	// only when that changed, or at least every meteoAlarmFull.
	meteoAlarmEvery = 10 * time.Minute
	meteoAlarmFull  = time.Hour
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

// maRegion is one of MeteoAlarm's regions: its EMMA_ID, country, name, the other codes warnings may
// name it by (type, code), and its outline, longitude and latitude.
type maRegion struct {
	code, country, name string
	aliases             [][2]string
	rings               [][][2]float64
	box                 [4]float64 // west, south, east, north
}

// marine is whether a region is a sea area: MeteoAlarm numbers those from 800, but in a country with
// no coast the 800s are land (Austria's AT801 to AT804 are districts).
func (g maRegion) marine() bool {
	return len(g.code) == 5 && g.code[2] == '8' && !landlocked[g.country]
}

// landlocked are MeteoAlarm's countries with no coast.
var landlocked = map[string]bool{"AD": true, "AT": true, "CH": true, "CZ": true, "HU": true, "LU": true,
	"MD": true, "MK": true, "RS": true, "SK": true}

// regionsNear are the regions home's alerts are for, found from where it is: those it lies in, and
// the sea areas just off it.
func regionsNear(all []maRegion, lat, lon float64) []maRegion {
	var out []maRegion
	for _, g := range all {
		reach := 0.0
		if g.marine() {
			reach = coastKm
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
// "NUTS3"...), where home is, and the countries to read.
type homeArea struct {
	codes     map[[2]string]bool
	lat, lon  float64
	countries []string
	names     []string // the regions, for the log
}

// sig tells one area from another: what a feed's kept entries were matched against.
func (h homeArea) sig() string {
	keys := make([]string, 0, len(h.codes))
	for k := range h.codes {
		keys = append(keys, k[0]+":"+k[1])
	}
	slices.Sort(keys)
	return fmt.Sprintf("%.5f,%.5f %s", h.lat, h.lon, strings.Join(keys, " "))
}

// areaFor is home's area: the regions found from where home is. The countries are those regions',
// and Home Assistant's own.
func areaFor(all []maRegion, lat, lon float64, country string) homeArea {
	h := homeArea{codes: map[[2]string]bool{}, lat: lat, lon: lon}
	add := func(c string) {
		if _, ok := meteoAlarmCountries[c]; ok && !slices.Contains(h.countries, c) && len(h.countries) < maxFeeds {
			h.countries = append(h.countries, c)
		}
	}
	add(country)
	for _, g := range regionsNear(all, lat, lon) {
		h.codes[[2]string{"EMMA_ID", g.code}] = true
		for _, a := range g.aliases {
			h.codes[a] = true
		}
		h.names = append(h.names, g.code+" "+g.name)
		add(g.country)
	}
	return h
}

// ---- the feed

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

// outlines are the entry's outlines, longitude and latitude, as they came: CAP writes "lat,lon
// lat,lon ...". Home is matched against them in full; only the copy drawn on the map is thinned.
func (e maEntry) outlines() [][][2]float64 {
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
			out = append(out, ring)
		}
	}
	return out
}

// drawn are outlines thinned to what the rain map can show, about a kilometer between points.
func drawn(outlines [][][2]float64) [][][2]float64 {
	out := make([][][2]float64, 0, len(outlines))
	for _, r := range outlines {
		out = append(out, thin(r, 0.01))
	}
	return out
}

// at is whether the entry is for home: one of its codes is one of home's regions', or its outline holds
// home.
func (h homeArea) at(e maEntry, rings [][][2]float64) bool {
	for _, g := range e.Geocodes {
		if h.codes[[2]string{strings.TrimSpace(g.Name), strings.ToUpper(strings.TrimSpace(g.Value))}] {
			return true
		}
	}
	for _, r := range rings {
		if ringContains(r, h.lat, h.lon) {
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
// warnings are listed, each country feed's home entries as last read, and the area last matched,
// for the log.
type meteoAlarm struct {
	caps   map[string]*capAlert // nil for one that could not be had (a 404: it was replaced)
	feeds  map[string]*feedMemo
	names  string
	logged bool
}

// feedMemo is a country feed as last read: its length, when, for which area, and home's entries.
type feedMemo struct {
	length  int64
	read    time.Time
	area    string
	entries []maEntry
}

var errFeed = errors.New("not a MeteoAlarm feed")

// homeEntries are the entries of a country's feed that are for home. The feed is asked for its
// length first: unchanged, and read within meteoAlarmFull, the entries kept from last time are it.
func (f *Feature) homeEntries(ctx context.Context, country string, area homeArea) ([]maEntry, error) {
	url := meteoAlarmFeeds + meteoAlarmCountries[country]
	a := &f.alerts
	a.mu.Lock()
	memo := a.ma.feeds[country]
	a.mu.Unlock()
	if memo != nil && memo.area == area.sig() && time.Since(memo.read) < meteoAlarmFull {
		if n, err := feedLength(ctx, url); err == nil && n > 0 && n == memo.length {
			return memo.entries, nil
		}
	}
	entries, n, err := readFeed(ctx, url, area)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", country, err)
	}
	a.mu.Lock()
	if a.ma.feeds == nil {
		a.ma.feeds = map[string]*feedMemo{}
	}
	a.ma.feeds[country] = &feedMemo{length: n, read: time.Now(), area: area.sig(), entries: entries}
	a.mu.Unlock()
	return entries, nil
}

// feedLength is a feed's length, from a HEAD.
func feedLength(ctx context.Context, url string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, "HEAD", url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := maClient.Do(req)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return resp.ContentLength, nil
}

// readFeed reads a country's feed as it comes, an entry at a time, and keeps home's: a big
// country's feed runs to megabytes, nearly all of it for elsewhere. A feed that is not one
// (MeteoAlarm answers some with a page saying "Internal Server Error") is an error, not a country
// without warnings.
func readFeed(ctx context.Context, url string, area homeArea) (entries []maEntry, length int64, err error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, 0, err
	}
	// No Accept: MeteoAlarm answers one naming the feed's own type with 406 Not Acceptable.
	req.Header.Set("User-Agent", userAgent)
	resp, err := maClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, 0, fmt.Errorf("%s: %w", url, errNotFound)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("%s: %s", url, resp.Status)
	}
	counted := &countingReader{r: io.LimitReader(resp.Body, 64<<20)}
	sc := bufio.NewScanner(counted)
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20) // one entry, its outline and all
	sc.Split(splitEntries)
	isFeed := false
	for sc.Scan() {
		chunk := sc.Bytes()
		start := bytes.Index(chunk, []byte("<entry>"))
		head := chunk
		if start >= 0 {
			head = chunk[:start]
		}
		if !isFeed && bytes.Contains(head, []byte("<feed")) {
			isFeed = true // a feed with no entries is one too
		}
		if start < 0 || !isFeed {
			continue
		}
		chunk = chunk[start:]
		// Most of a big country's feed is for elsewhere: told from the raw bytes, without decoding.
		if !area.mayBeAt(chunk) {
			continue
		}
		var e maEntry
		if err := xml.Unmarshal(chunk, &e); err != nil {
			return nil, 0, fmt.Errorf("%w: %v", errFeed, err)
		}
		if area.at(e, e.outlines()) {
			entries = append(entries, e)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, 0, fmt.Errorf("%w: %v", errFeed, err)
	}
	if !isFeed {
		return nil, 0, errFeed
	}
	n := resp.ContentLength
	if n <= 0 {
		n = counted.n
	}
	return entries, n, nil
}

// splitEntries splits a feed after each entry: a token is what came before an entry and the entry.
func splitEntries(data []byte, atEOF bool) (int, []byte, error) {
	end := []byte("</entry>")
	if i := bytes.Index(data, end); i >= 0 {
		return i + len(end), data[:i+len(end)], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// mayBeAt is whether an entry's raw bytes may be for home: one of home's codes is in it, or it has
// an outline and a point of it is within reach of home. It may say yes wrongly, never no.
func (h homeArea) mayBeAt(chunk []byte) bool {
	for k := range h.codes {
		if bytes.Contains(chunk, []byte(">"+k[1]+"<")) {
			return true
		}
	}
	rest := chunk
	for {
		i := bytes.Index(rest, []byte("polygon>"))
		if i < 0 {
			return false
		}
		rest = rest[i+len("polygon>"):]
		j := bytes.IndexByte(rest, '<')
		if j < 0 {
			return false
		}
		poly := rest[:j]
		rest = rest[j:]
		var ring [][2]float64
		for _, pair := range bytes.Fields(poly) {
			la, lo, ok := bytes.Cut(pair, []byte(","))
			if !ok {
				continue
			}
			lat, err1 := strconv.ParseFloat(string(la), 64)
			lon, err2 := strconv.ParseFloat(string(lo), 64)
			if err1 == nil && err2 == nil {
				ring = append(ring, [2]float64{lon, lat})
			}
		}
		if len(ring) >= 3 && ringContains(ring, h.lat, h.lon) {
			return true
		}
	}
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// buildMeteoAlarm is the alerts for a home at lat, lon from MeteoAlarm.
func (f *Feature) buildMeteoAlarm(ctx context.Context, lat, lon float64) (AlertView, error) {
	// The regions may need MeteoAlarm's geocode file, some 30 MB: more time than a feed.
	rctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	all := regionsAround(rctx, lat, lon)
	cancel()
	area := areaFor(all, lat, lon, homeCountry())
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
		entries, err := f.homeEntries(ctx, c, area)
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
			rings := drawn(e.outlines())
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
