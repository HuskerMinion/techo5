package detect

import (
	"bytes"
	"errors"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/detect/assets"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wake"
)

// commandRig is a command word, the volume down word unless a test says otherwise, with the world stood
// in for: the music, the speaker's mute, a turn, a ring, the clock, the timer, the level, the next song
// and the bus.
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
	reaches   bool
	skipped   int
	fired     []map[string]string
	wakeWords []string
	models    []wake.Model
}

func newCommandRig(t *testing.T) *commandRig {
	t.Helper()
	fakes(t)
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	r := &commandRig{
		e:      New(lastCommandSlot+1, nil),
		clock:  time.Unix(1_000_000, 0),
		level:  30,
		models: []wake.Model{model("turn_it_down", wake.KindMicroWakeWord), model("okay_nabu", wake.KindMicroWakeWord)},
	}
	r.c = r.newWord(volumeDown)
	return r
}

// newWord is a command word of kind k on the rig: the same engine, music, clock and bus as the rest.
func (r *commandRig) newWord(k *commandKind) *command {
	word := &esphome.Select{}
	return &command{
		kind:      k,
		d:         &Detect{engine: r.e},
		word:      word,
		opts:      component.NewOptions(word),
		threshold: &esphome.Number{},
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
		reaches: func() bool { return r.reaches },
		next:    func() { r.skipped++ },
		fire:    func(data map[string]string) { r.fired = append(r.fired, data) },
		now:     func() time.Time { return r.clock },
		after:   func(d time.Duration, f func()) { r.waits = append(r.waits, d); r.timers = append(r.timers, f) },
	}
}

func (r *commandRig) loaded() string {
	m, ok := r.e.Loaded(r.c.kind.slot)
	if !ok {
		return ""
	}
	return m.ID
}

func (r *commandRig) choose(t *testing.T, id string) {
	t.Helper()
	if err := r.c.kind.setID(id); err != nil {
		t.Fatal(err)
	}
}

// as makes the rig's word another command word: its slot, its saved choice, what it does.
func (r *commandRig) as(k *commandKind) *commandRig {
	r.c = r.newWord(k)
	return r
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
// id or by phrase, keeps its choice and listens again once it is not. One the device no longer has keeps
// its choice too, as the hang-up word does, with the select showing None.
func TestTheVolumeDownWordTurnsOff(t *testing.T) {
	for _, c := range []struct {
		name    string
		off     func(*testing.T, *commandRig)
		cleared bool   // the saved choice is None
		shows   string // what the select shows
	}{
		{"threshold at the top", func(t *testing.T, r *commandRig) {
			if err := config.Set().VolumeDown().Threshold(config.StopOff); err != nil {
				t.Fatal(err)
			}
		}, false, "turn_it_down"},
		{"chosen as a wake word", func(_ *testing.T, r *commandRig) { r.wakeWords = []string{"turn_it_down"} }, false, "turn_it_down"},
		{"its phrase chosen as a wake word", func(_ *testing.T, r *commandRig) {
			r.models = append(r.models, wake.Model{ID: "turn_it_down_2", Phrase: "turn_it_down", Kind: wake.KindMicroWakeWord})
			r.wakeWords = []string{"turn_it_down_2"}
		}, false, "turn_it_down"},
		{"None", func(t *testing.T, r *commandRig) { r.choose(t, "") }, true, commandNone},
		{"the model gone", func(_ *testing.T, r *commandRig) { r.models = r.models[1:] }, false, commandNone},
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
			if c.cleared && saved != "" {
				t.Errorf("still saved as %q, want None", saved)
			}
			if !c.cleared && saved != "turn_it_down" {
				t.Errorf("saved as %q, want the choice kept", saved)
			}
			if got := r.c.word.Get(); got != c.shows {
				t.Errorf("the select shows %q, want %q", got, c.shows)
			}
		})
	}
}

