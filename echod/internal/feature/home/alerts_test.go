package home

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeNWS answers the four things alerts.go asks: home's state, the alerts at a point, the alerts in
// some states, and a zone's shape; it counts the zone fetches.
type fakeNWS struct {
	mu       sync.Mutex
	zoneHits int
	here     string // the point list's features
	states   string // the states list's features
	areas    string // the area= it was last asked for
}

func (n *fakeNWS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasPrefix(r.URL.Path, "/points/"):
		fmt.Fprint(w, `{"properties":{"relativeLocation":{"properties":{"state":"NE"}}}}`)
	case r.URL.Path == "/alerts/active" && r.URL.Query().Get("point") != "":
		fmt.Fprintf(w, `{"features":[%s]}`, n.here)
	case r.URL.Path == "/alerts/active":
		n.mu.Lock()
		n.areas = r.URL.Query().Get("area")
		n.mu.Unlock()
		fmt.Fprintf(w, `{"features":[%s]}`, n.states)
	case strings.HasPrefix(r.URL.Path, "/zones/"):
		n.mu.Lock()
		n.zoneHits++
		n.mu.Unlock()
		fmt.Fprint(w, `{"geometry":{"type":"Polygon","coordinates":[[[-96,41],[-95,41],[-95,42],[-96,42],[-96,41]]]}}`)
	default:
		http.NotFound(w, r)
	}
}

func feature(id, event, severity, msg, ends, geometry, zone string) string {
	return fmt.Sprintf(`{"id":%q,"geometry":%s,"properties":{"event":%q,"severity":%q,"messageType":%q,
		"ends":%q,"expires":%q,"senderName":"NWS Omaha/Valley NE","areaDesc":"Douglas",
		"description":"* WHAT...Gusts to 50 mph.\n\n* WHERE...Douglas\ncounty.","instruction":"Hold on.",
		"affectedZones":["%s/zones/county/%s"]}}`, id, geometry, event, severity, msg, ends, ends, "SERVER", zone)
}

func TestAlertsAtHomeAndNearby(t *testing.T) {
	n := &fakeNWS{}
	srv := httptest.NewServer(n)
	defer srv.Close()
	defer func(a, d string) { nwsAPI, mapDir = a, d }(nwsAPI, mapDir)
	nwsAPI, mapDir = srv.URL, t.TempDir()

	later := time.Now().Add(6 * time.Hour).UTC().Format(time.RFC3339)
	gone := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	storm := `{"type":"Polygon","coordinates":[[[-96.1,41.2],[-95.9,41.2],[-95.9,41.4],[-96.1,41.2]]]}`
	wind := feature("w1", "Wind Advisory", "Moderate", "Update", later, "null", "NEC055")
	// An update names the alert it replaces: while both are listed, only the update counts.
	wind2 := strings.Replace(feature("w2", "Wind Advisory", "Moderate", "Alert", later, "null", "NEC055"),
		`"properties":{`, `"properties":{"references":[{"@id":"w1"}],`, 1)
	tstorm := feature("t1", "Severe Thunderstorm Warning", "Severe", "Alert", later, storm, "NEC055")
	canceled := feature("c1", "Flood Watch", "Moderate", "Cancel", later, "null", "NEC055")
	over := feature("o1", "Heat Advisory", "Moderate", "Alert", gone, "null", "NEC055")
	minorAway := feature("m1", "Frost Advisory", "Minor", "Alert", later, "null", "IAC001")
	n.here = strings.ReplaceAll(strings.Join([]string{wind, wind2, tstorm, canceled, over}, ","), "SERVER", srv.URL)
	n.states = strings.ReplaceAll(strings.Join([]string{wind, wind2, tstorm, canceled, over, minorAway}, ","), "SERVER", srv.URL)

	f := &Feature{}
	v, err := f.buildAlertsAt(41.26, -95.94)
	if err != nil {
		t.Fatal(err)
	}
	if n.areas == "" || !strings.HasPrefix(n.areas, "NE,") || !strings.Contains(n.areas, "IA") {
		t.Errorf("asked about %q, want Nebraska and its neighbors", n.areas)
	}
	var here []string
	for _, a := range v.Here {
		here = append(here, a.Event)
	}
	if strings.Join(here, "|") != "Severe Thunderstorm Warning|Wind Advisory" {
		t.Errorf("at home: %v, want the warning then one Wind Advisory (the canceled and the ended left out)", here)
	}
	for _, a := range v.Near {
		if a.Event == "Frost Advisory" {
			t.Error("a minor advisory away from home had its zones fetched and drawn")
		}
		if a.Event == "Severe Thunderstorm Warning" && (!a.Storm || len(a.Rings) != 1) {
			t.Errorf("the warning's own polygon: storm %v, %d rings", a.Storm, len(a.Rings))
		}
		if a.Event == "Wind Advisory" && (a.Storm || len(a.Rings) == 0) {
			t.Error("the advisory did not get its zone's shape")
		}
	}
	if w := v.Here[1]; w.Description != "What: Gusts to 50 mph.\nWhere: Douglas county." || w.Color != AlertColor("Wind Advisory", "") {
		t.Errorf("the text reads %q, colored %v", w.Description, w.Color)
	}

	// The zone is kept: another refresh, and a restart, ask for it no more.
	hits := n.zoneHits
	if _, err := f.buildAlertsAt(41.26, -95.94); err != nil || n.zoneHits != hits {
		t.Errorf("a second refresh fetched the zone again (err %v)", err)
	}
	if _, err := (&Feature{}).buildAlertsAt(41.26, -95.94); err != nil || n.zoneHits != hits {
		t.Errorf("after a restart the zone was fetched again instead of read from disk (err %v)", err)
	}
}

