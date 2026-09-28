package home

import (
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
)

// The action's sound argument is optional in both directions: an automation that wants the doorbell
// heard asks for it, one that wants a view quiet refuses it, and anything else — an absent argument
// included — is the device's own setting answering. Getting this wrong is silent: a camera that
// plays its audio when nobody asked, or one that refuses to when they did.
func TestSoundAsked(t *testing.T) {
	cases := []struct {
		arg     string
		setting bool
		want    bool
	}{
		{"", true, true},
		{"", false, false},
		{"on", false, true},
		{"ON", false, true},
		{" on ", false, true},
		{"off", true, false},
		{"Off", true, false},
		{"true", false, true},
		{"false", true, false},
		{"1", false, true},
		{"0", true, false},
		{"sometimes", true, true}, // not a word this understands: the setting answers
		{"sometimes", false, false},
	}
	for _, c := range cases {
		if got := soundAsked(c.arg, c.setting); got != c.want {
			t.Errorf("soundAsked(%q, setting=%v) = %v, want %v", c.arg, c.setting, got, c.want)
		}
	}
}

// fakeOver is the media player's side of a sound over the music, as a test can hold it: an ask hands out a
// token that says it is on its way, the state of each is what the test says it is, and what was asked for,
// stopped and given up on is recorded. What the real one does with those — the stream, the claim, the
// audio — is the media player's business, and is tested there.
type fakeOver struct {
	mu      sync.Mutex
	next    media.OverToken
	states  map[media.OverToken]media.OverState
	asked   []media.OverToken
	stopped []media.OverToken
	dropped []media.OverToken
}

// fakeOverFor swaps the real player and the call to Home Assistant for fakes, and puts them back after the
// test. The call is made to answer: what a request does when it fails is a case of its own.
func fakeOverFor(t *testing.T) *fakeOver {
	t.Helper()
	fake := &fakeOver{states: map[media.OverToken]media.OverState{}}
	prevOver, prevStream, prevPoll := over, playStream, cameraSoundPoll
	over = overPlayer{Ask: fake.ask, State: fake.state, Stop: fake.stop, Drop: fake.drop}
	playStream = func(string, string) error { return nil }
	// The watcher's poll is a second on the device: a test of what it does does not need to wait for it.
	cameraSoundPoll = time.Millisecond
	t.Cleanup(func() { over, playStream, cameraSoundPoll = prevOver, prevStream, prevPoll })
	return fake
}

func (f *fakeOver) ask() media.OverToken {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	f.asked = append(f.asked, f.next)
	f.states[f.next] = media.OverComing // on its way until the test says otherwise
	return f.next
}

func (f *fakeOver) state(t media.OverToken) media.OverState {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.states[t]; ok {
		return s
	}
	return media.OverGone
}

// Neither stopping nor giving up on anything is done with the zero token: it is what a view that never
// asked for a sound holds, and there is nothing behind it. The real player says the same by finding no
// sound and no ask of that token.
func (f *fakeOver) stop(t media.OverToken) {
	if t == 0 {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch f.states[t] {
	case media.OverComing, media.OverPlaying:
		f.stopped = append(f.stopped, t)
		delete(f.states, t)
	}
	// Anything else is nothing to stop: the sound is already over, or it was taken from this view rather
	// than stopped by it. The real player says the same by finding nothing of that token.
}

func (f *fakeOver) drop(t media.OverToken) {
	if t == 0 {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dropped = append(f.dropped, t)
	delete(f.states, t)
}

// playing and taken are the two things that can become of a sound: one that arrived, and one something
// else claimed the speaker from.
func (f *fakeOver) playing(t media.OverToken) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states[t] = media.OverPlaying
}

func (f *fakeOver) taken(t media.OverToken) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states[t] = media.OverTaken
}

func (f *fakeOver) counts() (asked, stopped, dropped int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.asked), len(f.stopped), len(f.dropped)
}

func (f *fakeOver) wasStopped(t media.OverToken) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Contains(f.stopped, t)
}

func (f *fakeOver) wasDropped(t media.OverToken) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Contains(f.dropped, t)
}

