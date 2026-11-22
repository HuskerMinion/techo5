//go:build !dot

package home

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

func regionCodes(rs []maRegion) []string {
	var out []string
	for _, g := range rs {
		out = append(out, g.code)
	}
	slices.Sort(out)
	return out
}

// Home's regions are the ones it lies in and the sea off it; a neighbor does not count.
func TestRegionsFoundFromWhereHomeIs(t *testing.T) {
	all := loadRegions()
	if len(all) < 1900 {
		t.Fatalf("%d regions read, want MeteoAlarm's two thousand", len(all))
	}
	for _, c := range []struct {
		name     string
		lat, lon float64
		want     []string
	}{
		{"Santander", 43.46, -3.80, []string{"ES133", "ES842"}}, // the coast, and the waters off it
		{"Bremen", 53.08, 8.80, []string{"DE369"}},
		{"Rome", 41.90, 12.50, []string{"IT012"}},
	} {
		if got := regionCodes(regionsNear(all, c.lat, c.lon)); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	if got := regionsNear(all, 35.68, 139.69); len(got) != 0 {
		t.Errorf("Tokyo: %v, want none", regionCodes(got))
	}
	i := slices.IndexFunc(all, func(g maRegion) bool { return g.code == "ES842" })
	if i < 0 || all[i].name != "Costa - Litoral cántabro" {
		t.Errorf("ES842 is called %q", all[i].name)
	}
}

// fakeMeteoAlarm serves countries' feeds and the warnings' CAP documents, and counts what it is asked.
type fakeMeteoAlarm struct {
	mu    sync.Mutex
	feeds map[string]string // by country feed name
	caps  map[string]string // by warning id; absent is a 404
	asked []string
}

func (m *fakeMeteoAlarm) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if a := r.Header.Get("Accept"); a != "" && a != "*/*" {
		// As MeteoAlarm does, for application/atom+xml and application/cap+xml alike.
		http.Error(w, "Not Acceptable", http.StatusNotAcceptable)
		return
	}
	m.mu.Lock()
	m.asked = append(m.asked, r.URL.Path)
	m.mu.Unlock()
	if name, ok := strings.CutPrefix(r.URL.Path, "/feeds/meteoalarm-legacy-atom-"); ok {
		if f, ok := m.feeds[name]; ok {
			fmt.Fprint(w, f)
			return
		}
	}
	if strings.Contains(r.URL.Path, "/warnings/") {
		if c, ok := m.caps[r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]]; ok {
			fmt.Fprint(w, c)
			return
		}
	}
	http.NotFound(w, r)
}

func (m *fakeMeteoAlarm) count(prefix string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, p := range m.asked {
		if strings.HasPrefix(p, prefix) {
			n++
		}
	}
	return n
}

// entry is one feed entry for a region by code (geocode "EMMA_ID:ES133") or by outline ("lat,lon ...").
func entry(srv, id, region, area, title, msg, expires string) string {
	where := ""
	if kind, code, ok := strings.Cut(region, ":"); ok {
		where = fmt.Sprintf(`<cap:geocode><valueName>%s</valueName><value>%s</value></cap:geocode>`, kind, code)
	} else {
		where = `<cap:polygon>` + region + `</cap:polygon>`
	}
	return fmt.Sprintf(`<entry>%s<cap:areaDesc>%s</cap:areaDesc><cap:event>moderate warning</cap:event>
<cap:sent>2026-11-21T10:40:09+00:00</cap:sent><cap:expires>%s</cap:expires><cap:onset>2020-01-01T00:00:00+00:00</cap:onset><cap:severity>Moderate</cap:severity>
<cap:message_type>%s</cap:message_type><cap:status>Actual</cap:status><cap:identifier>id-%s</cap:identifier>
<link type="application/cap+xml" href="%s/api/v1/warnings/feeds-x/%s"/><title>%s</title></entry>`,
		where, area, expires, msg, id, srv, id, title)
}

