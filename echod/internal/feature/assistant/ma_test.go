package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/radiobrowser"
)

// fakeLibrary is a Music Assistant: this device's player is connected or not, and starts playing
// what it is given after a few looks, or never.
type fakeLibrary struct {
	mu        sync.Mutex
	available bool
	startsIn  int // looks at the player before it says playing; -1 for never
	looks     int
	played    []string
}

func (f *fakeLibrary) serve(t *testing.T) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Command string         `json:"command"`
			Args    map[string]any `json:"args"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		defer f.mu.Unlock()
		switch body.Command {
		case "players/all":
			state := "idle"
			if len(f.played) > 0 {
				f.looks++
				if f.startsIn >= 0 && f.looks > f.startsIn {
					state = "playing"
				}
			}
			json.NewEncoder(w).Encode([]map[string]any{{"player_id": "upc1", "available": f.available, "playback_state": state,
				"output_protocols": []map[string]any{{"output_protocol_id": "aa:bb:cc:dd:ee:ff"}}}})
		case "player_queues/play_media":
			if body.Args["queue_id"] != "upc1" {
				t.Errorf("played on %v, not the device's player", body.Args["queue_id"])
			}
			f.played = append(f.played, body.Args["media"].(string))
			w.Write([]byte("null"))
		default:
			w.Write([]byte("null"))
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func withLibrary(t *testing.T, f *fakeLibrary) {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	tok := "tok"
	if err := config.Set().MusicAssistant().Server(f.serve(t), &tok); err != nil {
		t.Fatal(err)
	}
	wasID, wasWait, wasPoll, wasReady := playerID, maStartWait, maPoll, libraryReady
	playerID = func() ([]string, error) { return []string{"aa:bb:cc:dd:ee:ff"}, nil }
	maStartWait, maPoll = 300*time.Millisecond, 10*time.Millisecond
	libraryReady = func() bool { return f.available }
	t.Cleanup(func() { playerID, maStartWait, maPoll, libraryReady = wasID, wasWait, wasPoll, wasReady })
}

// What the library is asked to play counts only once it is playing here, and a device the library
// has no connection to is told as that, not as the music failing.
func TestPlayingThroughTheLibraryIsChecked(t *testing.T) {
	ctx := context.Background()

	f := &fakeLibrary{available: false}
	withLibrary(t, f)
	if err := playOnMA(ctx, "library://artist/1"); err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Errorf("an unconnected device: %v", err)
	}
	if len(f.played) != 0 {
		t.Error("asked an unconnected device to play")
	}

	f = &fakeLibrary{available: true, startsIn: 2}
	withLibrary(t, f)
	if err := playOnMA(ctx, "library://artist/1"); err != nil {
		t.Errorf("a device that started: %v", err)
	}

	f = &fakeLibrary{available: true, startsIn: -1}
	withLibrary(t, f)
	if err := playOnMA(ctx, "library://artist/1"); err == nil || !strings.Contains(err.Error(), "nothing started") {
		t.Errorf("a device that never started: %v", err)
	}
}

// fakeDirectory is Radio Browser, answering by where a search looks: nothing within reach, the
// station in the state, and a station of the same name abroad anywhere else.
func fakeDirectory(t *testing.T) *[]string {
	var mu sync.Mutex
	asked := &[]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		mu.Lock()
		*asked = append(*asked, r.URL.RawQuery)
		mu.Unlock()
		local := `[{"name":"106.7 KBPI","url_resolved":"https://203.0.113.7/kbpi.aac","state":"Colorado","country":"The United States Of America","codec":"AAC"}]`
		abroad := `[{"name":"Zeppelin 106.7","url_resolved":"https://203.0.113.8/zep","state":"Athens","country":"Greece","codec":"MP3"}]`
		switch {
		case q.Get("geo_lat") != "":
			w.Write([]byte("[]"))
		case q.Get("state") == "Colorado" && q.Get("codec") == "":
			w.Write([]byte(local))
		case q.Get("countrycode") != "":
			w.Write([]byte("[]"))
		default:
			w.Write([]byte(abroad))
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(radiobrowser.UseServers(srv.URL))
	return asked
}

func placeDenver(t *testing.T) {
	t.Helper()
	if err := config.Set().Home().Place(config.Place{Name: "Denver, Colorado", Lat: 39.74, Lon: -104.99, Country: "US"}); err != nil {
		t.Fatal(err)
	}
}

// A station by name or frequency is the local one: looked for near the device, then in its state, and
// found there before the same name abroad is ever asked for. With a music library, its stream need not
// be one the device decodes itself, and it is played through the library.
func TestALocalStationIsFoundAndPlayedThroughTheLibrary(t *testing.T) {
	f := &fakeLibrary{available: true, startsIn: 1}
	withLibrary(t, f)
	placeDenver(t)
	asked := fakeDirectory(t)

	got, err := playRadio("106.7")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "KBPI") || strings.Contains(got, " from ") {
		t.Errorf("played %q", got)
	}
	if len(f.played) != 1 || f.played[0] != "https://203.0.113.7/kbpi.aac" {
		t.Errorf("the library was given %v", f.played)
	}
	// The radio page's own list of local stations searches too, with no name: only the searches by
	// name are this one's.
	var byName []string
	for _, q := range *asked {
		if strings.Contains(q, "name=") {
			byName = append(byName, q)
		}
	}
	for _, q := range byName {
		if strings.Contains(q, "codec=") {
			t.Errorf("a search with a library asked only for one codec: %s", q)
		}
	}
	if len(byName) != 2 || !strings.Contains(byName[0], "geo_lat") || !strings.Contains(byName[1], "state=Colorado") {
		t.Errorf("searched %v, want near and then the state", byName)
	}
}

// Without a library, only streams the device decodes itself are asked for, and a station from
// elsewhere is said to be from elsewhere.
func TestWithoutALibraryOnlyPlayableStreamsAreSought(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	placeDenver(t)
	asked := fakeDirectory(t)

	ss, err := findRadio("Zeppelin", "name")
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range *asked {
		if strings.Contains(q, "name=") && !strings.Contains(q, "codec=MP3") {
			t.Errorf("a search without a library asked for any codec: %s", q)
		}
	}
	if len(ss) != 1 || ss[0].Name != "Zeppelin 106.7" {
		t.Fatalf("found %+v", ss)
	}
	if w := whereFrom(ss[0]); w != " from Athens, Greece" {
		t.Errorf("a station abroad is from %q", w)
	}
}

// A frequency is looked for only near the device and in its state: the same frequency elsewhere is
// another station, so not finding it here is the answer, with a way forward.
func TestAFrequencyStaysLocal(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Home().Place(config.Place{Name: "Boise, Idaho", Lat: 43.6, Lon: -116.2, Country: "US"}); err != nil {
		t.Fatal(err)
	}
	asked := fakeDirectory(t)
	_, err := playRadio("106.7 FM")
	if err == nil || !strings.Contains(err.Error(), "near this device") || !strings.Contains(err.Error(), "call letters") {
		t.Errorf("a frequency not found here: %v", err)
	}
	for _, q := range *asked {
		if strings.Contains(q, "name=") && !strings.Contains(q, "geo_lat") && !strings.Contains(q, "state=") {
			t.Errorf("a frequency was looked for beyond the state: %s", q)
		}
	}
	for q, want := range map[string]bool{"106.7": true, "106.7 FM": true, "1080 am": true, "KBPI": false, "Zeppelin 106.7": false, "3": false} {
		if isFrequency(q) != want {
			t.Errorf("isFrequency(%q) = %v", q, !want)
		}
	}
}