// cameraUp puts a camera on the screen for the rest of the test. sound is the entity whose audio the view
// asked for, empty for one opened without any, and token the request that sound was asked with.
func cameraUp(f *Feature, entity, sound string, token media.OverToken) {
	f.cam = CameraView{Entity: entity, Until: time.Now().Add(time.Minute)}
	f.camSound, f.camOver = sound, token
}

// The screen's control is drawn for as long as the view has a sound of its own, and it says what it will
// do: Mute while the sound is playing or on its way, Unmute when it is not. Drawn only while it plays,
// silencing would be a door that only closes, and a sound taken by an announcement would leave the view
// with no way back to it.
func TestAViewKeepsItsSoundWhetherOrNotItIsPlaying(t *testing.T) {
	fake := fakeOverFor(t)

	if (&Feature{}).CameraSoundOn() || (&Feature{}).CameraSoundLive() {
		t.Fatal("a device with no camera view was said to have a sound")
	}

	f := &Feature{}
	cameraUp(f, "camera.deck", "camera.deck", fake.ask())
	fake.playing(f.camOver)
	if !f.CameraSoundOn() {
		t.Fatal("a view with a sound was said to have none")
	}
	if !f.CameraSoundLive() {
		t.Fatal("a sound that is playing was not said to be one to silence")
	}

	// Taken by a reply or an announcement: the view keeps its sound, and the control offers to ask for it
	// again rather than saying there is something to silence.
	fake.taken(f.camOver)
	if !f.CameraSoundOn() {
		t.Fatal("a view whose sound was taken was said to have none")
	}
	if f.CameraSoundLive() {
		t.Fatal("a sound that was taken was said to be one to silence rather than to ask for")
	}

	// A view asked for without a sound has no control to draw at all.
	f2 := &Feature{}
	cameraUp(f2, "camera.deck", "", 0)
	if f2.CameraSoundOn() {
		t.Fatal("a view asked for without a sound was said to have one")
	}
}

// One control either way: a tap silences what is playing or on its way, and asks for it again when it is
// not. That is what makes muting recoverable from the screen, and a second tap that asked again while the
// first was still on its way would be two requests for one control.
func TestTappingTheControlSilencesOrAsks(t *testing.T) {
	fake := fakeOverFor(t)
	f := &Feature{}
	cameraUp(f, "camera.deck", "camera.deck", 0)

	// Nothing playing, nothing asked for: a tap asks.
	f.ToggleCameraSound()
	if asked, _, _ := fake.counts(); asked != 1 {
		t.Fatalf("%d requests were made for a sound that was not playing, want 1", asked)
	}
	if !f.CameraSoundLive() {
		t.Fatal("a sound that was just asked for is not one the control offers to silence")
	}
	token := f.camOver

	// On its way: the same tap silences it, so a stream still arriving does not start after the button
	// was pressed.
	f.ToggleCameraSound()
	if asked, stopped, _ := fake.counts(); asked != 1 || stopped != 1 {
		t.Fatalf("a tap while the sound was on its way made %d requests and %d stops, want 1 and 1", asked, stopped)
	}
	if !fake.wasStopped(token) {
		t.Fatal("silencing a sound on its way did not give that request up")
	}
	if f.CameraSoundLive() {
		t.Fatal("a silenced sound is still one the control says can be silenced")
	}

	// And the tap after that asks again, which is the way back.
	f.ToggleCameraSound()
	if asked, _, _ := fake.counts(); asked != 2 {
		t.Fatalf("a tap on a silenced view made %d requests, want one more", asked)
	}
	if f.camOver == token {
		t.Fatal("asking again kept the token of the request that was given up on")
	}
}

