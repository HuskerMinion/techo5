package home

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/ygelfand/go-esphome-device/api"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/hastate"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
)

// What may be put back on and what may not. A device that restarts what somebody just stopped is
// worse than one that never restarts anything, so the cases that must do nothing are the ones worth
// holding: only the stream that is playing, only a station this device asked for, only so often.
func TestWhatIsWorthPuttingBackOn(t *testing.T) {
	for _, c := range []struct {
		what    string
		set     func(f *Feature)
		ended   string
		wantTry bool
	}{
		{
			what: "the stream that is playing, after a station was asked for",
			set: func(f *Feature) {
				f.url, f.asked, f.askedAt, f.stationURL = "http://p/1", "KXYZ", time.Now(), "http://p/1"
			},
			ended:   "http://p/1",
			wantTry: true,
		},
		{
			what: "a stream that is no longer the one playing",
			set: func(f *Feature) {
				f.url, f.asked, f.askedAt, f.stationURL = "http://p/2", "KXYZ", time.Now(), "http://p/1"
			},
			ended: "http://p/1",
		},
		{
			what: "a song played after the station, with the station still the last asked for",
			set: func(f *Feature) {
				f.url, f.asked, f.askedAt, f.stationURL = "http://p/song", "KXYZ", time.Now(), "http://p/1"
			},
			ended: "http://p/song",
		},
		{
			what:  "a stream nobody asked this device for",
			set:   func(f *Feature) { f.url = "http://p/1" },
			ended: "http://p/1",
		},
		{
			what: "a station asked for so long ago that this is something else",
			set: func(f *Feature) {
				f.url, f.asked, f.askedAt, f.stationURL = "http://p/1", "KXYZ", time.Now().Add(-askedFor-time.Minute), "http://p/1"
			},
			ended: "http://p/1",
		},
		{
			what: "a stream that has already been put back on too many times",
			set: func(f *Feature) {
				f.url, f.asked, f.askedAt, f.stationURL = "http://p/1", "KXYZ", time.Now(), "http://p/1"
				f.resumed, f.resumedAt = resumeTries, time.Now()
			},
			ended: "http://p/1",
		},
	} {
		f := &Feature{}
		c.set(f)
		f.ended(c.ended)

		f.mu.Lock()
		tried := f.resumed > 0 && f.resumedAt.After(time.Time{})
		if c.set != nil && f.resumed == resumeTries && !c.wantTry {
			tried = false // the run it started with, not a try this call made
		}
		f.forgetStation() // the put-back on its timer is not this test's
		f.mu.Unlock()

		if tried != c.wantTry {
			t.Errorf("%s: put back = %t, want %t", c.what, tried, c.wantTry)
		}
	}
}

// The run of tries is forgotten once a stream has stayed up for a while, so an evening of listening
// is not ended by five drops spread across it.
func TestTheRunOfTriesIsForgotten(t *testing.T) {
	f := &Feature{}
	f.url, f.asked, f.askedAt, f.stationURL = "http://p/1", "KXYZ", time.Now(), "http://p/1"
	f.resumed, f.resumedAt = resumeTries, time.Now().Add(-resumeWindow-time.Minute)

	f.ended("http://p/1")

	f.mu.Lock()
	defer f.mu.Unlock()
	f.forgetStation() // the put-back on its timer is not this test's
	if f.resumed != 1 {
		t.Errorf("after the window passed the count is %d, expected a fresh first try", f.resumed)
	}
}

// Live radio is the stream that starts for a station this device asked for, decided as it starts: not
// whatever plays while a station's name lingers on the radio page.
func TestLiveIsTheStreamStartedForAStation(t *testing.T) {
	f := &Feature{}
	if f.liveURL("http://p/song") {
		t.Error("with no station asked for, a stream was taken for live radio")
	}

	f.mu.Lock()
	f.askStation("KXYZ")
	f.meta.station = "KXYZ"
	f.mu.Unlock()
	if !f.liveURL("http://p/1") {
		t.Fatal("the stream that started for a station was not taken for live radio")
	}
	// What follows the station is not it, though the page still names the station.
	if f.liveURL("http://p/clip") {
		t.Error("a clip after the station was taken for live radio")
	}
	if f.liveURL("/local/sleep/rain.mp3") {
		t.Error("a file after the station was taken for live radio")
	}
	// The station's own stream started again (put back on) still is.
	if !f.liveURL("http://p/1") {
		t.Error("the station's stream started again was not taken for live radio")
	}

	// A new station: its stream is, and the one before it no longer is.
	f.mu.Lock()
	f.askStation("KABC")
	f.mu.Unlock()
	if !f.liveURL("http://p/2") || f.liveURL("http://p/1") {
		t.Error("switching stations did not go by the new station's stream")
	}

	// A station asked for long ago whose stream never came: what starts now is something else.
	f.mu.Lock()
	f.askStation("KDEF")
	f.claimAt = time.Now().Add(-liveClaim - time.Second)
	f.mu.Unlock()
	if f.liveURL("http://p/song") {
		t.Error("a stream starting long after a station was asked for was taken for live radio")
	}
}

