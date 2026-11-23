package home

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// MeteoAlarm's warning regions around home, from MeteoAlarm's own geocode files
// (gitlab.com/meteoalarm-pm-group/documents, licensed under terms equivalent to CC BY 4.0): a GeoJSON
// of every region a warning names by its EMMA_ID, and a list of the other codes some countries name
// the same regions by (France's NUTS3, Germany's WARNCELLID...). Nothing of it is in the image. The
// files are downloaded only for a home that may be in a MeteoAlarm country; the GeoJSON, some 30 MB,
// is read as a stream and only the regions near home are kept, at full detail and with their holes
// (a city a district surrounds is a hole in the district), in a small file on the device. MeteoAlarm
// publishes a new pair a few times a year; whether there is one is asked once a month.

// Where MeteoAlarm's files are; a test points it at its own server.
var (
	geocodeList = "https://gitlab.com/api/v4/projects/meteoalarm-pm-group%2Fdocuments/repository/tree?per_page=100"
	geocodeRaw  = "https://gitlab.com/meteoalarm-pm-group/documents/-/raw/master/"
)

// regionsDir is where the regions around home are kept (a test moves it).
var regionsDir = func() string { return filepath.Join(mapDir, "meteoalarm") }

const (
	// regionsCheck is how often whether MeteoAlarm has published newer geocode files is asked.
	regionsCheck = 30 * 24 * time.Hour
	// regionsRetry is how long a failed download waits before it is tried again.
	regionsRetry = time.Hour
	// regionsKeepKm is how far from home a region is kept: past the coastal reach, so a sea area off
	// home is kept too.
	regionsKeepKm = 20
)

// maClient fetches MeteoAlarm's feeds and files: a country's feed or the geocode file takes longer
// than a map tile.
var maClient = &http.Client{Timeout: 5 * time.Minute}

// regionNames are names MeteoAlarm's geocode file has garbled, by code.
var regionNames = map[string]string{
	"ES072": "Poniente y Almería Capital",
	"ES133": "Litoral cántabro",
	"ES842": "Costa - Litoral cántabro",
	"ES857": "Costa - Poniente y Almería Capital",
}

// regionsFile is what is kept on the device: the regions near a place, from a pair of MeteoAlarm's files.
type regionsFile struct {
	Source  string     `json:"source"` // the geocode file's name, which carries its date
	Lat     float64    `json:"lat"`
	Lon     float64    `json:"lon"`
	Checked time.Time  `json:"checked"` // when MeteoAlarm was last asked for newer files
	Regions []regionJS `json:"regions"`
}

type regionJS struct {
	Code    string         `json:"code"`
	Country string         `json:"country"`
	Name    string         `json:"name"`
	Aliases [][2]string    `json:"aliases,omitempty"`
	Rings   [][][2]float64 `json:"rings"`
}

var regionStore struct {
	sync.Mutex
	file  *regionsFile
	tried time.Time // when a download last failed
}

// regionsAround are the regions near home, from the file kept on the device, downloaded first when
// there is none for this place, or when MeteoAlarm has published newer files since it was made. On a
// failure what was kept is used, or none, and the download waits a while before it is tried again.
func regionsAround(ctx context.Context, lat, lon float64) []maRegion {
	regionStore.Lock()
	defer regionStore.Unlock()
	f := regionStore.file
	if f == nil || f.Lat != lat || f.Lon != lon {
		f = readRegionsFile(lat, lon)
		regionStore.file = f
	}
	stale := f == nil || time.Since(f.Checked) > regionsCheck
	if stale && time.Since(regionStore.tried) > regionsRetry {
		nf, err := refreshRegions(ctx, f, lat, lon)
		if err != nil {
			regionStore.tried = time.Now()
			slog.Warn("alerts: MeteoAlarm's regions could not be fetched", "err", err)
		} else {
			f = nf
			regionStore.file = f
		}
	}
	if f == nil {
		return nil
	}
	return f.regions()
}