// A view that ends takes its sound with it, and a sound the view never had is nothing to stop: the
// watching runs out of every view that asked for one, and a camera left with its sound still playing would
// be a device nobody can silence from the screen.
func TestTheViewEndingTakesItsSoundWithIt(t *testing.T) {
	fake := fakeOverFor(t)
	f := &Feature{}
	token := fake.ask()
	cameraUp(f, "camera.deck", "camera.deck", token)
	f.cam.Until = time.Now() // over: nothing is watching the clock but this

	f.watchCameraSound("camera.deck", token)

	if !fake.wasStopped(token) {
		t.Fatal("the sound of a view that ended was left playing")
	}
	if f.CameraSoundOn() {
		t.Fatal("a sound outlived the view it belonged to, so the next view would start with it")
	}
}

// A view with no sound of its own, and one whose sound has already been silenced, are both nothing to
// stop: the watching must not take a sound off the speaker that was never this view's.
func TestTheViewEndingStopsNothingItWasNotPlaying(t *testing.T) {
	fake := fakeOverFor(t)

	f := &Feature{}
	cameraUp(f, "camera.deck", "", 0)
	f.cam.Until = time.Now()
	f.watchCameraSound("camera.deck", 0)
	if _, stopped, _ := fake.counts(); stopped != 0 {
		t.Fatalf("a view with no sound stopped %d sounds", stopped)
	}

	f2 := &Feature{}
	cameraUp(f2, "camera.deck", "camera.deck", fake.ask())
	f2.ToggleCameraSound() // silenced: the request is given up on, and the sound is not this view's
	f2.cam.Until = time.Now()
	f2.watchCameraSound("camera.deck", f2.camOver)
	if _, stopped, _ := fake.counts(); stopped != 1 {
		t.Fatalf("the sound of a view that was already silenced was stopped again (%d stops)", stopped)
	}
}

// A reply or an announcement claims the speaker, and what was playing over the music goes with it. The
// view still wants a sound, so it is asked for again — what it must not do is go on saying Mute over a
// sound that is no longer there.
func TestASoundTakenFromTheViewIsAskedForAgain(t *testing.T) {
	fake := fakeOverFor(t)
	f := &Feature{}
	cameraUp(f, "camera.deck", "camera.deck", fake.ask())
	first := f.camOver
	fake.playing(first)

	go f.watchCameraSound("camera.deck", first)
	fake.taken(first)

	eventually(t, "the sound asked for again", func() bool {
		asked, _, _ := fake.counts()
		return asked == 2
	})
	if f.camOver == first {
		t.Fatal("the request that was taken is still the one the view holds")
	}
	if f.camSound != "camera.deck" {
		t.Fatalf("the view's sound was forgotten when it was taken: %q", f.camSound)
	}
	if !f.CameraSoundLive() {
		t.Fatal("a sound asked for again is not one the control offers to silence")
	}
}

// The call that starts a stream is answered after the stream is set up, so it can fail with the sound
// already playing: a slow one times out while the camera is being heard. A sound that has arrived is left
// playing and left stoppable; a request that nothing answered is given up on, so that a url arriving
// later is dropped rather than played as a track over the room's music.
func TestAFailedCallKeepsASoundThatArrivedAndGivesUpOneThatDidNot(t *testing.T) {
	fake := fakeOverFor(t)
	playStream = func(string, string) error { return errNoStream }
	f := &Feature{}

	cameraUp(f, "camera.deck", "camera.deck", fake.ask())
	fake.playing(f.camOver)
	f.askCameraSound("camera.deck", f.camOver)
	if fake.wasDropped(f.camOver) {
		t.Fatal("a sound that had already arrived was given up on because the call failed")
	}
	if !f.CameraSoundLive() {
		t.Fatal("a sound playing under a call that failed is not one the control offers to silence")
	}

	f2 := &Feature{}
	cameraUp(f2, "camera.deck", "camera.deck", fake.ask())
	f2.askCameraSound("camera.deck", f2.camOver)
	if !fake.wasDropped(f2.camOver) {
		t.Fatal("a request that nothing answered was left waiting for a url after its call failed")
	}
	if f2.CameraSoundLive() {
		t.Fatal("a request that nothing answered is still one the control offers to silence")
	}
}

// errNoStream is a call to a camera Home Assistant will not stream: the request is refused, and there is
// no stream coming for it.
var errNoStream = errors.New("camera.play_stream: no stream for this camera")
