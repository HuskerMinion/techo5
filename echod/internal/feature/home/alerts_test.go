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
	wind2 := feature("w2", "Wind Advisory", "Moderate", "Alert", later, "null", "NEC055")
	tstorm := feature("t1", "Severe Thunderstorm Warning", "Severe", "Alert", later, storm, "NEC055")
	cancelled := feature("c1", "Flood Watch", "Moderate", "Cancel", later, "null", "NEC055")
	over := feature("o1", "Heat Advisory", "Moderate", "Alert", gone, "null", "NEC055")
	minorAway := feature("m1", "Frost Advisory", "Minor", "Alert", later, "null", "IAC001")
	n.here = strings.ReplaceAll(strings.Join([]string{wind, wind2, tstorm, cancelled, over}, ","), "SERVER", srv.URL)
	n.states = strings.ReplaceAll(strings.Join([]string{wind, wind2, tstorm, cancelled, over, minorAway}, ","), "SERVER", srv.URL)

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
		t.Errorf("at home: %v, want the warning then one Wind Advisory (the cancelled and the ended left out)", here)
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
