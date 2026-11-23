package home

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeGitLab serves MeteoAlarm's documents project as the device asks for it: the listing, and a
// geocode and alias file pair, here a few real regions cut from MeteoAlarm's files (testdata).
type fakeGitLab struct {
	mu      sync.Mutex
	geocode string // the newest geocode file's name
	asked   []string
	geo     []byte
	ali     []byte
}

func (g *fakeGitLab) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	g.asked = append(g.asked, r.URL.Path)
	name := g.geocode
	g.mu.Unlock()
	switch {
	case strings.HasSuffix(r.URL.Path, "/tree"):
		w.Write([]byte(`[{"name":"README.md","type":"blob"},{"name":"MeteoAlarm_Geocodes_2025_08_12.json","type":"blob"},
			{"name":"` + name + `","type":"blob"},{"name":"MeteoAlarm_Geocodes-Aliases_2026_11_13.csv","type":"blob"}]`))
	case strings.HasSuffix(r.URL.Path, "/"+name):
		w.Write(g.geo)
	case strings.HasSuffix(r.URL.Path, ".csv"):
		w.Write(g.ali)
	default:
		http.NotFound(w, r)
	}
}

func (g *fakeGitLab) count(suffix string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	n := 0
	for _, p := range g.asked {
		if strings.HasSuffix(p, suffix) {
			n++
		}
	}
	return n
}

// useSampleRegions points the region download at a server of its own with the sample regions, keeps
// what it writes in the test's own folder, and forgets what an earlier test kept.
func useSampleRegions(t *testing.T) *fakeGitLab {
	t.Helper()
	gz, err := os.ReadFile("testdata/meteoalarm-geocodes-sample.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		t.Fatal(err)
	}
	geo, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	ali, err := os.ReadFile("testdata/meteoalarm-aliases-sample.csv")
	if err != nil {
		t.Fatal(err)
	}
	g := &fakeGitLab{geocode: "MeteoAlarm_Geocodes_2026_11_13.json", geo: geo, ali: ali}
	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	list, raw, rdir := geocodeList, geocodeRaw, regionsDir
	t.Cleanup(func() { geocodeList, geocodeRaw, regionsDir = list, raw, rdir; resetRegionStore() })
	geocodeList, geocodeRaw = srv.URL+"/api/v4/projects/x/repository/tree", srv.URL+"/raw/"
	regionsDir = func() string { return dir }
	resetRegionStore()
	return g
}

func resetRegionStore() {
	regionStore.Lock()
	regionStore.file, regionStore.tried = nil, time.Time{}
	regionStore.Unlock()
}

// Home's regions are the ones it lies in, holes and all, and the sea off it; a neighbor does not
// count, nor a district numbered like a sea area in a country with no sea.
func TestRegionsFoundFromWhereHomeIs(t *testing.T) {
	useSampleRegions(t)
	for _, c := range []struct {
		name     string
		lat, lon float64
		want     []string
	}{
		{"Santander", 43.46, -3.80, []string{"ES133", "ES842"}}, // the coast, and the waters off it
		{"Bremen", 53.08, 8.80, []string{"DE369"}},
		{"Graz", 47.07, 15.44, []string{"AT601"}},       // the city, a hole in Graz-Umgebung
		{"Seiersberg", 47.01, 15.40, []string{"AT606"}}, // Graz-Umgebung itself
		{"Bregenz", 47.50, 9.75, []string{"AT802"}},
		{"Lindau", 47.55, 9.68, nil}, // 6 km from AT802 Bregenz, a district, not the sea
		{"Tokyo", 35.68, 139.69, nil},
	} {
		got := regionCodes(regionsNear(regionsAround(context.Background(), c.lat, c.lon), c.lat, c.lon))
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	all := regionsAround(context.Background(), 43.46, -3.80)
	i := slices.IndexFunc(all, func(g maRegion) bool { return g.code == "ES842" })
	if i < 0 || all[i].name != "Costa - Litoral cántabro" {
		t.Errorf("ES842 is called %q", all[i].name)
	}
}

// The regions are downloaded once for a place and kept; a month on, MeteoAlarm is asked whether
// there are newer files, and they are read only when there are.
func TestRegionsAreKeptAndRefreshed(t *testing.T) {
	g := useSampleRegions(t)
	ctx := context.Background()
	if n := len(regionsAround(ctx, 47.07, 15.44)); n == 0 {
		t.Fatal("no regions for Graz")
	}
	if g.count(".json") != 1 {
		t.Fatalf("the geocode file was asked for %d times, want once", g.count(".json"))
	}
	resetRegionStore() // a restart: read from the device
	regionsAround(ctx, 47.07, 15.44)
	if g.count(".json") != 1 || g.count("/tree") != 1 {
		t.Errorf("after a restart MeteoAlarm was asked again (%v)", g.asked)
	}

	age := func() {
		regionStore.Lock()
		regionStore.file.Checked = time.Now().Add(-regionsCheck - time.Hour)
		regionStore.Unlock()
	}
	age()
	regionsAround(ctx, 47.07, 15.44)
	if g.count("/tree") != 2 || g.count(".json") != 1 {
		t.Errorf("a month on, with the same files: %v, want the listing asked and nothing read", g.asked)
	}
	age()
	g.mu.Lock()
	g.geocode = "MeteoAlarm_Geocodes_2027_02_01.json"
	g.mu.Unlock()
	regionsAround(ctx, 47.07, 15.44)
	if g.count("2027_02_01.json") != 1 {
		t.Errorf("newer files were not read: %v", g.asked)
	}

	// Another place is its own download.
	regionsAround(ctx, 53.08, 8.80)
	if g.count("2027_02_01.json") != 2 {
		t.Errorf("home moved, and its regions were not read: %v", g.asked)
	}
}

// A small outline is matched in full: thinned to the map's kilometer it would miss home.
func TestOutlinesMatchedInFull(t *testing.T) {
	// A sliver along a river, 300 m wide and 4 km long, with home in it, written as CAP does.
	var pts []string
	for i := 0; i <= 40; i++ {
		pts = append(pts, fmtLatLon(50.70+float64(i)*0.001, -3.290))
	}
	for i := 40; i >= 0; i-- {
		pts = append(pts, fmtLatLon(50.70+float64(i)*0.001, -3.286))
	}
	pts = append(pts, pts[0])
	e := maEntry{Polygons: []string{strings.Join(pts, " ")}}
	h := homeArea{codes: map[[2]string]bool{}, lat: 50.7205, lon: -3.2875}
	if !h.at(e, e.outlines()) {
		t.Error("home in the sliver was not matched")
	}
	if thinned := drawn(e.outlines()); len(thinned[0]) >= len(e.outlines()[0]) {
		t.Error("the drawn copy was not thinned")
	}
	far := homeArea{codes: map[[2]string]bool{}, lat: 50.7205, lon: -3.2800}
	if far.at(e, e.outlines()) {
		t.Error("a home 500 m off the sliver was matched")
	}
}

func fmtLatLon(lat, lon float64) string { return fmt.Sprintf("%.4f,%.4f", lat, lon) }
