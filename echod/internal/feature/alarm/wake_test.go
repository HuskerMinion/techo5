package alarm

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/ring"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// music stands in for the speaker's music while an alarm wakes with it (#651): what was started and
// stopped, the levels it was held at, and whether the ring was given its alarm sound.
type music struct {
	mu      sync.Mutex
	started []config.WakeWith
	stops   int
	levels  []int
	sounds  int
	ready   error // what wakeReady says
	refuse  error // what wakeStart says
	plays   bool  // whether a start is heard
	heard   atomic.Bool
	running sync.WaitGroup

	// pauses and resumes count the music paused for a snooze and resumed after it; resumeErr is what a
	// resume says, and resumePlays whether one is heard.
	pauses, resumes int
	resumeErr       error
	resumePlays     bool
	// held is the level the music was held at, -1 once let go, and holds how many times it was held.
	held, holds int
}

func fakeMusic(t *testing.T) *music {
	t.Helper()
	m := &music{plays: true}
	m.resumePlays = true
	was := []any{wakeWithin, wakeEvery, startQuiet, goWake, wakeReady, wakeStart, wakeHeard, wakeStop, wakeLower,
		wakeResume, wakePause, wakeVolume}
	wakeWithin, wakeEvery = 150*time.Millisecond, 5*time.Millisecond
	startQuiet = func(what, label string, ended func()) (func(), func([]speaker.Note, time.Duration)) {
		stop, sound := ring.StartQuiet(what, label, ended)
		return stop, func(notes []speaker.Note, rise time.Duration) {
			m.mu.Lock()
			m.sounds++
			m.mu.Unlock()
			sound(notes, rise)
		}
	}
	goWake = func(f func()) {
		m.running.Add(1)
		go func() { defer m.running.Done(); f() }()
	}
	wakeReady = func(config.WakeWith) error { m.mu.Lock(); defer m.mu.Unlock(); return m.ready }
	wakeStart = func(_ context.Context, w config.WakeWith, _ func() bool) error {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.started = append(m.started, w)
		if m.refuse == nil && m.plays {
			m.heard.Store(true)
		}
		return m.refuse
	}
	wakeHeard = m.heard.Load
	wakeStop = func() { m.mu.Lock(); m.stops++; m.mu.Unlock(); m.heard.Store(false) }
	wakeLower = func(db int) { m.mu.Lock(); m.levels = append(m.levels, db); m.mu.Unlock() }
	wakeResume = func(context.Context) error {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.resumes++
		if m.resumeErr == nil && m.resumePlays {
			m.heard.Store(true)
		}
		return m.resumeErr
	}
	wakePause = func() { m.mu.Lock(); m.pauses++; m.mu.Unlock(); m.heard.Store(false) }
	wakeVolume = func(step int) func() {
		m.mu.Lock()
		m.held, m.holds = step, m.holds+1
		m.mu.Unlock()
		var once sync.Once
		return func() { once.Do(func() { m.mu.Lock(); m.held = -1; m.mu.Unlock() }) }
	}
	t.Cleanup(func() {
		m.running.Wait()
		wakeWithin, wakeEvery = was[0].(time.Duration), was[1].(time.Duration)
		startQuiet = was[2].(func(string, string, func()) (func(), func([]speaker.Note, time.Duration)))
		goWake = was[3].(func(func()))
		wakeReady = was[4].(func(config.WakeWith) error)
		wakeStart = was[5].(func(context.Context, config.WakeWith, func() bool) error)
		wakeHeard, wakeStop, wakeLower = was[6].(func() bool), was[7].(func()), was[8].(func(int))
		wakeResume, wakePause = was[9].(func(context.Context) error), was[10].(func())
		wakeVolume = was[11].(func(int) func())
	})
	return m
}

func (m *music) state() (started []config.WakeWith, stops, sounds int, levels []int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]config.WakeWith(nil), m.started...), m.stops, m.sounds, append([]int(nil), m.levels...)
}

// volume is the level the music is held at (-1 once let go, 0 before it was held), how many times it
// was held, and the pauses and resumes.
func (m *music) volume() (held, holds, pauses, resumes int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.held, m.holds, m.pauses, m.resumes
}

