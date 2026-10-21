package detect

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/detect/assets"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wake"
)

// commandRig is a volume down word with the world stood in for: the music, the speaker's mute, a turn,
// a ring, the clock, the timer, the level and the bus.
type commandRig struct {
	c         *command
	e         *Engine
	playing   bool
	muted     bool
	busy      bool
	ringing   bool
	clock     time.Time
	timers    []func()
	waits     []time.Duration
	level     int
	adjusted  []int
	fired     []map[string]string
	wakeWords []string
	models    []wake.Model
}

func newCommandRig(t *testing.T) *commandRig {
	t.Helper()
	fakes(t)
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	r := &commandRig{
		e:      New(CommandSlot+1, nil),
		clock:  time.Unix(1_000_000, 0),
		level:  30,
		models: []wake.Model{model("turn_it_down", wake.KindMicroWakeWord), model("okay_nabu", wake.KindMicroWakeWord)},
	}
	word := &esphome.Select{}
	r.c = &command{
		d:         &Detect{engine: r.e},
		word:      word,
		opts:      component.NewOptions(word),
		models:    func() []wake.Model { return r.models },
		wakeWords: func() []string { return r.wakeWords },
		playing:   func() bool { return r.playing },
		muted:     func() bool { return r.muted },
		busy:      func() bool { return r.busy },
		ringing:   func() bool { return r.ringing },
		score:     func() float64 { return 0.934 },
		adjust: func(delta int) int {
			r.adjusted = append(r.adjusted, delta)
			r.level = max(0, r.level+delta)
			return r.level
		},
		fire:  func(data map[string]string) { r.fired = append(r.fired, data) },
		now:   func() time.Time { return r.clock },
		after: func(d time.Duration, f func()) { r.waits = append(r.waits, d); r.timers = append(r.timers, f) },
	}
	return r
}

func (r *commandRig) loaded() string {
	m, ok := r.e.Loaded(CommandSlot)
	if !ok {
		return ""
	}
	return m.ID
}

func (r *commandRig) choose(t *testing.T, id string) {
	t.Helper()
	if err := config.Set().VolumeDown().ID(id); err != nil {
		t.Fatal(err)
	}
}

// fireTimers runs the timers that were set, as time.AfterFunc would once they are due.
func (r *commandRig) fireTimers() {
	due := r.timers
	r.timers = nil
	for _, f := range due {
		f()
	}
}

// Nothing is listened for until a word is chosen: None is the default, so a device that was never set
// up turns nothing down on its own.
func TestNoVolumeDownWordByDefault(t *testing.T) {
	r := newCommandRig(t)
	r.playing = true
	r.c.follow()
	if got := r.loaded(); got != "" {
		t.Fatalf("by default, loaded %q", got)
	}
	if got := (config.CommandWord{}).ThresholdAt(); got != config.DefaultCommandThreshold {
		t.Errorf("unset threshold %v, want %v", got, config.DefaultCommandThreshold)
	}
}

// The model is in its slot while music plays and out of it while none does, and after the engine starts
// again with every slot empty it is put back.
func TestTheVolumeDownWordFollowsTheMusic(t *testing.T) {
	r := newCommandRig(t)
	r.choose(t, "turn_it_down")

	r.c.follow()
	if got := r.loaded(); got != "" {
		t.Fatalf("nothing playing, loaded %q", got)
	}
	if len(r.timers) != 0 {
		t.Fatalf("nothing playing and nothing loaded, yet a look was set for later")
	}

	r.playing = true
	r.c.follow()
	if got := r.loaded(); got != "turn_it_down" {
		t.Fatalf("playing, loaded %q, want turn_it_down", got)
	}

	r.e.Clear(CommandSlot) // as a restart empties every slot
	r.c.follow()           // a look between the engine's Close and its Start: not a failure
	if got := r.loaded(); got != "turn_it_down" || r.c.failed != "" {
		t.Fatalf("a look in a restart: loaded %q, failed %q; want it back and nothing failed", got, r.c.failed)
	}
	r.e.Clear(CommandSlot)
	r.c.restarted()
	if got := r.loaded(); got != "turn_it_down" {
		t.Fatalf("after a restart, loaded %q, want turn_it_down", got)
	}
}