func feed(entries ...string) string {
	return `<?xml version="1.0" encoding="UTF-8"?><feed xmlns="http://www.w3.org/2005/Atom" xmlns:cap="urn:oasis:names:tc:emergency:cap:1.2">` +
		strings.Join(entries, "\n") + `</feed>`
}

// capDoc is a warning in Spanish and (not quite) English, replacing the warnings it references.
func capDoc(level, kind, expires, references string) string {
	info := func(lang, sender, desc string) string {
		return fmt.Sprintf(`<info><language>%s</language><event>Event %s</event><severity>Severe</severity>
<expires>%s</expires><senderName>%s</senderName><headline>Headline %s</headline>
<description>%s</description><instruction>Line one
line two.</instruction>
<parameter><valueName>awareness_level</valueName><value>%s</value></parameter>
<parameter><valueName>awareness_type</valueName><value>%s</value></parameter></info>`, lang, lang, expires, sender, lang, desc, level, kind)
	}
	return `<?xml version="1.0" encoding="utf-8"?><alert xmlns="urn:oasis:names:tc:emergency:cap:1.2">
<identifier>new</identifier><sent>2026-11-21T11:40:09+01:00</sent><msgType>Update</msgType><references>` + references + `</references>` +
		info("es-ES", "AEMET", "Olas de 4 m.") + info("en-GB", "AEMET", "Waves of 4 m. Mar combinada del noroeste.") + `</alert>`
}

// useConfig is a configuration of the test's own with home at p, and an empty one again after it: the
// NWS's tests that follow go by the country.
func useConfig(t *testing.T, p config.Place) {
	t.Helper()
	after := filepath.Join(t.TempDir(), "after.json")
	t.Cleanup(func() { config.Use(after) })
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Home().Place(p); err != nil {
		t.Fatal(err)
	}
}

