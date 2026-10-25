package home

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/hastate"
)

// hintRig is a feature whose favorites play through a script, with the wait for a sign from Home
// Assistant cut short, the calls the device makes counted and the notes it puts up kept.
type hintRig struct {
	f     *Feature
	h     config.Radio
	calls chan component.Call
	hints chan Hint
}

func newHintRig(t *testing.T, wait time.Duration) *hintRig {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	h := config.Radio{
		Stations: []string{"input_select.hint_test_stations"}, Service: "script.hint_test_play_station",
		Field: "station", SpeakerField: "speaker", Speaker: "media_player.hint_test_speaker",
	}
	if err := config.Set().Home().Radio(h); err != nil {
		t.Fatal(err)
	}
	old := startWait
	startWait = wait
	t.Cleanup(func() { startWait = old })
	r := &hintRig{f: &Feature{}, h: h, calls: make(chan component.Call, 4), hints: make(chan Hint, 4)}
	t.Cleanup(component.CallService.Listen(func(c component.Call) { r.calls <- c }))
	r.f.Hint.Listen(func(h Hint) {
		select {
		case r.hints <- h:
		default: // a timer of a test that has finished
		}
	})
	return r
}

// ask is a favorite tapped, as play asks for one: the claim on the next stream, the station chosen,
// and the call to the script, which the rig waits to see go out.
func (r *hintRig) ask(t *testing.T, station string) {
	t.Helper()
	r.askWatched(t, station, true)
}