func (f *regionsFile) regions() []maRegion {
	out := make([]maRegion, 0, len(f.Regions))
	for _, r := range f.Regions {
		g := maRegion{code: r.Code, country: r.Country, name: r.Name, aliases: r.Aliases, rings: r.Rings}
		g.box = boxOf(r.Rings)
		out = append(out, g)
	}
	return out
}

func regionsPath() string { return filepath.Join(regionsDir(), "regions.json") }

// readRegionsFile is the file kept on the device, when it is for this place.
func readRegionsFile(lat, lon float64) *regionsFile {
	b, err := os.ReadFile(regionsPath())
	if err != nil {
		return nil
	}
	var f regionsFile
	if json.Unmarshal(b, &f) != nil || f.Lat != lat || f.Lon != lon {
		return nil
	}
	return &f
}

func writeRegionsFile(f *regionsFile) {
	b, err := json.Marshal(f)
	if err != nil {
		return
	}
	_ = os.MkdirAll(regionsDir(), 0o755)
	tmp := regionsPath() + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		_ = os.Rename(tmp, regionsPath())
	}
}

var (
	geocodeName = regexp.MustCompile(`^MeteoAlarm_Geocodes_\d{4}_\d\d_\d\d\.json$`)
	aliasName   = regexp.MustCompile(`^MeteoAlarm_Geocodes-Aliases_\d{4}_\d\d_\d\d\.csv$`)
)

// refreshRegions asks MeteoAlarm for its newest geocode files and, when they are not the ones the
// kept file came from (or there is none), reads the regions near home from them.
func refreshRegions(ctx context.Context, old *regionsFile, lat, lon float64) (*regionsFile, error) {
	var tree []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	b, err := getLimit(ctx, geocodeList, "", 1<<20)
	if err != nil {
		return nil, fmt.Errorf("listing: %w", err)
	}
	if err := json.Unmarshal(b, &tree); err != nil {
		return nil, fmt.Errorf("listing: %w", err)
	}
	var geo, ali []string
	for _, e := range tree {
		switch {
		case e.Type != "blob":
		case geocodeName.MatchString(e.Name):
			geo = append(geo, e.Name)
		case aliasName.MatchString(e.Name):
			ali = append(ali, e.Name)
		}
	}
	if len(geo) == 0 {
		return nil, errors.New("no geocode file listed")
	}
	sort.Strings(geo) // the names carry their dates
	sort.Strings(ali)
	newest := geo[len(geo)-1]
	if old != nil && old.Source == newest {
		old.Checked = time.Now()
		writeRegionsFile(old)
		return old, nil
	}
	aliases := map[string][][2]string{}
	if len(ali) > 0 {
		if aliases, err = readAliases(ctx, geocodeRaw+ali[len(ali)-1]); err != nil {
			return nil, err
		}
	}
	regions, err := readGeocodes(ctx, geocodeRaw+newest, lat, lon, aliases)
	if err != nil {
		return nil, err
	}
	f := &regionsFile{Source: newest, Lat: lat, Lon: lon, Checked: time.Now(), Regions: regions}
	writeRegionsFile(f)
	slog.Info("alerts: MeteoAlarm's regions near home read", "from", newest, "regions", len(regions))
	return f, nil
}

func readAliases(ctx context.Context, url string) (map[string][][2]string, error) {
	b, err := getLimit(ctx, url, "", 4<<20)
	if err != nil {
		return nil, fmt.Errorf("aliases: %w", err)
	}
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(b), "\uFEFF"))).ReadAll()
	if err != nil || len(rows) == 0 {
		return nil, fmt.Errorf("aliases: %v", err)
	}
	col := map[string]int{}
	for i, h := range rows[0] {
		col[strings.TrimSpace(h)] = i
	}
	ci, ok1 := col["CODE"]
	ai, ok2 := col["ALIAS_CODE"]
	ti, ok3 := col["ALIAS_TYPE"]
	if !ok1 || !ok2 || !ok3 {
		return nil, errors.New("aliases: not the columns expected")
	}
	out := map[string][][2]string{}
	for _, r := range rows[1:] {
		if len(r) <= max(ci, ai, ti) {
			continue
		}
		code := strings.TrimSpace(r[ci])
		out[code] = append(out[code], [2]string{strings.TrimSpace(r[ti]), strings.TrimSpace(r[ai])})
	}
	return out, nil
}