func TestMeteoAlarmAlertsAtHome(t *testing.T) {
	useConfig(t, config.Place{Name: "Santander", Lat: 43.46, Lon: -3.80, Country: "ES"})
	m := &fakeMeteoAlarm{caps: map[string]string{}}
	srv := httptest.NewServer(m)
	defer srv.Close()
	defer func(f string) { meteoAlarmFeeds = f }(meteoAlarmFeeds)
	meteoAlarmFeeds = srv.URL + "/feeds/meteoalarm-legacy-atom-"

	later := time.Now().Add(6 * time.Hour).UTC().Format(time.RFC3339)
	gone := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	m.feeds = map[string]string{"spain": feed(
		entry(srv.URL, "coast", "EMMA_ID:ES842", "Costa - Litoral cántabro", "Orange Coastalevent Warning issued for Spain - Costa", "Update", later),
		// The warning the coastal one replaces, still listed.
		entry(srv.URL, "old", "EMMA_ID:ES842", "Costa - Litoral cántabro", "Yellow Coastalevent Warning issued for Spain - Costa", "Alert", later),
		entry(srv.URL, "over", "EMMA_ID:ES133", "Litoral cántabro", "Yellow Wind Warning issued for Spain - Litoral", "Alert", gone),
		entry(srv.URL, "cancel", "EMMA_ID:ES133", "Litoral cántabro", "Yellow Wind Warning issued for Spain - Litoral", "Cancel", later),
		entry(srv.URL, "away", "EMMA_ID:ES242", "Interior norte de Castellón", "Red Wind Warning issued for Spain - Castellón", "Alert", later),
		entry(srv.URL, "green", "EMMA_ID:ES133", "Litoral cántabro", "Green Rain Warning issued for Spain - Litoral", "Alert", later),
		// Its CAP document is gone (404): shown from the feed.
		entry(srv.URL, "rain", "EMMA_ID:ES133", "Litoral cántabro", "Yellow Rain Warning issued for Spain - Litoral", "Alert", later),
	)}
	m.caps["coast"] = capDoc("3; orange; Severe", "7; coastalevent", later, "http://www.aemet.es,id-old,2026-11-20T10:00:00+00:00")
	m.caps["old"] = capDoc("2; yellow; Moderate", "7; coastalevent", later, "")

	f := &Feature{}
	v, err := f.buildMeteoAlarm(context.Background(), 43.46, -3.80)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, a := range v.Here {
		got = append(got, fmt.Sprintf("%s/%d/%d", a.ID[strings.LastIndex(a.ID, "/")+1:], a.Level, a.Kind))
	}
	if strings.Join(got, " ") != "coast/3/7 rain/2/10" {
		t.Fatalf("at home: %v, want the orange coastal warning, then the yellow rain one", got)
	}
	coast := v.Here[0]
	if coast.Color != meteoAlarmColor(levelOrange) || coast.Severity != "Severe" || coast.Area != "Costa - Litoral cántabro" ||
		coast.Source != "MeteoAlarm" || !coast.Onset.IsZero() || coast.Gone.IsZero() {
		t.Errorf("the coastal warning: %+v", coast)
	}

	// In the screen's language when the service issued it in that language, else in English.
	es := coast.inLanguage("es")
	if es.Event != "Event es-ES" || es.Description != "Olas de 4 m." ||
		es.Instruction != "Line one\nline two." || es.Sender != "AEMET · meteoalarm.org" || es.Headline != "Headline es-ES" {
		t.Errorf("in Spanish: %q %q %q %q", es.Event, es.Description, es.Instruction, es.Sender)
	}
	de := coast.inLanguage("de")
	if de.Event != "Event en-GB" || de.Headline != "Headline en-GB" || !strings.HasPrefix(de.Description, "Waves of 4 m.") {
		t.Errorf("in German: %q %q", de.Event, de.Description)
	}
	if coast.Issued.IsZero() {
		t.Error("no time of issue")
	}
	if rain := v.Here[1].inLanguage("en"); rain.Event != "moderate warning" || rain.Headline != "" || !strings.Contains(rain.Sender, "MeteoAlarm") {
		t.Errorf("the rain warning from the feed alone: %q from %q", rain.Event, rain.Sender)
	}

	// The CAP documents are read once; a refresh reads only the feed again.
	capsRead := m.count("/api/")
	if _, err := f.buildMeteoAlarm(context.Background(), 43.46, -3.80); err != nil || m.count("/api/") != capsRead {
		t.Errorf("a second refresh read %d CAP documents more (err %v)", m.count("/api/")-capsRead, err)
	}
}

// A country that sends outlines is matched by home's place: the outlines home lies in.
func TestMeteoAlarmOutlines(t *testing.T) {
	useConfig(t, config.Place{Name: "Zürich", Lat: 47.37, Lon: 8.54, Country: "CH"})
	m := &fakeMeteoAlarm{caps: map[string]string{}}
	srv := httptest.NewServer(m)
	defer srv.Close()
	defer func(f string) { meteoAlarmFeeds = f }(meteoAlarmFeeds)
	meteoAlarmFeeds = srv.URL + "/feeds/meteoalarm-legacy-atom-"
	later := time.Now().Add(6 * time.Hour).UTC().Format(time.RFC3339)
	around := "47.3,8.4 47.3,8.7 47.45,8.7 47.45,8.4 47.3,8.4" // Zürich
	ticino := "46.0,8.8 46.0,9.0 46.2,9.0 46.2,8.8 46.0,8.8"   // 130 km south
	m.feeds = map[string]string{"switzerland": feed(
		entry(srv.URL, "zh", around, "Zürich", "Yellow Snow-ice Warning issued for Switzerland - Zürich", "Alert", later),
		entry(srv.URL, "ti", ticino, "Ticino", "Orange Rain Warning issued for Switzerland - Ticino", "Alert", later),
	)}
	f := &Feature{}
	v, err := f.buildMeteoAlarm(context.Background(), 47.37, 8.54)
	if err != nil || len(v.Here) != 1 || v.Here[0].Area != "Zürich" || len(v.Here[0].Rings) != 1 || v.Here[0].Kind != 2 {
		t.Fatalf("Zürich: %+v, %v", v.Here, err)
	}
	if len(v.Near) != 1 {
		t.Errorf("%d alerts for the map, want home's one, outlined", len(v.Near))
	}
}