func TestNoAlertsOutsideTheUS(t *testing.T) {
	defer func(a string) { nwsAPI = a }(nwsAPI)
	nwsAPI = "http://127.0.0.1:1" // nothing may be asked
	v, err := (&Feature{}).buildAlertsAt(51.5, -0.12)
	if err != nil || len(v.Here)+len(v.Near) != 0 {
		t.Errorf("London: %v, %v", v, err)
	}
	for _, p := range [][2]float64{{61.2, -149.9}, {21.3, -157.9}, {18.4, -66.1}, {41.26, -95.94}} {
		if !inUS(p[0], p[1]) {
			t.Errorf("%v not counted as where the NWS issues alerts", p)
		}
	}
}

// A paragraph the NWS starts "* ..." with nothing before the dots (it happens) is left as it is, not
// read as a heading: that took the daemon down, and again after every restart while the alert stood.
func TestTidyTextSurvivesAnEmptyHeading(t *testing.T) {
	for in, want := range map[string]string{
		"* ...more to follow":      "* ...more to follow",
		"* WHAT...Snow.":           "What: Snow.",
		"*":                        "*",
		"* ...":                    "* ...",
		"* What...lower case head": "* What...lower case head",
	} {
		if got := tidyText(in); got != want {
			t.Errorf("tidyText(%q) = %q, want %q", in, got, want)
		}
	}
}

// An alert that ends while the screen is dark is gone the next time anybody looks, fetched or not.
func TestEndedAlertsLeaveAtOnce(t *testing.T) {
	now := time.Now()
	v := AlertView{Here: []Alert{{ID: "a", Gone: now.Add(-time.Minute)}, {ID: "b", Gone: now.Add(time.Hour)}, {ID: "c"}}}
	v.Near = v.Here
	got := v.without(now)
	if len(got.Here) != 2 || got.Here[0].ID != "b" || len(got.Near) != 2 {
		t.Errorf("after the end: %+v", got)
	}
}

// Where the NWS has no forecast point (a 404 from /points), it is not asked again until home moves.
func TestNoForecastPointIsRemembered(t *testing.T) {
	var points, alerts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/points/") {
			points++
			http.NotFound(w, r)
			return
		}
		alerts++
		fmt.Fprint(w, `{"features":[]}`)
	}))
	defer srv.Close()
	defer func(a string) { nwsAPI = a }(nwsAPI)
	nwsAPI = srv.URL
	f := &Feature{}
	for i := 0; i < 3; i++ {
		if v, err := f.buildAlertsAt(48.95, -97.2); err != nil || len(v.Here) != 0 {
			t.Fatalf("try %d: %v %v", i, v, err)
		}
	}
	if points != 1 || alerts != 0 {
		t.Errorf("asked /points %d times and for alerts %d, want once and never", points, alerts)
	}
	if _, err := f.buildAlertsAt(48.90, -97.2); err != nil || points != 2 {
		t.Errorf("after home moved, /points asked %d times, want again (err %v)", points, err)
	}
}

// The boxes: Alaska without the Yukon, and Home Assistant's country over any box.
func TestWhereTheNWSIssuesAlerts(t *testing.T) {
	for _, tc := range []struct {
		name     string
		lat, lon float64
		country  string
		want     bool
	}{
		{"Anchorage", 61.2, -149.9, "", true}, {"Juneau", 58.3, -134.4, "", true},
		{"Whitehorse", 60.72, -135.06, "", false}, {"Adak", 51.88, -176.65, "", true},
		{"Omaha", 41.26, -95.94, "US", true}, {"Toronto, set to Canada", 43.65, -79.38, "CA", false},
		{"Tijuana, set to Mexico", 32.5, -117.0, "MX", false}, {"San Juan", 18.4, -66.1, "PR", true},
	} {
		country.Lock()
		country.code, country.asked = tc.country, time.Now()
		country.Unlock()
		if got := inUS(tc.lat, tc.lon); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
	country.Lock()
	country.code, country.asked = "", time.Time{}
	country.Unlock()
}