func until(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); time.Sleep(2 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

var radio = config.WakeWith{Kind: config.WakeRadio, Name: "WXYZ", URI: "http://radio.invalid/wxyz.mp3"}

// wakeAlarm is a device alarm that wakes with w, put on a fresh device, and a source of it to fire.
func wakeAlarm(t *testing.T, w config.WakeWith) (*Alarms, source) {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	a := build()
	endRing(t, a)
	// No label: a label is said over the ring on a goroutine of its own, which would outlive the test.
	al := config.Alarm{ID: "wake", Hour: 6, Minute: 30, Days: config.DaysWeekdays, On: true, Wake: w}
	if err := a.Put(al); err != nil {
		t.Fatal(err)
	}
	return a, source{key: "wake", days: al.Days, local: true}
}

// An alarm that wakes with music plays it, and not the alarm sound; it rings until stopped, and the
// stop stops the music. Home Assistant is told what it woke with.
func TestAnAlarmWakesWithItsMusic(t *testing.T) {
	m := fakeMusic(t)
	got := events(t)
	a, src := wakeAlarm(t, radio)
	if err := config.Set().Alarms().RingVolume(9); err != nil {
		t.Fatal(err)
	}
	a.fire(src, time.Now())
	until(t, "the music", func() bool { s, _, _, _ := m.state(); return len(s) == 1 })
	time.Sleep(2 * wakeWithin)
	if held, holds, _, _ := m.volume(); held != 9 || holds != 1 {
		t.Errorf("the music is held at %d (%d times), want the ring volume 9", held, holds)
	}
	started, _, sounds, _ := m.state()
	if started[0] != radio || sounds != 0 || !a.Ringing() || !ring.IsSounding() {
		t.Fatalf("started %+v, %d alarm sounds, ringing %v", started, sounds, a.Ringing())
	}
	a.Stop()
	until(t, "the music to stop", func() bool { _, stops, _, _ := m.state(); return stops == 1 && !a.Ringing() })
	m.running.Wait()
	if _, _, _, levels := m.state(); levels[len(levels)-1] != 0 {
		t.Errorf("the music was left at %d dB", levels[len(levels)-1])
	}
	if held, _, _, _ := m.volume(); held != -1 {
		t.Errorf("the stop left the music held at %d", held)
	}
	e := waitFor(t, got, 2)
	if e[0]["event"] != "ringing" || e[0]["wake_with"] != "radio: WXYZ" || e[1]["event"] != "stopped" || e[1]["wake_with"] != "radio: WXYZ" {
		t.Errorf("events %v", e)
	}
}

// Music not heard within wakeWithin is stopped and the alarm sound rings instead, so nobody oversleeps.
func TestMusicNotHeardInTimeRingsTheAlarmSound(t *testing.T) {
	m := fakeMusic(t)
	m.plays = false
	a, src := wakeAlarm(t, radio)
	at := time.Now()
	a.fire(src, at)
	until(t, "the alarm sound", func() bool { _, _, sounds, _ := m.state(); return sounds == 1 })
	if d := time.Since(at); d < wakeWithin {
		t.Errorf("the alarm sound came after %v, before the music had its %v", d, wakeWithin)
	}
	if _, stops, _, _ := m.state(); stops != 1 || !a.Ringing() {
		t.Errorf("%d stops; ringing %v", stops, a.Ringing())
	}
	if held, _, _, _ := m.volume(); held != -1 {
		t.Errorf("the alarm sound left the music held at %d", held)
	}
}

// Music that stops while the alarm rings, and does not come back within wakeWithin, gives way to the
// alarm sound as well.
func TestMusicThatStopsRingsTheAlarmSound(t *testing.T) {
	m := fakeMusic(t)
	a, src := wakeAlarm(t, radio)
	a.fire(src, time.Now())
	until(t, "the music", func() bool { return m.heard.Load() })
	time.Sleep(wakeWithin / 2)
	m.heard.Store(false) // the station dropped
	at := time.Now()
	until(t, "the alarm sound", func() bool { _, _, sounds, _ := m.state(); return sounds == 1 })
	// Counted from when it was last heard, which is up to a look before it stopped.
	if d := time.Since(at); d < wakeWithin-2*wakeEvery {
		t.Errorf("the alarm sound came %v after the music stopped, before it had %v to come back", d, wakeWithin)
	}
	if !a.Ringing() {
		t.Error("the alarm stopped ringing")
	}
}

// Music that cannot be played, or that the player refuses, rings the alarm sound at once.
func TestMusicThatCannotPlayRingsTheAlarmSoundAtOnce(t *testing.T) {
	for _, c := range []struct {
		name          string
		ready, refuse error
		starts        int
	}{
		{"unavailable", errors.New("no music library is set up"), nil, 0},
		{"refused", nil, errors.New("the favorite is gone"), 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := fakeMusic(t)
			m.ready, m.refuse = c.ready, c.refuse
			a, src := wakeAlarm(t, config.WakeWith{Kind: config.WakeMusic, Name: "Morning", URI: "library://playlist/1"})
			at := time.Now()
			a.fire(src, at)
			until(t, "the alarm sound", func() bool { _, _, sounds, _ := m.state(); return sounds == 1 })
			if d := time.Since(at); d >= wakeWithin {
				t.Errorf("the alarm sound waited %v", d)
			}
			if started, _, _, _ := m.state(); len(started) != c.starts {
				t.Errorf("%d starts, want %d", len(started), c.starts)
			}
		})
	}
}