// A model that failed before the engine started again is tried again after it, as the wake words are.
func TestARestartTriesAFailedModelAgain(t *testing.T) {
	r := newCommandRig(t)
	r.choose(t, "turn_it_down")
	r.playing = true
	r.c.follow()
	r.e.Clear(CommandSlot) // as Engine.dropFailed does
	r.c.dropped("turn_it_down")
	r.c.follow()
	if got := r.loaded(); got != "" {
		t.Fatalf("a failed model was loaded again before the restart: %q", got)
	}
	r.c.restarted()
	if got := r.loaded(); got != "turn_it_down" {
		t.Fatalf("after a restart, loaded %q, want the model tried again", got)
	}
}

// The gap between two tracks does not unload it: it stays in for commandHold after the music stops,
// with one look set for the end of the hold however often the player says something changed, and goes
// once the hold is over with nothing playing.
func TestTheVolumeDownWordStaysThroughTheGapBetweenTracks(t *testing.T) {
	r := newCommandRig(t)
	r.choose(t, "turn_it_down")
	r.playing = true
	r.c.follow()

	r.playing = false
	r.c.follow()
	if got := r.loaded(); got != "turn_it_down" {
		t.Fatalf("just after the music stopped, loaded %q, want it kept", got)
	}
	if len(r.waits) != 1 || r.waits[0] != commandHold {
		t.Fatalf("looks set for %v, want one at %v", r.waits, commandHold)
	}
	r.clock = r.clock.Add(4 * time.Second)
	r.c.follow()
	if len(r.waits) != 1 {
		t.Fatalf("a second look was set while one was due: %v", r.waits)
	}
	if got := r.loaded(); got != "turn_it_down" {
		t.Fatalf("inside the hold, loaded %q, want it kept", got)
	}

	// The next track starts inside the hold: nothing changes.
	r.playing = true
	r.c.follow()
	r.clock = r.clock.Add(6 * time.Second)
	r.fireTimers()
	if got := r.loaded(); got != "turn_it_down" {
		t.Fatalf("playing again when the hold ended, loaded %q, want it kept", got)
	}

	// The music stops for good.
	r.playing = false
	r.c.follow()
	r.clock = r.clock.Add(commandHold)
	r.fireTimers()
	if got := r.loaded(); got != "" {
		t.Fatalf("the hold over and nothing playing, still loaded %q", got)
	}
	if len(r.timers) != 0 {
		t.Fatalf("a look was set again after the word went")
	}
}

// A look at the end of a hold that the music interrupted, then stopped again, waits out the new hold
// rather than unloading early.
func TestASecondStopGetsAHoldOfItsOwn(t *testing.T) {
	r := newCommandRig(t)
	r.choose(t, "turn_it_down")
	r.playing = true
	r.c.follow()

	r.playing = false
	r.c.follow() // hold one starts
	r.clock = r.clock.Add(3 * time.Second)
	r.playing = true
	r.c.follow()
	r.clock = r.clock.Add(time.Second)
	r.playing = false
	r.c.follow() // hold two starts, 4 s after hold one

	r.clock = r.clock.Add(6 * time.Second) // hold one's look comes due
	r.fireTimers()
	if got := r.loaded(); got != "turn_it_down" {
		t.Fatalf("6 s into the second hold, loaded %q, want it kept", got)
	}
	if len(r.waits) != 2 || r.waits[1] != 4*time.Second {
		t.Fatalf("looks set for %v, want a second one for the 4 s left", r.waits)
	}
	r.clock = r.clock.Add(4 * time.Second)
	r.fireTimers()
	if got := r.loaded(); got != "" {
		t.Fatalf("the second hold over, still loaded %q", got)
	}
}