// readGeocodes streams MeteoAlarm's GeoJSON, a feature at a time, and keeps the regions within reach
// of home with every ring of their outline: the outer ones and their holes.
func readGeocodes(ctx context.Context, url string, lat, lon float64, aliases map[string][][2]string) ([]regionJS, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := maClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("geocodes: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("geocodes: %s", resp.Status)
	}
	dec := json.NewDecoder(io.LimitReader(resp.Body, 256<<20))
	// Down to the features: {"crs":..., "features": [ ... ], "type": ...}.
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, errors.New("geocodes: not a GeoJSON object")
	}
	for {
		t, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("geocodes: %w", err)
		}
		if t == json.Delim('}') {
			return nil, errors.New("geocodes: no features")
		}
		if t == "features" {
			break
		}
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return nil, fmt.Errorf("geocodes: %w", err)
		}
	}
	if t, err := dec.Token(); err != nil || t != json.Delim('[') {
		return nil, errors.New("geocodes: features is not a list")
	}
	var out []regionJS
	for dec.More() {
		var f struct {
			Properties struct {
				Code    string `json:"code"`
				Country string `json:"country"`
				Name    string `json:"name"`
				Type    string `json:"type"`
			} `json:"properties"`
			Geometry struct {
				Type        string          `json:"type"`
				Coordinates json.RawMessage `json:"coordinates"`
			} `json:"geometry"`
		}
		if err := dec.Decode(&f); err != nil {
			return nil, fmt.Errorf("geocodes: %w", err)
		}
		p := f.Properties
		if p.Type != "" && p.Type != "EMMA_ID" {
			continue
		}
		rings := allRings(f.Geometry.Type, f.Geometry.Coordinates)
		if len(rings) == 0 || !boxWithin(boxOf(rings), lat, lon, regionsKeepKm) {
			continue
		}
		code := strings.ToUpper(strings.TrimSpace(p.Code))
		name := strings.TrimSpace(p.Name)
		if n, ok := regionNames[code]; ok {
			name = n
		}
		out = append(out, regionJS{Code: code, Country: strings.ToUpper(strings.TrimSpace(p.Country)), Name: name,
			Aliases: aliases[code], Rings: rings})
	}
	return out, nil
}

// allRings are every ring of a GeoJSON Polygon or MultiPolygon, the holes among them, longitude and
// latitude, as they are: a region is told by its full outline.
func allRings(kind string, coords json.RawMessage) [][][2]float64 {
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
		for _, r := range p {
			if len(r) >= 4 {
				out = append(out, r)
			}
		}
	}
	return out
}

// boxOf is the rings' bounding box: west, south, east, north.
func boxOf(rings [][][2]float64) [4]float64 {
	b := [4]float64{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
	for _, r := range rings {
		for _, p := range r {
			b = [4]float64{min(b[0], p[0]), min(b[1], p[1]), max(b[2], p[0]), max(b[3], p[1])}
		}
	}
	return b
}

// boxWithin is whether a box comes within km of a place.
func boxWithin(b [4]float64, lat, lon, km float64) bool {
	dLat := km / 111
	dLon := km / (111 * math.Max(math.Cos(lat*math.Pi/180), 0.1))
	return lon >= b[0]-dLon && lon <= b[2]+dLon && lat >= b[1]-dLat && lat <= b[3]+dLat
}

// mayBeEurope is whether a place may be in a country MeteoAlarm covers, from the Canaries and
// Iceland to Ukraine and Israel: a home with no country is only looked up there.
func mayBeEurope(lat, lon float64) bool {
	return lat >= 27 && lat <= 72 && lon >= -32 && lon <= 45
}
