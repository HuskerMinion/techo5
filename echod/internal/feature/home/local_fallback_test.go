package home

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/websocket"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/radiobrowser"
)

// noRadioBrowserHA is a Home Assistant whose every media browse fails with message, as one without
// the Radio Browser integration answers (#752), and whose location is in Lincoln, Nebraska.
func noRadioBrowserHA(t *testing.T, message string) *httptest.Server {
	up := websocket.Upgrader{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/config":
			_ = json.NewEncoder(w).Encode(map[string]any{"latitude": 40.8, "longitude": -96.7, "country": "us"})
			return
		case "/api/websocket":
		default:
			http.NotFound(w, r)
			return
		}
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		_ = c.WriteJSON(map[string]string{"type": "auth_required"})
		var auth map[string]string
		if c.ReadJSON(&auth) != nil {
			return
		}
		_ = c.WriteJSON(map[string]string{"type": "auth_ok"})
		var cmd map[string]any
		if c.ReadJSON(&cmd) != nil {
			return
		}
		_ = c.WriteJSON(map[string]any{"id": cmd["id"], "type": "result", "success": false,
			"error": map[string]string{"code": "browse_media_failed", "message": message}})
	}))
}

// fakeSearch answers Radio Browser's search in its place, keeping the queries asked.
func fakeSearch(t *testing.T) *[]radiobrowser.Query {
	var asked []radiobrowser.Query
	was := searchStations
	searchStations = func(_ context.Context, q radiobrowser.Query) ([]radiobrowser.Station, error) {
		asked = append(asked, q)
		return []radiobrowser.Station{{Name: " KZUM  Community Radio", URL: "http://stream.example/kzum.mp3"}}, nil
	}
	t.Cleanup(func() { searchStations = was })
	return &asked
}

func TestNoRadioBrowserFallsBackToDirect(t *testing.T) {
	srv := noRadioBrowserHA(t, "Failed to browse media with content ID media-source://radio_browser/local: Unknown media source: radio_browser")
	defer srv.Close()
	useHomeAssistant(t, srv)
	asked := fakeSearch(t)
	toldNoRadioBrowser.Store(false)

	// No place on the setup page: Home Assistant's own location is what "near" goes by.
	got, err := browseStations(config.RadioLocal)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "KZUM Community Radio" || got[0].URL != "http://stream.example/kzum.mp3" || got[0].ID != "" {
		t.Fatalf("stations = %+v", got)
	}
	if q := (*asked)[0]; !q.Near || q.Lat != 40.8 || q.Lon != -96.7 || q.RadiusKm != localRadius {
		t.Errorf("local query = %+v", q)
	}
	if !toldNoRadioBrowser.Load() {
		t.Error("the missing integration was not noted")
	}

	// The device's own place wins, and the popular list is its country's.
	if err := config.Set().Home().Place(config.Place{Name: "Omaha", Lat: 41.3, Lon: -96, Country: "CA"}); err != nil {
		t.Fatal(err)
	}
	if _, err := browseStations(config.RadioLocal); err != nil {
		t.Fatal(err)
	}
	if q := (*asked)[1]; q.Lat != 41.3 || q.Lon != -96 {
		t.Errorf("local query with a place = %+v", q)
	}
	if _, err := browseStations(config.RadioPopular); err != nil {
		t.Fatal(err)
	}
	if q := (*asked)[2]; q.Near || q.CountryCode != "CA" {
		t.Errorf("popular query = %+v", q)
	}
}

// A station listed directly carries its stream, so playListed plays it on the device (PlayStream)
// rather than asking Home Assistant for a media id it does not have.
func TestDirectStationPlaysItsStream(t *testing.T) {
	f := &Feature{}
	f.lists.local.stations = []station{{Name: "KZUM", URL: "not a stream"}}
	// An address PlayStream refuses shows the stream path was taken: the media id path would have
	// started a request and said true.
	if f.playListed(config.RadioLocal, "KZUM", false) {
		t.Error("a direct station went another way than its stream")
	}
}

func TestOtherBrowseErrorsStayErrors(t *testing.T) {
	srv := noRadioBrowserHA(t, "Failed to browse media with content ID media-source://radio_browser/local: Connection to radio-browser.info failed")
	defer srv.Close()
	useHomeAssistant(t, srv)
	asked := fakeSearch(t)

	if _, err := browseStations(config.RadioLocal); err == nil {
		t.Fatal("a failing Radio Browser integration was taken for a missing one")
	}
	if len(*asked) != 0 {
		t.Errorf("radio-browser.info was asked %d times", len(*asked))
	}
}