// Off however it is turned off, and taken out at once, music or not. A word that is a wake word too, by
// id or by phrase, keeps its choice and listens again once it is not; one the device no longer has goes
// back to None, and the select says so.
func TestTheVolumeDownWordTurnsOff(t *testing.T) {
	for _, c := range []struct {
		name   string
		off    func(*testing.T, *commandRig)
		toNone bool
	}{
		{"threshold at the top", func(t *testing.T, r *commandRig) {
			if err := config.Set().VolumeDown().Threshold(config.StopOff); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"chosen as a wake word", func(_ *testing.T, r *commandRig) { r.wakeWords = []string{"turn_it_down"} }, false},
		{"its phrase chosen as a wake word", func(_ *testing.T, r *commandRig) {
			r.models = append(r.models, wake.Model{ID: "turn_it_down_2", Phrase: "turn_it_down", Kind: wake.KindMicroWakeWord})
			r.wakeWords = []string{"turn_it_down_2"}
		}, false},
		{"None", func(t *testing.T, r *commandRig) { r.choose(t, "") }, true},
		{"the model gone", func(_ *testing.T, r *commandRig) { r.models = r.models[1:] }, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newCommandRig(t)
			r.choose(t, "turn_it_down")
			r.playing = true
			r.c.follow()
			if got := r.loaded(); got != "turn_it_down" {
				t.Fatalf("playing, loaded %q", got)
			}
			if got := r.c.word.Get(); got != "turn_it_down" {
				t.Fatalf("the select shows %q", got)
			}
			c.off(t, r)
			r.c.follow()
			if got := r.loaded(); got != "" {
				t.Fatalf("still loaded %q", got)
			}
			saved := config.Get().Wake.VolumeDown.ID
			if c.toNone && saved != "" {
				t.Errorf("still saved as %q, want None", saved)
			}
			if !c.toNone && saved != "turn_it_down" {
				t.Errorf("saved as %q, want the choice kept", saved)
			}
			if want := map[bool]string{true: commandNone, false: "turn_it_down"}[c.toNone]; r.c.word.Get() != want {
				t.Errorf("the select shows %q, want %q", r.c.word.Get(), want)
			}
		})
	}
}

// Choosing another word while the music plays swaps the model in the slot.
func TestChoosingAnotherVolumeDownWordSwapsIt(t *testing.T) {
	r := newCommandRig(t)
	r.choose(t, "turn_it_down")
	r.playing = true
	r.c.follow()
	r.choose(t, "okay_nabu")
	r.c.follow()
	if got := r.loaded(); got != "okay_nabu" {
		t.Fatalf("loaded %q, want okay_nabu", got)
	}
}

// A model the engine took out because it failed on the audio is not loaded again on the next look, which
// for the music player is every few seconds: it waits until it is chosen again or the library changes.
func TestAModelTheEngineDroppedIsLeftOut(t *testing.T) {
	r := newCommandRig(t)
	r.choose(t, "turn_it_down")
	r.playing = true
	r.c.follow()

	r.c.dropped("okay_nabu") // not the one it holds: nothing to do with it
	r.c.follow()
	if got := r.loaded(); got != "turn_it_down" {
		t.Fatalf("another model's failure took it out: loaded %q", got)
	}

	r.e.Clear(CommandSlot) // as Engine.dropFailed does, before it calls OnDropped
	r.c.dropped("turn_it_down")
	r.c.follow()
	r.c.follow()
	if got := r.loaded(); got != "" {
		t.Fatalf("a model that failed was loaded again: %q", got)
	}

	r.c.retry.Store(true) // chosen again, or the library changed
	r.c.follow()
	if got := r.loaded(); got != "turn_it_down" {
		t.Fatalf("after it was chosen again, loaded %q, want it tried again", got)
	}
}

// A model that will not load is tried once, not on every look.
func TestAModelThatWillNotLoadIsTriedOnce(t *testing.T) {
	r := newCommandRig(t)
	builds := 0
	was := build
	build = func(wake.Kind) (backend, error) { builds++; return nil, errors.New("no engine") }
	t.Cleanup(func() { build = was })

	r.choose(t, "turn_it_down")
	r.playing = true
	r.c.follow()
	r.c.follow()
	r.c.follow()
	if builds != 1 {
		t.Fatalf("tried %d times, want once", builds)
	}
	if got := r.loaded(); got != "" {
		t.Fatalf("loaded %q", got)
	}
}