// France names its regions by NUTS3 code, which MeteoAlarm's alias list ties to the EMMA_ID.
func TestMeteoAlarmAliasCodes(t *testing.T) {
	useConfig(t, config.Place{Name: "Paris", Lat: 48.857, Lon: 2.35, Country: "FR"})
	all := loadRegions()
	h := areaFor(all, 48.857, 2.35, "FR")
	e := maEntry{}
	e.Geocodes = append(e.Geocodes, struct {
		Name  string `xml:"valueName"`
		Value string `xml:"value"`
	}{"NUTS3", "FR101"})
	if !h.at(e, nil) {
		t.Errorf("a warning for NUTS3 FR101 (Paris) is not home's, whose regions are %v", h.names)
	}
	e.Geocodes[0].Value = "FR102" // Seine-et-Marne, next door
	if h.at(e, nil) {
		t.Error("a warning for the district next door counted as home's")
	}
	if !slices.Equal(h.countries, []string{"FR"}) {
		t.Errorf("countries %v", h.countries)
	}
}

// MeteoAlarm sometimes answers with a page that is not a feed: that is an error, tried again soon,
// not a country without warnings.
func TestMeteoAlarmNotAFeed(t *testing.T) {
	useConfig(t, config.Place{Name: "Wien", Lat: 48.21, Lon: 16.37, Country: "AT"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "Internal Server Error")
	}))
	defer srv.Close()
	defer func(f string) { meteoAlarmFeeds = f }(meteoAlarmFeeds)
	meteoAlarmFeeds = srv.URL + "/feeds/meteoalarm-legacy-atom-"
	if _, err := (&Feature{}).buildMeteoAlarm(context.Background(), 48.21, 16.37); err == nil {
		t.Error("a page that is not a feed read as no warnings")
	}
}

func TestAlertSourceFor(t *testing.T) {
	for _, c := range []struct {
		place config.Place
		want  alertSource
	}{
		{config.Place{Lat: 43.46, Lon: -3.80, Country: "ES"}, alertsFromMeteoAlarm},
		{config.Place{Lat: 51.51, Lon: -0.13, Country: "GB"}, alertsFromMeteoAlarm}, // outlines, no regions
		{config.Place{Lat: 41.26, Lon: -95.94, Country: "US"}, alertsFromNWS},
		{config.Place{Lat: 35.68, Lon: 139.69, Country: "JP"}, alertsFromNone},
		{config.Place{Lat: 53.08, Lon: 8.80}, alertsFromMeteoAlarm}, // no country: one of MeteoAlarm's regions
		{config.Place{Lat: 41.26, Lon: -95.94}, alertsFromNWS},      // no country: the U.S.
		// No country, in a country MeteoAlarm has no regions for: nothing (docs/overview.md says so).
		{config.Place{Lat: 51.51, Lon: -0.13}, alertsFromNone},
	} {
		c.place.Name = "Home"
		useConfig(t, c.place)
		if got := alertSourceFor(c.place.Lat, c.place.Lon); got != c.want {
			t.Errorf("%+v: %v, want %v", c.place, got, c.want)
		}
	}
	if alertsFromMeteoAlarm.every() != 10*time.Minute || alertsFromNWS.every() != alertsEvery {
		t.Error("the sources' paces")
	}
}

func TestFromTitle(t *testing.T) {
	for title, want := range map[string][2]int{
		"Orange Coastalevent Warning issued for Spain - Costa": {levelOrange, 7},
		"Yellow Snow-ice Warning issued for Germany - Kreis":   {levelYellow, 2},
		"Red Low-temperature Warning issued for Finland - X":   {levelRed, 6},
		"Yellow Rain-flood Warning issued for Italy - X":       {levelYellow, 13},
		"something else": {0, 0},
	} {
		if l, k := fromTitle(title); l != want[0] || k != want[1] {
			t.Errorf("%q: level %d kind %d, want %v", title, l, k, want)
		}
	}
}