// Snooze stops a radio station, which is live, and the snooze ringing starts it again; the snooze is as
// long as ever, and the volume is let go of meanwhile.
func TestSnoozingMusicStopsItAndTheSnoozeBringsItBack(t *testing.T) {
	m := fakeMusic(t)
	a, src := wakeAlarm(t, radio)
	a.fire(src, time.Now())
	until(t, "the music", func() bool { return m.heard.Load() })
	before := time.Now()
	if !a.Snooze() {
		t.Fatal("not snoozed")
	}
	until(t, "the music to stop", func() bool { _, stops, _, _ := m.state(); return stops == 1 && !a.Ringing() })
	m.running.Wait()
	if held, _, pauses, _ := m.volume(); held != -1 || pauses != 0 {
		t.Errorf("snoozed: held at %d, %d pauses", held, pauses)
	}
	a.mu.Lock()
	snoozed := append([]source(nil), a.snoozed...)
	a.mu.Unlock()
	want := before.Add(time.Duration(config.Get().Alarms.Snooze()) * time.Minute)
	if len(snoozed) != 1 || snoozed[0].once.Before(want.Add(-time.Second)) || snoozed[0].once.After(want.Add(time.Second)) {
		t.Fatalf("snoozes %+v, want one at %v", snoozed, want)
	}
	m.running.Wait()
	a.fire(snoozed[0], time.Now())
	until(t, "the music again", func() bool { s, _, _, _ := m.state(); return len(s) == 2 })
	if started, _, sounds, _ := m.state(); started[1] != radio || sounds != 0 {
		t.Errorf("the snooze started %+v, %d alarm sounds", started[1], sounds)
	}
}

// Gets louder brings the music up from its floor as it brings up the alarm sound, and a hush for a
// word (or a press that silences the ring) takes it right down.
func TestMusicGetsLouder(t *testing.T) {
	m := fakeMusic(t)
	a, src := wakeAlarm(t, radio)
	if err := config.Set().Alarms().Rise(config.Rise30s); err != nil {
		t.Fatal(err)
	}
	a.fire(src, time.Now())
	until(t, "a few levels", func() bool { _, _, _, l := m.state(); return len(l) > 5 })
	_, _, _, levels := m.state()
	if levels[0] != -30 {
		t.Errorf("the music started at %d dB, want -30", levels[0])
	}
	for _, db := range levels {
		if db < -30 || db > -29 {
			t.Errorf("a moment into a 30 second rise the music is at %d dB", db)
		}
	}
	ring.Hush()
	until(t, "the hush", func() bool { _, _, _, l := m.state(); return l[len(l)-1] == wakeHushed })

	// Once up, it is at full.
	if got := ring.RiseDB(30*time.Second, config.Rise30s.Duration()); got != 0 {
		t.Errorf("after the rise the music is %v dB under full", got)
	}
}