// Heard, it turns the music down by volumeDownSteps and tells the bus where the level went. During a
// turn, over a ring, with the speaker muted (Adjust would unmute it) or with no music, it does nothing.
func TestTheVolumeDownWordTurnsItDown(t *testing.T) {
	r := newCommandRig(t)
	r.choose(t, "turn_it_down")
	r.playing = true
	r.c.heard()
	if !slices.Equal(r.adjusted, []int{-volumeDownSteps}) {
		t.Fatalf("adjusted %v, want one step of %d", r.adjusted, -volumeDownSteps)
	}
	want := map[string]string{"command": "volume_down", "word": "turn_it_down", "score": "0.93", "volume": "25"}
	if len(r.fired) != 1 {
		t.Fatalf("fired %v, want one event", r.fired)
	}
	for k, v := range want {
		if r.fired[0][k] != v {
			t.Errorf("event %s = %q, want %q (%v)", k, r.fired[0][k], v, r.fired[0])
		}
	}

	for _, c := range []struct {
		name string
		set  func(*commandRig)
	}{
		{"a turn", func(r *commandRig) { r.busy = true }},
		{"a ring", func(r *commandRig) { r.ringing = true }},
		{"muted", func(r *commandRig) { r.muted = true }},
		{"no music", func(r *commandRig) { r.playing = false }},
	} {
		r := newCommandRig(t)
		r.playing = true
		c.set(r)
		r.c.heard()
		if len(r.adjusted) != 0 || len(r.fired) != 0 {
			t.Errorf("%s: adjusted %v, fired %v", c.name, r.adjusted, r.fired)
		}
	}
}

// The select offers None, then the device's models by phrase, each once, but for one of the user's called
// "Stop". The wake words stay in it, so it changes only with the library. None is saved as empty, the
// default.
func TestVolumeDownOptions(t *testing.T) {
	r := newCommandRig(t)
	r.models = append(r.models,
		wake.Model{ID: "my_stop", Phrase: assets.StopPhrase, Kind: wake.KindMicroWakeWord},
		wake.Model{ID: "turn_it_down_2", Phrase: "turn_it_down", Kind: wake.KindMicroWakeWord},
		wake.Model{ID: "okay_nabu_v2", Phrase: "okay_nabu", Kind: wake.KindMicroWakeWord})
	r.wakeWords = []string{"okay_nabu"}

	want := []string{commandNone, "turn_it_down", "okay_nabu"}
	if got := r.c.options(); !slices.Equal(got, want) {
		t.Errorf("options %v, want %v", got, want)
	}
	for _, c := range []struct{ phrase, id string }{{commandNone, ""}, {"turn_it_down", "turn_it_down"}, {"okay_nabu", "okay_nabu"}} {
		if id, ok := r.c.idFor(c.phrase); !ok || id != c.id {
			t.Errorf("idFor(%q) = %q, %v; want %q", c.phrase, id, ok, c.id)
		}
		if got := r.c.phraseFor(c.id); got != c.phrase {
			t.Errorf("phraseFor(%q) = %q, want %q", c.id, got, c.phrase)
		}
	}
	if _, ok := r.c.idFor("nothing like it"); ok {
		t.Error("an unknown option was taken")
	}
	if got := r.c.phraseFor("gone"); got != commandNone {
		t.Errorf("a model the device no longer has shows as %q", got)
	}

	// The select is kept up to date as the device's models change.
	r.models = append(r.models, model("louder", wake.KindMicroWakeWord))
	r.c.follow()
	if got := r.c.opts.Get(); !slices.Contains(got, "louder") {
		t.Errorf("a model the device has now is not offered: %v", got)
	}
}

// The wake words left out are the chosen ones and the ones running: a selection is loaded before it is
// saved.
func TestWakeWordIDsIncludeWhatRuns(t *testing.T) {
	fakes(t)
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	e := New(CommandSlot+1, nil)
	if err := e.Use(0, model("okay_nabu", wake.KindMicroWakeWord)); err != nil {
		t.Fatal(err)
	}
	d := &Detect{engine: e}
	got := d.wakeWordIDs()
	if !slices.Contains(got, "okay_nabu") {
		t.Errorf("wake words %v, want the running okay_nabu among them", got)
	}
	for _, id := range chosenWakeWords() {
		if !slices.Contains(got, id) {
			t.Errorf("wake words %v leave out the chosen %q", got, id)
		}
	}
}

// Its threshold is DefaultCommandThreshold unless one is set, and at the top of the range it is off.
func TestVolumeDownThreshold(t *testing.T) {
	if got := (config.CommandWord{ID: "x", Threshold: 0.93}).ThresholdAt(); got != 0.93 {
		t.Errorf("set: %v", got)
	}
	if (config.CommandWord{ID: "x", Threshold: config.StopOff}).On() {
		t.Error("on at the top of the range")
	}
	if (config.CommandWord{}).On() {
		t.Error("on with none chosen")
	}
	if !(config.CommandWord{ID: "x"}).On() {
		t.Error("off with a word chosen and the default threshold")
	}
}