// A station that will not start after all (not on the list, or Home Assistant said no) leaves no claim
// behind for whatever starts next.
func TestAStationThatDidNotStartClaimsNothing(t *testing.T) {
	f := &Feature{}
	f.mu.Lock()
	f.askStation("KXYZ")
	f.mu.Unlock()
	f.unask("KXYZ")
	if f.liveURL("http://p/song") {
		t.Error("a stream after a station that did not start was taken for live radio")
	}
	// Only the station still asked for: a later ask is not undone by an earlier one failing.
	f.mu.Lock()
	f.askStation("KABC")
	f.mu.Unlock()
	f.unask("KXYZ")
	if !f.liveURL("http://p/2") {
		t.Error("an earlier station's failure undid the claim of the one asked for since")
	}
}

// A station started from Home Assistant itself, which names it in the "last station" text, has its
// stream taken for live radio as one asked for here would; but not the name of one just started here,
// arriving after its stream, and not the value that was there at start.
func TestAStationStartedFromHomeAssistantIsLive(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Home().Radio(config.Radio{Now: "input_text.last_station"}); err != nil {
		t.Fatal(err)
	}
	now := func(v string, first bool) hastate.Update {
		return hastate.Update{Key: hastate.Key{Entity: "input_text.last_station"}, Value: v, First: first}
	}
	f := &Feature{}
	f.stationStarted(now("KXYZ", true))
	if f.liveURL("http://p/song") {
		t.Error("the value at start armed a claim")
	}
	f.stationStarted(now("unavailable", false))
	if f.liveURL("http://p/song") {
		t.Error("an unavailable text armed a claim")
	}
	f.stationStarted(now("KXYZ", false))
	if !f.liveURL("http://p/1") {
		t.Fatal("the stream of a station started from Home Assistant was not taken for live radio")
	}
	// The name arriving just after the stream it names: no second claim for the next stream.
	f.stationStarted(now("KABC", false))
	if f.liveURL("http://p/clip") {
		t.Error("a station's name arriving after its stream armed a claim for the next one")
	}
}

// A Stop in the seconds before a put-back cancels it, as does another station asked for meanwhile.
func TestAStopCancelsAPutBack(t *testing.T) {
	f := &Feature{}
	f.url, f.asked, f.askedAt, f.stationURL = "http://p/1", "KXYZ", time.Now(), "http://p/1"
	if !f.stillWanted("KXYZ", "http://p/1", f.stationGen) {
		t.Fatal("a station that dropped with nothing else going on is not to be put back")
	}
	f.mu.Lock()
	f.forgetStation()
	f.mu.Unlock()
	if f.stillWanted("KXYZ", "http://p/1", f.stationGen) {
		t.Error("a station stopped before its put-back is still put back")
	}
	f.mu.Lock()
	f.asked, f.stationURL = "KABC", "http://p/1"
	f.mu.Unlock()
	if f.stillWanted("KXYZ", "http://p/1", f.stationGen) {
		t.Error("a station replaced by another asked for is still put back")
	}
}