// A helper followed from Home Assistant, a snooze of an alarm gone, and an alarm that does not ring
// wake with the alarm sound.
func TestOnlyARingingAlarmWakesWithMusic(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	a := build()
	for _, al := range []config.Alarm{{ID: "lit", LightOnly: true, Wake: radio}, {ID: "up", Wake: radio}} {
		if err := a.Put(al); err != nil {
			t.Fatal(err)
		}
	}
	if wakeOf("input_datetime.wake").Music() || wakeOf("snooze:gone").Music() || wakeOf("lit").Music() {
		t.Error("something other than a ringing alarm wakes with music")
	}
	if wakeOf("snooze:up") != radio {
		t.Error("a snooze does not wake with its alarm's music")
	}
	if err := a.SetWake("lit", radio); err == nil {
		t.Error("a light-only alarm was given music")
	}
}

// alarm_wake_with reads what Home Assistant writes, by the names the editors list, and sets it.
func TestWakeWithFromHomeAssistant(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	a := build()
	wasStations, wasFetch := wakeStations, fetchFavorites
	t.Cleanup(func() { wakeStations, fetchFavorites = wasStations, wasFetch })
	morning := config.WakeWith{Kind: config.WakeMusic, Name: "Morning", URI: "library://playlist/1"}
	wakeStations = func() []config.WakeWith { return []config.WakeWith{radio} }
	fetchFavorites = func(context.Context) ([]config.WakeWith, error) { return []config.WakeWith{morning}, nil }
	wakeFavorites.Lock()
	wakeFavorites.list, wakeFavorites.at = nil, time.Time{}
	wakeFavorites.Unlock()

	for _, c := range []struct {
		in   string
		want config.WakeWith
	}{
		{"alarm_sound", config.WakeWith{}},
		{"", config.WakeWith{}},
		{"radio: wxyz", radio},
		{"music_assistant: Morning", morning},
		{"music_assistant: library://album/7", config.WakeWith{Kind: config.WakeMusic, Name: "library://album/7", URI: "library://album/7"}},
	} {
		got, err := a.ParseWake(c.in)
		if err != nil || got != c.want {
			t.Errorf("%q: %+v %v, want %+v", c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{"radio: Nowhere FM", "spotify: something", "radio:"} {
		if _, err := a.ParseWake(bad); err == nil {
			t.Errorf("%q was taken", bad)
		}
	}

	if err := a.Put(config.Alarm{ID: "wake", Hour: 6, Minute: 30, On: true}); err != nil {
		t.Fatal(err)
	}
	if err := a.SetWake("wake", morning); err != nil {
		t.Fatal(err)
	}
	if got := config.Get().Alarms.List[0].Wake; got != morning {
		t.Errorf("saved %+v", got)
	}
	if err := a.SetWake("nope", morning); err == nil {
		t.Error("an alarm that is not there was set")
	}
}

// A Music Assistant favorite is paused for a snooze and resumed where it was when the snooze rings; a
// resume that fails, or is not heard, starts the favorite again, and failing that the alarm sound rings.
func TestSnoozingAFavoriteResumesIt(t *testing.T) {
	morning := config.WakeWith{Kind: config.WakeMusic, Name: "Morning", URI: "library://playlist/1"}
	for _, c := range []struct {
		name               string
		resumeErr          error
		resumePlays, plays bool
		starts, sounds     int
	}{
		{"resumed", nil, true, true, 1, 0},
		{"the queue is gone", errors.New("no queue"), false, true, 2, 0},
		{"the resume is not heard", nil, false, true, 2, 0},
		{"nothing plays", errors.New("no queue"), false, false, 2, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := fakeMusic(t)
			a, src := wakeAlarm(t, morning)
			a.fire(src, time.Now())
			until(t, "the ring to hear the music", func() bool {
				a.mu.Lock()
				defer a.mu.Unlock()
				return a.ringing != nil && a.ringing.playing
			})
			if !a.Snooze() {
				t.Fatal("not snoozed")
			}
			// The snooze fires minutes later on a device; here it fires at once, so the first ring has to
			// have ended first, or the snooze is folded into it as a second reason to ring.
			until(t, "the first ring to end", func() bool { return !a.Ringing() })
			m.running.Wait()
			_, stops, _, _ := m.state()
			if held, _, pauses, _ := m.volume(); pauses != 1 || stops != 0 || held != -1 {
				t.Fatalf("snoozed: %d pauses, %d stops, held at %d", pauses, stops, held)
			}
			a.mu.Lock()
			snoozed := a.snoozed[0]
			a.mu.Unlock()
			m.mu.Lock()
			m.resumeErr, m.resumePlays, m.plays = c.resumeErr, c.resumePlays, c.plays
			m.mu.Unlock()
			a.fire(snoozed, time.Now())
			if c.sounds > 0 {
				until(t, "the alarm sound", func() bool { _, _, sounds, _ := m.state(); return sounds == c.sounds })
			} else {
				until(t, "the music again", func() bool { return m.heard.Load() })
				time.Sleep(2 * wakeWithin)
			}
			started, _, sounds, _ := m.state()
			if _, _, _, resumes := m.volume(); resumes != 1 || len(started) != c.starts || sounds != c.sounds {
				t.Errorf("%d resumes, %d starts, %d alarm sounds; want 1, %d, %d", resumes, len(started), sounds, c.starts, c.sounds)
			}
		})
	}
}

// alarm_wake_with runs on the Home Assistant connection's read loop: a name among the favorites kept
// is taken at once, without waiting on Music Assistant for fresh ones.
func TestAKnownFavoriteDoesNotWaitForMusicAssistant(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	a := build()
	wasStations, wasFetch := wakeStations, fetchFavorites
	t.Cleanup(func() { wakeStations, fetchFavorites = wasStations, wasFetch })
	morning := config.WakeWith{Kind: config.WakeMusic, Name: "Morning", URI: "library://playlist/1"}
	wakeStations = func() []config.WakeWith { return nil }
	slow := make(chan struct{})
	t.Cleanup(func() {
		// The fetch left under way ends before the next test, which would otherwise find its answer.
		close(slow)
		wakeFavorites.Lock()
		f := wakeFavorites.fetching
		wakeFavorites.Unlock()
		if f != nil {
			<-f
		}
	})
	fetchFavorites = func(context.Context) ([]config.WakeWith, error) { <-slow; return nil, nil }
	wakeFavorites.Lock()
	wakeFavorites.list, wakeFavorites.at = []config.WakeWith{morning}, time.Now().Add(-time.Hour)
	wakeFavorites.Unlock()

	start := time.Now()
	got, err := a.ParseWake("music_assistant: Morning")
	if err != nil || got != morning {
		t.Fatalf("%+v %v, want %+v", got, err, morning)
	}
	if took := time.Since(start); took > time.Second {
		t.Errorf("took %v with the favorite already kept", took)
	}
}

// With no Music Assistant set up there is nothing to keep: once one is, it is asked at once, not ten
// minutes later.
func TestNoLibraryIsNotKeptAsAnAnswer(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	a := build()
	wasStations, wasFetch := wakeStations, fetchFavorites
	t.Cleanup(func() { wakeStations, fetchFavorites = wasStations, wasFetch })
	wakeStations = func() []config.WakeWith { return nil }
	fetchFavorites = maFavorites
	wakeFavorites.Lock()
	wakeFavorites.list, wakeFavorites.at = nil, time.Time{}
	wakeFavorites.Unlock()

	if got := a.WakeChoices(time.Second); len(got) != 0 {
		t.Errorf("offered %+v with no library", got)
	}
	wakeFavorites.Lock()
	at, fetching := wakeFavorites.at, wakeFavorites.fetching
	wakeFavorites.Unlock()
	if again := time.Until(at.Add(wakeFavoritesFor)); again > wakeNoLibraryAgain || fetching != nil {
		t.Errorf("no library was kept as an answer for %v, fetching %v", again, fetching != nil)
	}

	// A few seconds on, a library set up meanwhile is asked.
	wakeFavorites.Lock()
	wakeFavorites.at = time.Now().Add(-wakeFavoritesFor - time.Second)
	wakeFavorites.Unlock()
	morning := config.WakeWith{Kind: config.WakeMusic, Name: "Morning", URI: "library://playlist/1"}
	fetchFavorites = func(context.Context) ([]config.WakeWith, error) { return []config.WakeWith{morning}, nil }
	if got := a.WakeChoices(time.Second); len(got) != 1 || got[0] != morning {
		t.Errorf("a library set up after is not asked: %+v", got)
	}
}