// dropper is a fake engine whose models can be made to fail on the audio, as a broken upload does.
type dropper struct {
	fake
	fail []string
}

func (d *dropper) failed() []string {
	f := d.fail
	d.fail = nil
	return f
}

// The engine says which slot's model it took out for failing (OnDropped), which is how the volume down
// word tells a failure from a restart's empty slots.
func TestTheEngineReportsAModelItDropped(t *testing.T) {
	d := &dropper{fake: fake{loaded: map[string]float64{}}}
	was := build
	build = func(wake.Kind) (backend, error) { return d, nil }
	t.Cleanup(func() { build = was })

	e := New(CommandSlot+1, nil)
	type drop struct {
		slot int
		id   string
	}
	got := make(chan drop, 1)
	e.OnDropped = func(slot int, id string) { got <- drop{slot, id} }
	if err := e.Use(CommandSlot, model("turn_it_down", wake.KindMicroWakeWord)); err != nil {
		t.Fatal(err)
	}
	d.fail = []string{"turn_it_down"}
	e.mu.Lock()
	e.dropFailed()
	e.mu.Unlock()

	select {
	case g := <-got:
		if g.slot != CommandSlot || g.id != "turn_it_down" {
			t.Errorf("reported %+v", g)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the drop was not reported")
	}
	if _, ok := e.Loaded(CommandSlot); ok {
		t.Error("the failed model is still in its slot")
	}
}

// The whole way through a selection: choosing the volume down word's model as a wake word takes it out of
// its slot without losing the choice (the load looks again), and once the selection moves off it, the
// look after the save (selected) puts it back.
func TestAWakeWordSelectionMovesTheVolumeDownWord(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"turn_it_down.tflite": "model",
		"turn_it_down.json":   `{"type":"micro","wake_word":"Turn it down","model":"turn_it_down.tflite"}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r := newCommandRig(t)
	busy, _ := fixture(t, warm{0: true})
	d := &Detect{engine: r.e, busy: busy, lib: wake.NewLibrary(dir), command: r.c}
	r.c.d = d
	r.c.models = func() []wake.Model { return d.library().Ours() }
	r.c.wakeWords = d.wakeWordIDs

	r.choose(t, "turn_it_down")
	r.playing = true
	r.c.follow()
	if got := r.loaded(); got != "turn_it_down" {
		t.Fatalf("playing, loaded %q", got)
	}

	if got := d.load([]string{"turn_it_down"}); len(got) != 1 {
		t.Fatalf("the wake word did not load: %v", got)
	}
	deadline := time.Now().Add(3 * time.Second)
	for r.loaded() != "" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := r.loaded(); got != "" {
		t.Fatalf("a wake word now, and still loaded %q as the volume down word", got)
	}
	if got := config.Get().Wake.VolumeDown.ID; got != "turn_it_down" {
		t.Fatalf("the choice was lost: saved %q", got)
	}

	// The selection moves to no wake word: voice clears the slot and saves it, then calls selected.
	r.e.Clear(0)
	if err := config.Set().Wake(0).ID(""); err != nil {
		t.Fatal(err)
	}
	if err := config.Set().NoneChosen(true); err != nil {
		t.Fatal(err)
	}
	d.selected()
	if got := r.loaded(); got != "turn_it_down" {
		t.Fatalf("no longer a wake word, loaded %q, want it back", got)
	}
}

// Crossing is the score the latest detection fired at, the one a command word's event carries.
func TestTheEngineKeepsTheCrossingScore(t *testing.T) {
	fakes(t)
	e := New(CommandSlot+1, nil)
	if got := e.Crossing(CommandSlot); got != 0 {
		t.Fatalf("before any detection: %v", got)
	}
	e.mu.Lock()
	e.slots[CommandSlot].crossing = 0.91
	e.mu.Unlock()
	if got := e.Crossing(CommandSlot); got != 0.91 {
		t.Errorf("crossing %v, want 0.91", got)
	}
	if got := e.Crossing(len(e.slots)); got != 0 {
		t.Errorf("a slot out of range: %v", got)
	}
}