// askWatched is ask, with watch as callFavorite takes it: false for a station the device puts back on.
func (r *hintRig) askWatched(t *testing.T, station string, watch bool) {
	t.Helper()
	r.f.mu.Lock()
	r.f.askStation(station)
	r.f.chosen = station
	r.f.mu.Unlock()
	r.f.callFavorite(r.h, station, watch)
	select {
	case c := <-r.calls:
		if c.Service != r.h.Service || c.Data["station"] != station {
			t.Fatalf("called %+v", c)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the script was not called")
	}
}

func (r *hintRig) chosen() string {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	return r.f.chosen
}

// Home Assistant rejecting the device's call says so only in its own log: with no stream, no script
// turning on and no station named within the wait, the station is no longer shown as starting, and a
// note says what to check.
func TestAStationHomeAssistantNeverStartedSaysSo(t *testing.T) {
	r := newHintRig(t, 50*time.Millisecond)
	r.ask(t, "Example FM")
	select {
	case h := <-r.hints:
		if h.Message != NoStartHint || h.Title == "" {
			t.Errorf("note %+v", h)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no note for a station that never started")
	}
	if c := r.chosen(); c != "" {
		t.Errorf("still shown as starting %q", c)
	}
}

// A working setup shows nothing new: the stream reaching the player in time, or the script turning on
// (a station that takes longer to find), is Home Assistant at work, and no note goes up.
func TestAStationThatStartsInTimeHasNoNote(t *testing.T) {
	for name, sign := range map[string]func(r *hintRig){
		"the stream": func(r *hintRig) { r.f.played("http://ha.invalid/api/esphome/ffmpeg_proxy/x/a.flac") },
		"the script turning on": func(r *hintRig) {
			r.f.stationStarted(hastate.Update{Key: hastate.Key{Entity: r.h.Service}, Value: "on"})
		},
	} {
		r := newHintRig(t, 150*time.Millisecond)
		r.ask(t, "Example FM")
		sign(r)
		select {
		case h := <-r.hints:
			t.Errorf("%s: a note after all: %+v", name, h)
		case <-time.After(400 * time.Millisecond):
		}
	}
}

// No note where it would be wrong: a station the device puts back on by itself after its stream dropped
// (nobody asked for it), a call that went to no Home Assistant at all (none connected, so the question
// about its settings is the wrong one; the station is not left starting either), and Music Assistant
// playing on the device, which is the station arriving through Sendspin rather than as a stream to the
// player.
func TestNoNoteWhereItWouldBeWrong(t *testing.T) {
	quiet := func(t *testing.T, r *hintRig, what string) {
		t.Helper()
		select {
		case h := <-r.hints:
			t.Errorf("%s: a note: %+v", what, h)
		case <-time.After(300 * time.Millisecond):
		}
	}

	r := newHintRig(t, 50*time.Millisecond)
	r.askWatched(t, "Example FM", false)
	quiet(t, r, "a put-back")

	r = newHintRig(t, 50*time.Millisecond)
	stop := component.CallService.Listen(func(c component.Call) {
		if c.Unheard != nil {
			c.Unheard()
		}
	})
	r.ask(t, "Example FM")
	stop()
	quiet(t, r, "no Home Assistant connected")
	if c := r.chosen(); c != "" {
		t.Errorf("no Home Assistant connected: still shown as starting %q", c)
	}

	r = newHintRig(t, 50*time.Millisecond)
	r.f.external = func() bool { return true }
	r.ask(t, "Example FM")
	quiet(t, r, "Music Assistant playing")
}

// Another station tapped, or a Stop, before the wait is out: the note is not for the station before.
func TestOnlyTheStationStillStartingGetsTheNote(t *testing.T) {
	r := newHintRig(t, 150*time.Millisecond)
	r.ask(t, "Example FM")
	r.f.mu.Lock()
	r.f.chosen = ""
	r.f.mu.Unlock()
	select {
	case h := <-r.hints:
		t.Fatalf("a note for a station that was stopped: %+v", h)
	case <-time.After(400 * time.Millisecond):
	}

	r.ask(t, "Example FM")
	r.ask(t, "Sample Jazz")
	select {
	case <-r.hints: // the second station's, which nothing came of either
	case <-time.After(2 * time.Second):
		t.Fatal("no note for the station still starting")
	}
	select {
	case h := <-r.hints:
		t.Errorf("a second note: %+v", h)
	case <-time.After(300 * time.Millisecond):
	}
}

// A stream that comes after the note plays as any other: it is still the station's (live radio, put
// back if it drops), and it takes the note down.
func TestALateStreamStillPlaysAndTakesTheNoteDown(t *testing.T) {
	r := newHintRig(t, 50*time.Millisecond)
	r.ask(t, "Example FM")
	select {
	case <-r.hints:
	case <-time.After(2 * time.Second):
		t.Fatal("no note")
	}
	const url = "http://ha.invalid/api/esphome/ffmpeg_proxy/x/late.flac"
	if !r.f.liveURL(url) {
		t.Error("the late stream was not taken for the station")
	}
	r.f.played(url)
	select {
	case h := <-r.hints:
		if h.Message != "" {
			t.Errorf("want the note taken down, got %+v", h)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the note was not taken down")
	}
	r.f.mu.Lock()
	asked, station := r.f.asked, r.f.stationURL
	r.f.mu.Unlock()
	if asked != "Example FM" || station != url {
		t.Errorf("asked %q, station url %q", asked, station)
	}
	// Taken down once: the next stream has nothing to take down.
	r.f.played("http://ha.invalid/api/esphome/ffmpeg_proxy/x/next.flac")
	select {
	case h := <-r.hints:
		t.Errorf("another note: %+v", h)
	case <-time.After(200 * time.Millisecond):
	}
}

// The star's script refused (#174): a note names the script it needs, and the error still goes back to
// the screen that pressed it. Saved, nothing goes up.
func TestTheStarRefusedSaysWhichScript(t *testing.T) {
	r := newHintRig(t, time.Minute)
	r.f.mu.Lock()
	r.f.url, r.f.urlName = "http://ha.invalid/api/esphome/ffmpeg_proxy/x/a.flac", "Example FM"
	r.f.mu.Unlock()
	old := addFavorite
	t.Cleanup(func() { addFavorite = old })

	var asked string
	addFavorite = func(station string) error {
		asked = station
		return errors.New("hass: POST /api/services/script/radio_favorite_add_station: 400 Bad Request")
	}
	if err := r.f.FavoriteNow(); err == nil {
		t.Fatal("the refusal was not passed on")
	}
	if asked != "Example FM" {
		t.Errorf("saved %q", asked)
	}
	select {
	case h := <-r.hints:
		if h.Message != FavoriteHint {
			t.Errorf("note %+v", h)
		}
	default:
		t.Fatal("no note for the star")
	}

	addFavorite = func(string) error { return nil }
	if err := r.f.FavoriteNow(); err != nil {
		t.Fatal(err)
	}
	select {
	case h := <-r.hints:
		t.Errorf("a note for a saved station: %+v", h)
	default:
	}
}