// A station started from Home Assistant is the one put back on when its stream ends, not one asked for
// on the device before it.
func TestAStationStartedFromHomeAssistantIsTheOnePutBack(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Home().Radio(config.Radio{Now: "input_text.last_station"}); err != nil {
		t.Fatal(err)
	}
	f := &Feature{}
	f.mu.Lock()
	f.askStation("KABC")
	f.askedAt = time.Now().Add(-time.Hour)
	f.claiming = false
	f.mu.Unlock()
	f.stationStarted(hastate.Update{Key: hastate.Key{Entity: "input_text.last_station"}, Value: "KXYZ"})
	if !f.liveURL("http://p/2") {
		t.Fatal("the stream of a station started from Home Assistant was not taken for it")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.asked != "KXYZ" {
		t.Errorf("the station to put back is %q, want the one Home Assistant started", f.asked)
	}
}

// The claim made for a name in the house-wide "last station" text holds only seconds; one asked for on
// the device holds longer.
func TestTheClaimForAStationStartedFromHomeAssistantIsShort(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Home().Radio(config.Radio{Now: "input_text.last_station"}); err != nil {
		t.Fatal(err)
	}
	started := func(f *Feature, ago time.Duration) {
		f.stationStarted(hastate.Update{Key: hastate.Key{Entity: "input_text.last_station"}, Value: "KXYZ"})
		f.mu.Lock()
		f.claimAt = time.Now().Add(-ago)
		f.mu.Unlock()
	}
	f := &Feature{}
	started(f, stationClaim-time.Second)
	if !f.liveURL("http://p/1") {
		t.Error("a stream just after Home Assistant named a station was not taken for it")
	}
	f = &Feature{}
	started(f, stationClaim+time.Second)
	if f.liveURL("http://p/1") {
		t.Error("a stream long after Home Assistant named a station was taken for it")
	}
	f = &Feature{}
	f.mu.Lock()
	f.askStation("KABC")
	f.claimAt = time.Now().Add(-stationClaim - time.Second)
	f.mu.Unlock()
	if !f.liveURL("http://p/2") {
		t.Error("a station asked for on the device lost its claim as soon as one named by Home Assistant")
	}
}

// A station the device plays itself (its own, one from the setup page, one the voice assistant found)
// is put back on when its stream ends by itself, as one Home Assistant sends is: the action button's
// way in (PlayOwn) included, which does not go through Play.
func TestAStationTheDevicePlaysItselfIsPutBack(t *testing.T) {
	played := ownPlayer(t)
	f := &Feature{}
	if !f.PlayOwn("KXYZ") {
		t.Fatal("the device's own station did not play")
	}
	if got := played(); len(got) != 1 || got[0] != "http://p/own" {
		t.Fatalf("played %v", got)
	}
	if !f.liveURL("http://p/own") {
		t.Fatal("the device's own station, started by PlayOwn, was not taken for live radio")
	}
	f.ended("http://p/own")
	f.mu.Lock()
	tried := f.resumed
	f.forgetStation() // the put-back on its timer is not this test's
	f.mu.Unlock()
	if tried != 1 {
		t.Fatalf("a station the device played itself was put back %d times, want once", tried)
	}
	// The put-back itself, as its timer makes it: from the same address.
	f.mu.Lock()
	f.askStation("KXYZ")
	f.playingOwn("KXYZ", "http://p/own")
	f.stationURL = "http://p/own"
	gen := f.stationGen
	f.mu.Unlock()
	f.putBack("KXYZ", "http://p/own", true, gen)
	if got := played(); len(got) != 2 || got[1] != "http://p/own" {
		t.Fatalf("the put-back played %v", got)
	}
}

// ownPlayer keeps one station on the device (KXYZ at http://p/own), records what the device is asked
// to play itself instead of playing it, and has nothing else playing.
func ownPlayer(t *testing.T) func() []string {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := SetOwnStations([]config.Station{{Name: "KXYZ", URL: "http://p/own"}}); err != nil {
		t.Fatal(err)
	}
	oldPlay, oldBusy := playURL, mediaBusy
	t.Cleanup(func() { playURL, mediaBusy = oldPlay, oldBusy })
	var mu sync.Mutex
	var played []string
	playURL = func(url string) {
		mu.Lock()
		played = append(played, url)
		mu.Unlock()
	}
	mediaBusy = func() bool { return false }
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(played)
	}
}