// A model taken off the device and sent again brings the word back as it was chosen: nothing was
// written over while it was gone.
func TestTheVolumeDownWordComesBackWithItsModel(t *testing.T) {
	r := newCommandRig(t)
	r.choose(t, "turn_it_down")
	r.playing = true
	all := r.models
	r.models = all[1:]
	r.c.follow()
	if got := r.loaded(); got != "" {
		t.Fatalf("its model gone, loaded %q", got)
	}
	r.models = all
	r.c.retry.Store(true) // as the library's Changed does
	r.c.follow()
	if got := r.loaded(); got != "turn_it_down" {
		t.Errorf("its model back, loaded %q", got)
	}
	if got := r.c.word.Get(); got != "turn_it_down" {
		t.Errorf("the select shows %q", got)
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

// Heard, it turns the music down by volumeSteps and tells the bus where the level went. During a
// turn, over a ring, with the speaker muted (Adjust would unmute it) or with no music, it does nothing.
func TestTheVolumeDownWordTurnsItDown(t *testing.T) {
	r := newCommandRig(t)
	r.choose(t, "turn_it_down")
	r.playing = true
	r.c.heard()
	if !slices.Equal(r.adjusted, []int{-volumeSteps}) {
		t.Fatalf("adjusted %v, want one step of %d", r.adjusted, -volumeSteps)
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
	e := New(lastCommandSlot+1, nil)
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

	e := New(lastCommandSlot+1, nil)
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
	d := &Detect{engine: r.e, busy: busy, lib: wake.NewLibrary(dir), commands: []*command{r.c}}
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
	e := New(lastCommandSlot+1, nil)
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

// The volume up word turns the music up by volumeSteps and tells the bus, with the same checks as the
// volume down word: nothing during a turn, over a ring, muted (Adjust would unmute it) or with no music.
func TestTheVolumeUpWordTurnsItUp(t *testing.T) {
	r := newCommandRig(t).as(volumeUp)
	r.level = 20
	r.choose(t, "turn_it_up")
	r.playing = true
	r.c.heard()
	if !slices.Equal(r.adjusted, []int{volumeSteps}) {
		t.Fatalf("adjusted %v, want one step of %d", r.adjusted, volumeSteps)
	}
	want := map[string]string{"command": "volume_up", "word": "turn_it_up", "score": "0.93", "volume": "25"}
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
		r := newCommandRig(t).as(volumeUp)
		r.playing = true
		c.set(r)
		r.c.heard()
		if len(r.adjusted) != 0 || len(r.fired) != 0 {
			t.Errorf("%s: adjusted %v, fired %v", c.name, r.adjusted, r.fired)
		}
	}
}

// The next song word asks for the next song and tells the bus, for a client that skips its own queue;
// it leaves the level alone. Not during a turn, a ring, muted, or with no music.
func TestTheNextSongWordSkips(t *testing.T) {
	r := newCommandRig(t).as(nextSong)
	r.choose(t, "next_song")
	r.playing = true
	r.reaches = true
	r.c.heard()
	if r.skipped != 1 || len(r.adjusted) != 0 {
		t.Fatalf("skipped %d, adjusted %v; want one skip and the level left alone", r.skipped, r.adjusted)
	}
	want := map[string]string{"command": "next", "word": "next_song", "score": "0.93", "asked": "true"}
	if len(r.fired) != 1 {
		t.Fatalf("fired %v, want one event", r.fired)
	}
	for k, v := range want {
		if r.fired[0][k] != v {
			t.Errorf("event %s = %q, want %q (%v)", k, r.fired[0][k], v, r.fired[0])
		}
	}
	if _, ok := r.fired[0]["volume"]; ok {
		t.Errorf("a skip says a volume: %v", r.fired[0])
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
		r := newCommandRig(t).as(nextSong)
		r.playing = true
		r.reaches = true
		c.set(r)
		r.c.heard()
		if r.skipped != 0 || len(r.fired) != 0 {
			t.Errorf("%s: skipped %d, fired %v", c.name, r.skipped, r.fired)
		}
	}
}

// With nobody the device can ask (its own stations, a client sending one track at a time), the next song
// word asks nothing, and the event says so: asked false is the skip, for whoever plays the music.
func TestTheNextSongWordWithNobodyToAsk(t *testing.T) {
	r := newCommandRig(t).as(nextSong)
	r.choose(t, "next_song")
	r.playing = true
	r.reaches = false
	r.c.heard()
	if r.skipped != 0 {
		t.Fatalf("asked for a skip with nobody to ask (%d)", r.skipped)
	}
	if len(r.fired) != 1 || r.fired[0]["command"] != "next" || r.fired[0]["asked"] != "false" {
		t.Fatalf("fired %v, want one next event with asked false", r.fired)
	}
}

// Each command word listens in a slot of its own, while music plays, each its own model.
func TestEachCommandWordHasItsOwnSlot(t *testing.T) {
	r := newCommandRig(t)
	r.models = append(r.models, model("turn_it_up", wake.KindMicroWakeWord), model("next_song", wake.KindMicroWakeWord))
	r.playing = true
	words := map[*commandKind]string{volumeDown: "turn_it_down", volumeUp: "turn_it_up", nextSong: "next_song"}
	var all []*command
	for _, k := range commandKinds {
		all = append(all, r.newWord(k))
	}
	for _, c := range all {
		if err := c.kind.setID(words[c.kind]); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range all {
		c.follow()
	}
	for _, c := range all {
		m, ok := r.e.Loaded(c.kind.slot)
		if !ok || m.ID != words[c.kind] {
			t.Errorf("%s: slot %d holds %q (%v), want %q", c.kind.what, c.kind.slot, m.ID, ok, words[c.kind])
		}
	}
	if !isCommandSlot(CommandSlot) || !isCommandSlot(VolumeUpSlot) || !isCommandSlot(NextSongSlot) ||
		isCommandSlot(HangupSlot) || isCommandSlot(StopSlot) || isCommandSlot(NextSongSlot+1) {
		t.Error("isCommandSlot does not match the command words' slots")
	}
	for _, k := range commandKinds {
		if commandKindAt(k.slot) != k {
			t.Errorf("commandKindAt(%d) is not the %s", k.slot, k.what)
		}
	}
}

// One model chosen as two command words listens only as the first, in slot order: heard as both, it would
// turn the music down and up at once. The second keeps its choice and listens once the first lets go.
func TestAModelChosenTwiceListensOnlyAsTheFirst(t *testing.T) {
	r := newCommandRig(t)
	r.playing = true
	up := r.newWord(volumeUp)
	if err := config.Set().VolumeDown().ID("turn_it_down"); err != nil {
		t.Fatal(err)
	}
	if err := config.Set().VolumeUp().ID("turn_it_down"); err != nil {
		t.Fatal(err)
	}
	r.c.follow()
	up.follow()
	if m, ok := r.e.Loaded(CommandSlot); !ok || m.ID != "turn_it_down" {
		t.Fatalf("the volume down word holds %q (%v)", m.ID, ok)
	}
	if m, ok := r.e.Loaded(VolumeUpSlot); ok {
		t.Fatalf("the same model also listens as the volume up word: %q", m.ID)
	}
	if _, why := up.wanted(); why != "it is the volume down word too" {
		t.Errorf("why %q", why)
	}
	if got := config.Get().Wake.VolumeUp.ID; got != "turn_it_down" {
		t.Fatalf("the volume up word's choice was lost: %q", got)
	}
	if err := config.Set().VolumeDown().ID(""); err != nil {
		t.Fatal(err)
	}
	r.c.follow()
	up.follow()
	if m, ok := r.e.Loaded(VolumeUpSlot); !ok || m.ID != "turn_it_down" {
		t.Errorf("with the volume down word off, the volume up word holds %q (%v)", m.ID, ok)
	}
}

// Their selects and thresholds are entities of their own, named for what they do, on the microphone.
func TestCommandWordEntities(t *testing.T) {
	fakes(t)
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	d := &Detect{engine: New(lastCommandSlot+1, nil)}
	seen := map[string]bool{}
	for _, k := range commandKinds {
		c := newCommand(d, k)
		for _, id := range []string{c.word.ObjectID, c.threshold.ObjectID} {
			if seen[id] {
				t.Errorf("object id %q twice", id)
			}
			seen[id] = true
		}
		if c.word.DeviceID != component.DeviceMicrophone || c.threshold.Name != c.word.Name+" threshold" {
			t.Errorf("%s: %q on %v, threshold %q", k.what, c.word.ObjectID, c.word.DeviceID, c.threshold.Name)
		}
	}
	for _, id := range []string{"volume_down_word", "volume_down_word_threshold", "volume_up_word",
		"volume_up_word_threshold", "next_song_word", "next_song_word_threshold"} {
		if !seen[id] {
			t.Errorf("no entity %q", id)
		}
	}
}

// allRig is the three command words on one Detect, as the device has them, so what one does to another is
// what is tested.
func newAllRig(t *testing.T) (*commandRig, *Detect, map[*commandKind]*command) {
	t.Helper()
	r := newCommandRig(t)
	r.models = append(r.models, model("turn_it_up", wake.KindMicroWakeWord), model("next_song", wake.KindMicroWakeWord),
		wake.Model{ID: "turn_it_down_2", Phrase: "turn_it_down", Kind: wake.KindMicroWakeWord})
	d := &Detect{engine: r.e}
	words := map[*commandKind]*command{}
	for _, k := range commandKinds {
		c := r.newWord(k)
		c.d = d
		d.commands = append(d.commands, c)
		words[k] = c
	}
	r.c = words[volumeDown]
	return r, d, words
}

func (r *commandRig) holds(slot int) string {
	m, ok := r.e.Loaded(slot)
	if !ok {
		return ""
	}
	return m.ID
}

// Each slot reaches its own word: heard, dropped. Not a command word's slot reaches none.
func TestCommandWordsAreReachedByTheirSlot(t *testing.T) {
	r, d, words := newAllRig(t)
	for _, k := range commandKinds {
		if d.commandAt(k.slot) != words[k] {
			t.Errorf("commandAt(%d) is not the %s", k.slot, k.what)
		}
	}
	if d.commandAt(StopSlot) != nil || d.commandAt(NextSongSlot+1) != nil || d.commandAt(0) != nil {
		t.Error("a slot that isn't a command word's has one")
	}
	if err := config.Set().VolumeUp().ID("turn_it_up"); err != nil {
		t.Fatal(err)
	}
	r.playing = true
	r.level = 10
	d.commandHeard(VolumeUpSlot)
	if !slices.Equal(r.adjusted, []int{volumeSteps}) || len(r.fired) != 1 || r.fired[0]["command"] != "volume_up" {
		t.Fatalf("heard in the volume up word's slot: adjusted %v, fired %v", r.adjusted, r.fired)
	}
	d.commandHeard(HangupSlot)
	if len(r.fired) != 1 {
		t.Errorf("a slot that isn't a command word's acted: %v", r.fired)
	}

	// A model that fails on the audio is left out of that word only.
	if err := config.Set().VolumeDown().ID("turn_it_down"); err != nil {
		t.Fatal(err)
	}
	for _, c := range d.commands {
		c.follow()
	}
	r.e.Clear(VolumeUpSlot) // as Engine.dropFailed does, before it calls OnDropped
	d.commandDropped(VolumeUpSlot, "turn_it_up")
	for _, c := range d.commands {
		c.follow()
	}
	if got := r.holds(VolumeUpSlot); got != "" {
		t.Errorf("the dropped model is back: %q", got)
	}
	if words[volumeUp].failed != "turn_it_up" || words[volumeDown].failed != "" {
		t.Errorf("failed: up %q, down %q", words[volumeUp].failed, words[volumeDown].failed)
	}
	if got := r.holds(CommandSlot); got != "turn_it_down" {
		t.Errorf("the volume down word was touched: holds %q", got)
	}
}

// A model chosen for two words moves between them as one lets go of it, without any word being followed by
// hand: followAll, which the selects and thresholds call, looks at all of them. And at no moment on the
// way is it in two slots at once, heard twice.
func TestASharedModelMovesWhenTheFirstLetsGo(t *testing.T) {
	logs := logged(t)
	r, _, words := newAllRig(t)
	r.playing = true
	down, up := words[volumeDown], words[volumeUp]
	for _, set := range []func(string) error{config.Set().VolumeDown().ID, config.Set().VolumeUp().ID} {
		if err := set("turn_it_down"); err != nil {
			t.Fatal(err)
		}
	}
	down.followAll()
	if r.holds(CommandSlot) != "turn_it_down" || r.holds(VolumeUpSlot) != "" {
		t.Fatalf("both chosen: down holds %q, up holds %q", r.holds(CommandSlot), r.holds(VolumeUpSlot))
	}
	if err := config.Set().VolumeDown().Threshold(config.StopOff); err != nil {
		t.Fatal(err)
	}
	down.followAll()
	if r.holds(CommandSlot) != "" || r.holds(VolumeUpSlot) != "turn_it_down" {
		t.Fatalf("volume down off: down holds %q, up holds %q", r.holds(CommandSlot), r.holds(VolumeUpSlot))
	}
	if err := config.Set().VolumeDown().Threshold(0.9); err != nil {
		t.Fatal(err)
	}
	down.followAll()
	if r.holds(CommandSlot) != "turn_it_down" || r.holds(VolumeUpSlot) != "" {
		t.Fatalf("volume down back: down holds %q, up holds %q", r.holds(CommandSlot), r.holds(VolumeUpSlot))
	}
	if got := config.Get().Wake.VolumeUp.ID; got != "turn_it_down" {
		t.Errorf("the volume up word's choice was lost: %q", got)
	}
	id, loads := heldTwice(logs.String())
	if id != "" {
		t.Errorf("%s was in two slots at once on the way", id)
	}
	if loads < 3 { // down, then up, then down again
		t.Errorf("the engine logged %d loads; want at least 3 (has its log changed?)", loads)
	}
	_ = up
}

// One thing said that crosses two command words acts as one: the one that peaked highest, even though
// the other crossed first, and higher (its threshold set higher). In the order the engine gives them:
// the first word's peak comes in before the second's, and the second joining puts the count back, so
// the first count is nothing and the second's peak is in for the last.
func TestCommandWordsHeardTogetherActAsOne(t *testing.T) {
	r, d, words := newAllRig(t)
	r.playing = true
	r.level = 30
	for _, c := range []struct {
		set func(string) error
		id  string
	}{{config.Set().VolumeDown().ID, "turn_it_down"}, {config.Set().VolumeUp().ID, "turn_it_up"}} {
		if err := c.set(c.id); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range d.commands {
		c.follow()
	}
	if d.commandsListening() != 2 {
		t.Fatalf("%d command words listening, want 2", d.commandsListening())
	}
	words[volumeUp].score = func() float64 { return 0.95 }
	words[volumeDown].score = func() float64 { return 0.91 }
	r.waits, r.timers = nil, nil
	d.commandHeard(VolumeUpSlot) // the volume up word crosses first, on a "turn it down"
	d.commandHeard(CommandSlot)
	if len(r.adjusted) != 0 || len(r.fired) != 0 {
		t.Fatalf("acted before the vote: adjusted %v, fired %v", r.adjusted, r.fired)
	}
	if len(r.timers) != 2 || r.waits[0] != commandDecide || r.waits[1] != commandDecide {
		t.Fatalf("waits %v; want two of %v, one from each crossing", r.waits, commandDecide)
	}
	d.scored(VolumeUpSlot, 0.96)
	r.timers[0]() // the first word's count: the second has joined since
	if len(r.adjusted) != 0 || len(r.fired) != 0 {
		t.Fatalf("counted before the second word's peak: adjusted %v, fired %v", r.adjusted, r.fired)
	}
	d.scored(CommandSlot, 1)
	d.scored(NextSongSlot, 1) // (not heard: not in the vote)
	r.timers[1]()
	if !slices.Equal(r.adjusted, []int{-volumeSteps}) || len(r.fired) != 1 || r.fired[0]["command"] != "volume_down" {
		t.Fatalf("adjusted %v, fired %v; want only the volume down word", r.adjusted, r.fired)
	}

	// The next thing said is a vote of its own.
	r.waits, r.timers = nil, nil
	d.commandHeard(VolumeUpSlot)
	d.scored(VolumeUpSlot, 0.95)
	if len(r.timers) != 1 {
		t.Fatalf("timers %d, want a new vote", len(r.timers))
	}
	r.timers[0]()
	if !slices.Equal(r.adjusted, []int{-volumeSteps, volumeSteps}) || len(r.fired) != 2 {
		t.Fatalf("adjusted %v, fired %v; want the volume up word on its own", r.adjusted, r.fired)
	}
}

// With one command word listening there is nothing to mistake it for: it acts at once, as it always has.
func TestOneCommandWordListeningActsAtOnce(t *testing.T) {
	r, d, _ := newAllRig(t)
	r.playing = true
	if err := config.Set().VolumeDown().ID("turn_it_down"); err != nil {
		t.Fatal(err)
	}
	for _, c := range d.commands {
		c.follow()
	}
	r.waits, r.timers = nil, nil
	d.commandHeard(CommandSlot)
	if len(r.timers) != 0 || len(r.adjusted) != 1 {
		t.Fatalf("timers %d, adjusted %v; want it to act at once", len(r.timers), r.adjusted)
	}
}

// logged has the log written to a buffer until the test ends. slog.SetDefault also points the log
// package at the new handler, and putting slog's own default back doesn't undo that, so the log
// package's writer and flags are put back too: after the default, which leaves them alone.
func logged(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	was, w, flags := slog.Default(), log.Writer(), log.Flags()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() {
		slog.SetDefault(was)
		log.SetOutput(w)
		log.SetFlags(flags)
	})
	return &buf
}

// heldTwice replays what the engine logged loading and clearing slots, from an empty engine, so a test
// sees every moment rather than only where it ended: the first model found in two slots at once, or "",
// and how many loads it read.
func heldTwice(text string) (id string, loads int) {
	held := map[string]string{} // slot: model
	for _, line := range strings.Split(text, "\n") {
		loaded, cleared := strings.Contains(line, `msg="wake word loaded"`), strings.Contains(line, `msg="wake word cleared"`)
		if !loaded && !cleared {
			continue
		}
		slot, model := logField(line, "slot"), logField(line, "id")
		if cleared {
			delete(held, slot)
			continue
		}
		loads++
		for s, other := range held {
			if s != slot && other == model {
				return model, loads
			}
		}
		held[slot] = model
	}
	return "", loads
}

// logField is the value of key in a line of the text log.
func logField(line, key string) string {
	for _, f := range strings.Fields(line) {
		if v, ok := strings.CutPrefix(f, key+"="); ok {
			return v
		}
	}
	return ""
}

// Two models of one phrase are one word to whoever says it: chosen as volume down and volume up, only
// the volume down word listens. And a later word never blocks an earlier one: next song behind volume up.
func TestOnePhraseIsOneCommandWord(t *testing.T) {
	r, d, words := newAllRig(t)
	r.playing = true
	if err := config.Set().VolumeDown().ID("turn_it_down"); err != nil {
		t.Fatal(err)
	}
	if err := config.Set().VolumeUp().ID("turn_it_down_2"); err != nil {
		t.Fatal(err)
	}
	for _, c := range d.commands {
		c.follow()
	}
	if r.holds(VolumeUpSlot) != "" {
		t.Fatalf("a second model of the same phrase listens as the volume up word: %q", r.holds(VolumeUpSlot))
	}
	if _, why := words[volumeUp].wanted(); why != "it is the volume down word too" {
		t.Errorf("why %q", why)
	}

	if err := config.Set().VolumeDown().ID(""); err != nil {
		t.Fatal(err)
	}
	if err := config.Set().VolumeUp().ID("turn_it_up"); err != nil {
		t.Fatal(err)
	}
	if err := config.Set().NextSong().ID("turn_it_up"); err != nil {
		t.Fatal(err)
	}
	for _, c := range d.commands {
		c.follow()
	}
	if r.holds(VolumeUpSlot) != "turn_it_up" || r.holds(NextSongSlot) != "" {
		t.Fatalf("up holds %q, next holds %q; want the volume up word only", r.holds(VolumeUpSlot), r.holds(NextSongSlot))
	}
	if _, why := words[nextSong].wanted(); why != "it is the volume up word too" {
		t.Errorf("why %q", why)
	}
	if _, why := words[volumeUp].wanted(); why != "" {
		t.Errorf("a later word blocks an earlier one: %q", why)
	}
}

// Each slot is judged at its own word's threshold, and the config lists the words in slot order.
func TestEachCommandWordHasItsOwnThreshold(t *testing.T) {
	fakes(t)
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	for _, c := range []struct {
		set func(float64) error
		v   float64
	}{{config.Set().VolumeDown().Threshold, 0.91}, {config.Set().VolumeUp().Threshold, 0.92}, {config.Set().NextSong().Threshold, 0.93}} {
		if err := c.set(c.v); err != nil {
			t.Fatal(err)
		}
	}
	w := config.Get().Wake
	for slot, want := range map[int]float64{CommandSlot: 0.91, VolumeUpSlot: 0.92, NextSongSlot: 0.93} {
		if got, ok := commandThreshold(w, slot); !ok || got != want {
			t.Errorf("slot %d: %v, %v; want %v", slot, got, ok, want)
		}
	}
	if _, ok := commandThreshold(w, HangupSlot); ok {
		t.Error("the hang-up word's slot read as a command word's")
	}
	for i, cw := range w.CommandWords() {
		if cw != commandKinds[i].saved(w) {
			t.Errorf("CommandWords()[%d] is not the %s", i, commandKinds[i].what)
		}
	}
}

// Restore publishes each word's own saved choice and threshold.
func TestEachCommandWordRestoresItsOwn(t *testing.T) {
	r, d, words := newAllRig(t)
	_ = r
	saved := config.Wake{
		VolumeDown: config.CommandWord{ID: "turn_it_down", Threshold: 0.91},
		VolumeUp:   config.CommandWord{ID: "turn_it_up", Threshold: 0.92},
		NextSong:   config.CommandWord{ID: "next_song", Threshold: 0.93},
	}
	d.restoreCommands(saved)
	for k, want := range map[*commandKind]struct {
		phrase string
		thr    float32
	}{volumeDown: {"turn_it_down", 0.91}, volumeUp: {"turn_it_up", 0.92}, nextSong: {"next_song", 0.93}} {
		if got := words[k].word.Get(); got != want.phrase {
			t.Errorf("%s shows %q, want %q", k.what, got, want.phrase)
		}
		if got := words[k].threshold.Get(); got != want.thr {
			t.Errorf("%s threshold %v, want %v", k.what, got, want.thr)
		}
	}
}
