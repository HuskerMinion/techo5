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
}

func fakeMusic(t *testing.T) *music {
	t.Helper()
	m := &music{plays: true}
	was := []any{wakeWithin, wakeEvery, startQuiet, goWake, wakeReady, wakeStart, wakeHeard, wakeStop, wakeLower}
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
	t.Cleanup(func() {
		m.running.Wait()
		wakeWithin, wakeEvery = was[0].(time.Duration), was[1].(time.Duration)
		startQuiet = was[2].(func(string, string, func()) (func(), func([]speaker.Note, time.Duration)))
		goWake = was[3].(func(func()))
		wakeReady = was[4].(func(config.WakeWith) error)
		wakeStart = was[5].(func(context.Context, config.WakeWith, func() bool) error)
		wakeHeard, wakeStop, wakeLower = was[6].(func() bool), was[7].(func()), was[8].(func(int))
	})
	return m
}

func (m *music) state() (started []config.WakeWith, stops, sounds int, levels []int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]config.WakeWith(nil), m.started...), m.stops, m.sounds, append([]int(nil), m.levels...)
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
	a.fire(src, time.Now())
	until(t, "the music", func() bool { s, _, _, _ := m.state(); return len(s) == 1 })
	time.Sleep(2 * wakeWithin)
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

// Snooze stops the music, and the snooze ringing starts it again; the snooze is as long as ever.
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