// A put-back is not made after a Stop (here or anywhere the player was stopped), after another station
// was asked for, or while something else plays.
func TestAPutBackGivesWay(t *testing.T) {
	played := ownPlayer(t)
	ready := func() (*Feature, uint64) {
		f := &Feature{}
		f.mu.Lock()
		f.askStation("KXYZ")
		f.playingOwn("KXYZ", "http://p/own")
		f.stationURL = "http://p/own"
		gen := f.stationGen
		f.mu.Unlock()
		return f, gen
	}
	f, gen := ready()
	f.mu.Lock()
	f.forgetStation()
	f.mu.Unlock()
	f.putBack("KXYZ", "http://p/own", true, gen)

	f, gen = ready()
	f.mu.Lock()
	f.askStation("KXYZ") // the same station asked for again meanwhile: that ask stands, not this
	f.mu.Unlock()
	f.putBack("KXYZ", "http://p/own", true, gen)

	f, gen = ready()
	mediaBusy = func() bool { return true }
	f.putBack("KXYZ", "http://p/own", true, gen)
	if got := played(); len(got) != 0 {
		t.Errorf("put back %v", got)
	}
}

// A claim asked for on the device and still waiting for its stream is not replaced by a name in the
// house's "last station" text: it keeps its time, and its station.
func TestAPendingClaimKeepsItsTimeAndName(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Home().Radio(config.Radio{Now: "input_text.last_station"}); err != nil {
		t.Fatal(err)
	}
	f := &Feature{}
	f.mu.Lock()
	f.askStation("KABC")
	f.mu.Unlock()
	f.stationStarted(hastate.Update{Key: hastate.Key{Entity: "input_text.last_station"}, Value: "KXYZ"})
	f.mu.Lock()
	f.claimAt = time.Now().Add(-8 * time.Second)
	f.mu.Unlock()
	if !f.liveURL("http://p/1") {
		t.Fatal("a claim asked for here lost its time to a name in the house's text")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.asked != "KABC" {
		t.Errorf("the station is %q, renamed by the house's text", f.asked)
	}
}

// A station the voice assistant asked for that does not play leaves neither its name on what plays
// nor its address to put back.
func TestAStationThatDidNotPlayLeavesNoName(t *testing.T) {
	old := playURLChecked
	t.Cleanup(func() { playURLChecked = old })
	playURLChecked = func(string, time.Duration) error { return errors.New("nothing arrived") }
	f := &Feature{}
	if err := f.PlayStreamChecked("KXYZ", "http://p/gone", time.Second); err == nil {
		t.Fatal("a station that did not play said it did")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.urlName != "" || f.ownURL != "" {
		t.Errorf("a station that did not play left name %q, address %q", f.urlName, f.ownURL)
	}
}

// A station the voice assistant asked for that never made a sound is forgotten: its end is not put
// back on.
func TestAStationThatDidNotPlayIsNotPutBack(t *testing.T) {
	old := playURLChecked
	t.Cleanup(func() { playURLChecked = old })
	f := &Feature{}
	playURLChecked = func(url string, _ time.Duration) error {
		// The stream is taken for the station as it starts, then fails.
		f.liveURL(url)
		return errors.New("nothing arrived")
	}
	if err := f.PlayStreamChecked("KXYZ", "http://p/gone", time.Second); err == nil {
		t.Fatal("a station that did not play said it did")
	}
	if f.stillWanted("KXYZ", "http://p/gone", f.stationGen) {
		t.Error("a station that never played is still to be put back on")
	}
}

// A stop while a station is being started here (its claim still fresh) keeps the claim, so the
// stream that follows is still live radio; it only calls off a put-back on its way. A stop later
// forgets the station.
func TestAStopWhileAStationStartsKeepsTheClaim(t *testing.T) {
	f := &Feature{}
	f.mu.Lock()
	f.askStation("KXYZ")
	gen := f.stationGen
	f.stopped()
	claiming, asked := f.claiming, f.asked
	f.mu.Unlock()
	if !claiming || asked != "KXYZ" {
		t.Fatalf("a stop while the station starts dropped it: claiming %v, asked %q", claiming, asked)
	}
	if f.stationGen == gen {
		t.Error("a stop did not call off a put-back on its way")
	}
	f.mu.Lock()
	f.claimAt = time.Now().Add(-time.Hour)
	f.stopped()
	asked = f.asked
	f.mu.Unlock()
	if asked != "" {
		t.Errorf("a stop long after the station started kept it: %q", asked)
	}
}

// A station Home Assistant names a moment after its stream started (a station started by voice) is
// that stream: taken for live radio, made live in the player, and put back on when it ends. But not a
// stream that started long before the name, a phone's audio, or the stream already taken for one.
func TestAStationNamedJustAfterItsStreamStartedIsThatStream(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Home().Radio(config.Radio{Now: "input_text.last_station"}); err != nil {
		t.Fatal(err)
	}
	marked := make(chan string, 4)
	old := markLive
	markLive = func(url string) bool { marked <- url; return true }
	t.Cleanup(func() { markLive = old })
	named := func(f *Feature, v string) {
		f.stationStarted(hastate.Update{Key: hastate.Key{Entity: "input_text.last_station"}, Value: v})
	}
	startedAgo := func(f *Feature, url string, ago time.Duration) {
		f.mu.Lock()
		f.url, f.urlAt = url, time.Now().Add(-ago)
		f.mu.Unlock()
	}

	f := &Feature{}
	startedAgo(f, "http://ha.invalid/api/esphome/ffmpeg_proxy/x/1.wav", time.Second)
	named(f, "WXYZ")
	select {
	case u := <-marked:
		if u != "http://ha.invalid/api/esphome/ffmpeg_proxy/x/1.wav" {
			t.Fatalf("marked %q live, want the stream that had just started", u)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the stream that had just started was not made live")
	}
	f.mu.Lock()
	asked, station := f.asked, f.stationURL
	f.mu.Unlock()
	if asked != "WXYZ" || station != "http://ha.invalid/api/esphome/ffmpeg_proxy/x/1.wav" {
		t.Fatalf("asked %q, station url %q", asked, station)
	}
	if !f.liveURL("http://ha.invalid/api/esphome/ffmpeg_proxy/x/1.wav") {
		t.Error("the same stream fetched again is not live")
	}
	if !f.stillWanted("WXYZ", "http://ha.invalid/api/esphome/ffmpeg_proxy/x/1.wav", f.stationGen) {
		t.Error("the station's end would not be put back on")
	}

	for name, start := range map[string]func(f *Feature){
		"a stream that started long before the name": func(f *Feature) { startedAgo(f, "http://p/song", time.Minute) },
		"a phone's audio": func(f *Feature) { startedAgo(f, media.BluetoothPrefix+"Pixel", time.Second) },
		"nothing playing": func(*Feature) {},
	} {
		f := &Feature{}
		start(f)
		named(f, "WXYZ")
		select {
		case u := <-marked:
			t.Errorf("%s: %q was made live", name, u)
		case <-time.After(50 * time.Millisecond):
		}
		f.mu.Lock()
		claiming, station := f.claiming, f.stationURL
		f.mu.Unlock()
		if station != "" || !claiming {
			t.Errorf("%s: station url %q, claiming %v; want the next stream claimed instead", name, station, claiming)
		}
	}
}

// The station script turning on is a station started, though the house's text did not change (the
// same station again, or a script that never writes it): the stream that just started is taken for it,
// or, with none yet, the next one is; the station is the one the text names. Its turning off, and a
// station service that is not a script, are nothing.
func TestTheStationScriptStartingIsAStationStarted(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	const text, script = "input_text.script_test_last_station", "script.script_test_play_station"
	if err := config.Set().Home().Radio(config.Radio{Now: text, Service: script}); err != nil {
		t.Fatal(err)
	}
	if err := hastate.Get().Handle(context.Background(), nil,
		&api.HomeAssistantStateResponse{EntityId: text, State: "WXYZ"}); err != nil {
		t.Fatal(err)
	}
	marked := make(chan string, 4)
	old := markLive
	markLive = func(url string) bool { marked <- url; return true }
	t.Cleanup(func() { markLive = old })
	run := func(f *Feature, v string) {
		f.stationStarted(hastate.Update{Key: hastate.Key{Entity: script}, Value: v})
	}

	// The stream came first.
	f := &Feature{}
	f.url, f.urlAt = "http://ha.invalid/api/esphome/ffmpeg_proxy/x/a.wav", time.Now().Add(-time.Second)
	run(f, "on")
	select {
	case u := <-marked:
		if u != "http://ha.invalid/api/esphome/ffmpeg_proxy/x/a.wav" {
			t.Fatalf("marked %q", u)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the stream that had just started was not made live")
	}
	f.mu.Lock()
	if f.asked != "WXYZ" || f.stationURL != "http://ha.invalid/api/esphome/ffmpeg_proxy/x/a.wav" {
		t.Errorf("asked %q, station url %q", f.asked, f.stationURL)
	}
	f.mu.Unlock()

	// The script came first.
	f = &Feature{}
	run(f, "on")
	if !f.liveURL("http://ha.invalid/api/esphome/ffmpeg_proxy/x/b.wav") {
		t.Fatal("the stream after the station script started was not taken for live radio")
	}
	f.mu.Lock()
	if f.asked != "WXYZ" {
		t.Errorf("asked %q, want the station the text names", f.asked)
	}
	f.mu.Unlock()

	// Turning off, and a service that is not a script.
	f = &Feature{}
	run(f, "off")
	if f.liveURL("http://ha.invalid/api/esphome/ffmpeg_proxy/x/c.wav") {
		t.Error("the script turning off armed a claim")
	}
	if err := config.Set().Home().Radio(config.Radio{Now: text, Service: "media_player.somewhere"}); err != nil {
		t.Fatal(err)
	}
	f = &Feature{}
	f.stationStarted(hastate.Update{Key: hastate.Key{Entity: "media_player.somewhere"}, Value: "on"})
	if f.liveURL("http://ha.invalid/api/esphome/ffmpeg_proxy/x/d.wav") {
		t.Error("a station service that is not a script armed a claim")
	}
}

// The station script starting reads the text as it is, which may still name the station before; a
// name arriving just after is the one, and the stream goes back on by its url whatever it is called.
func TestALaterNameReplacesTheOneTheScriptFound(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	const text, script = "input_text.later_test_last_station", "script.later_test_play_station"
	if err := config.Set().Home().Radio(config.Radio{Now: text, Service: script}); err != nil {
		t.Fatal(err)
	}
	if err := hastate.Get().Handle(context.Background(), nil,
		&api.HomeAssistantStateResponse{EntityId: text, State: "KOLD"}); err != nil {
		t.Fatal(err)
	}
	old := markLive
	markLive = func(string) bool { return true }
	t.Cleanup(func() { markLive = old })
	const url = "http://ha.invalid/api/esphome/ffmpeg_proxy/x/new.wav"

	// The script first, then its stream, then the new name.
	f := &Feature{}
	f.stationStarted(hastate.Update{Key: hastate.Key{Entity: script}, Value: "on"})
	if !f.liveURL(url) {
		t.Fatal("the stream after the script was not taken for the station")
	}
	f.stationStarted(hastate.Update{Key: hastate.Key{Entity: text}, Value: "WNEW"})
	f.mu.Lock()
	asked, own := f.asked, f.ownURL
	f.mu.Unlock()
	if asked != "WNEW" {
		t.Errorf("asked %q, want the name that arrived after", asked)
	}
	if own != url {
		t.Errorf("own url %q: the station would be put back by a name, not its url", own)
	}

	// The script first, its new name before the stream.
	f = &Feature{}
	f.stationStarted(hastate.Update{Key: hastate.Key{Entity: script}, Value: "on"})
	f.stationStarted(hastate.Update{Key: hastate.Key{Entity: text}, Value: "WNEW"})
	if !f.liveURL(url) {
		t.Fatal("the stream was not taken for the station")
	}
	f.mu.Lock()
	asked = f.asked
	f.mu.Unlock()
	if asked != "WNEW" {
		t.Errorf("asked %q, want the name that arrived before the stream", asked)
	}
}

// A stream that is not Home Assistant's (this device's own, a camera's sound) is not adopted when the
// station script starts somewhere in the house.
func TestOnlyHomeAssistantsStreamIsAdopted(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	const script = "script.adopt_test_play_station"
	if err := config.Set().Home().Radio(config.Radio{Service: script}); err != nil {
		t.Fatal(err)
	}
	marked := make(chan string, 1)
	old := markLive
	markLive = func(u string) bool { marked <- u; return true }
	t.Cleanup(func() { markLive = old })
	f := &Feature{}
	f.url, f.urlAt = "http://camera.invalid/sound.wav", time.Now()
	f.stationStarted(hastate.Update{Key: hastate.Key{Entity: script}, Value: "on"})
	select {
	case u := <-marked:
		t.Fatalf("%q was adopted", u)
	case <-time.After(50 * time.Millisecond):
	}
}
