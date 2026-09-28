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

// fakeOver is the media player's side of a sound over the music, as a test can hold it. An ask hands out a
// token that says it is on its way, a token is playing once the test says its stream arrived, muting keeps
// it connected and silent, and what was asked for, muted, stopped and given up on is recorded. What the
// real one does with those — the stream, the claim, the audio — is the media player's business, and is
// tested there.
type fakeOver struct {
	mu       sync.Mutex
	next     media.OverToken
	states   map[media.OverToken]media.OverState // coming, or playing once its stream arrived
	terminal map[media.OverToken]media.OverState // and what became of the ones that are over
	muted    map[media.OverToken]bool
	asked    []media.OverToken
	stopped  []media.OverToken
	dropped  []media.OverToken
}

// fakeOverFor swaps the real player and the call to Home Assistant for fakes, and puts them back after the
// test. The call is made to answer: what a request does when it fails is a case of its own.
func fakeOverFor(t *testing.T) *fakeOver {
	t.Helper()
	fake := &fakeOver{
		states:   map[media.OverToken]media.OverState{},
		terminal: map[media.OverToken]media.OverState{},
		muted:    map[media.OverToken]bool{},
	}
	prevOver, prevStream, prevPoll := over, playStream, cameraSoundPoll
	over = overPlayer{Ask: fake.ask, State: fake.state, Mute: fake.mute, Stop: fake.stop, Drop: fake.drop}
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
	f.states[f.next] = media.OverComing
	return f.next
}

func (f *fakeOver) state(t media.OverToken) media.OverState {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, over := f.terminal[t]; over {
		return s
	}
	if _, ok := f.states[t]; !ok {
		return media.OverGone // including the zero token, which is what a view without a sound holds
	}
	if f.muted[t] {
		return media.OverMuted
	}
	return f.states[t]
}

// mute keeps a sound: it is connected and silent, and the state says so while it lasts. The zero token is
// nothing to mute, and neither is a sound that is already over — the real player finds no sound and no ask
// of either, and does nothing.
func (f *fakeOver) mute(t media.OverToken, on bool) {
	if t == 0 {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, over := f.terminal[t]; over {
		return
	}
	f.muted[t] = on
}

// stop and drop are the end of a request: the sound is given up, not silenced. The zero token is what a
// view that never asked for a sound holds, and there is nothing behind it.
func (f *fakeOver) stop(t media.OverToken) {
	f.over(t, &f.stopped)
}

func (f *fakeOver) drop(t media.OverToken) {
	f.over(t, &f.dropped)
}

func (f *fakeOver) over(t media.OverToken, into *[]media.OverToken) {
	if t == 0 {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, done := f.terminal[t]; done {
		return // already over: nothing left to stop or give up
	}
	*into = append(*into, t)
	f.terminal[t] = media.OverGone
}

// playing and taken are the two things that can become of a sound: one whose stream arrived, and one
// something else claimed the speaker from.
func (f *fakeOver) playing(t media.OverToken) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states[t] = media.OverPlaying
}

func (f *fakeOver) taken(t media.OverToken) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.terminal[t] = media.OverTaken
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

func (f *fakeOver) mutedNow(t media.OverToken) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.muted[t]
}

// cameraUp puts a camera on the screen for the rest of the test. sound is the entity whose audio the view
// asked for, empty for one opened without any, and token the request that sound was asked with.
//
// The view is taken down again as the test ends, because a watcher left running would go on polling into
// the next test — where the player is a fake that never handed its token out, and where a token that
// happens to match is another test's sound entirely.
func cameraUp(t *testing.T, f *Feature, entity, sound string, token media.OverToken) {
	t.Helper()
	f.cam = CameraView{Entity: entity, Until: time.Now().Add(time.Minute)}
	f.camSound, f.camOver = sound, token
	t.Cleanup(func() {
		f.mu.Lock()
		f.cam.Until = time.Now()
		f.mu.Unlock()
		time.Sleep(5 * cameraSoundPoll) // a poll or two, while this test's fake is still the one in place
	})
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
	cameraUp(t, f, "camera.deck", "camera.deck", fake.ask())
	fake.playing(f.camOver)
	if !f.CameraSoundOn() {
		t.Fatal("a view with a sound was said to have none")
	}
	if !f.CameraSoundLive() {
		t.Fatal("a sound that is playing was not said to be one to silence")
	}

	// Connected and silenced: the view keeps its sound, and the control offers to bring it back.
	fake.mute(f.camOver, true)
	if !f.CameraSoundOn() {
		t.Fatal("a view whose sound was silenced was said to have none")
	}
	if f.CameraSoundLive() {
		t.Fatal("a silenced sound was said to be one to silence rather than to bring back")
	}

	// Taken by a reply or an announcement: the same, and it can be asked for again.
	fake.mute(f.camOver, false)
	fake.taken(f.camOver)
	if !f.CameraSoundOn() {
		t.Fatal("a view whose sound was taken was said to have none")
	}
	if f.CameraSoundLive() {
		t.Fatal("a sound that was taken was said to be one to silence")
	}

	// A view asked for without a sound has no control to draw at all.
	f2 := &Feature{}
	cameraUp(t, f2, "camera.deck", "", 0)
	if f2.CameraSoundOn() {
		t.Fatal("a view asked for without a sound was said to have one")
	}
}

// One control either way: a tap silences the sound, and the tap after that brings it back. Silencing keeps
// the stream — it is read and thrown away — so what comes back is the sound that was already connected
// rather than a second request and another few seconds.
func TestTappingTheControlSilencesAndBringsItBack(t *testing.T) {
	fake := fakeOverFor(t)
	f := &Feature{}
	cameraUp(t, f, "camera.deck", "camera.deck", 0)

	// Nothing playing and nothing asked for: a tap asks.
	f.ToggleCameraSound()
	if asked, _, _ := fake.counts(); asked != 1 {
		t.Fatalf("%d requests were made for a sound that was not playing, want 1", asked)
	}
	token := f.camOver
	if !f.CameraSoundLive() {
		t.Fatal("a sound that was just asked for is not one the control offers to silence")
	}

	// Still connecting: the tap silences it rather than giving the request up, so that the stream keeps
	// coming and can be brought back without another call.
	f.ToggleCameraSound()
	if asked, stopped, dropped := fake.counts(); asked != 1 || stopped != 0 || dropped != 0 {
		t.Fatalf("a tap while the sound was on its way made %d requests, %d stops and %d give-ups, want one request and nothing else",
			asked, stopped, dropped)
	}
	if !fake.mutedNow(token) {
		t.Fatal("a tap while the sound was on its way did not silence it")
	}
	if f.camOver != token {
		t.Fatal("silencing a sound on its way replaced the request it was silencing")
	}
	if f.CameraSoundLive() {
		t.Fatal("a silenced sound is still one the control says can be silenced")
	}

	// And the tap after that brings the sound back, on the same request.
	f.ToggleCameraSound()
	if asked, _, _ := fake.counts(); asked != 1 {
		t.Fatalf("bringing the sound back made %d requests, want the one it already had", asked)
	}
	if fake.mutedNow(token) {
		t.Fatal("a tap on a silenced sound did not bring it back")
	}
	if !f.CameraSoundLive() {
		t.Fatal("a sound brought back is not one the control offers to silence")
	}
}

// A sound that is not there at all — one that was never answered, or one something took the speaker from —
// is one to ask for. A tap asks for it audibly, whatever was true of the sound before: the tap is somebody
// saying they want to hear it.
func TestTappingAsksForASoundThatIsNotThere(t *testing.T) {
	fake := fakeOverFor(t)
	f := &Feature{}
	cameraUp(t, f, "camera.deck", "camera.deck", fake.ask())
	first := f.camOver

	// Never answered: the call was refused, so the request is over rather than on its way.
	fake.drop(first)
	f.ToggleCameraSound()
	if asked, _, _ := fake.counts(); asked != 2 {
		t.Fatalf("a tap on a sound that never arrived made %d requests, want one more", asked)
	}
	if fake.mutedNow(f.camOver) {
		t.Fatal("a tap that asks for a sound to hear asked for it silenced")
	}

	// And taken by an announcement, having been silenced first.
	fake.mute(f.camOver, true)
	f.camMuted = true
	fake.taken(f.camOver)
	f.ToggleCameraSound()
	if fake.mutedNow(f.camOver) {
		t.Fatal("a tap that asks again for a sound asked for it silenced")
	}
	if f.camOver == first {
		t.Fatal("asking again kept the token of the request that was taken")
	}
	if !f.CameraSoundLive() {
		t.Fatal("a sound asked for is not one the control offers to silence")
	}
}

// A view that ends takes its sound with it, and a sound the view never had is nothing to stop: the watching
// runs out of every view that asked for one, and a camera left with its sound still playing would be a
// device nobody can silence from the screen.
func TestTheViewEndingTakesItsSoundWithIt(t *testing.T) {
	fake := fakeOverFor(t)
	f := &Feature{}
	token := fake.ask()
	cameraUp(t, f, "camera.deck", "camera.deck", token)
	f.cam.Until = time.Now() // over: nothing is watching the clock but this

	f.watchCameraSound("camera.deck", token)

	if !fake.wasStopped(token) {
		t.Fatal("the sound of a view that ended was left playing")
	}
	if f.CameraSoundOn() {
		t.Fatal("a sound outlived the view it belonged to, so the next view would start with it")
	}
}

// Silencing a sound is not stopping it, and the view ending is what stops it: the watching must not take a
// sound off the speaker that the view gave up on already, or that it never had.
func TestTheViewEndingStopsNothingItWasNotPlaying(t *testing.T) {
	fake := fakeOverFor(t)

	f := &Feature{}
	cameraUp(t, f, "camera.deck", "", 0)
	f.cam.Until = time.Now()
	f.watchCameraSound("camera.deck", 0)
	if _, stopped, _ := fake.counts(); stopped != 0 {
		t.Fatalf("a view with no sound stopped %d sounds", stopped)
	}

	f2 := &Feature{}
	cameraUp(t, f2, "camera.deck", "camera.deck", fake.ask())
	f2.ToggleCameraSound() // silenced from the screen: connected and quiet, not given up
	if _, stopped, _ := fake.counts(); stopped != 0 {
		t.Fatalf("silencing a sound stopped it (%d stops)", stopped)
	}
	f2.cam.Until = time.Now()
	f2.watchCameraSound("camera.deck", f2.camOver)
	if _, stopped, _ := fake.counts(); stopped != 1 {
		t.Fatalf("the sound of a view that ended was stopped %d times, want once", stopped)
	}
}

// A reply or an announcement claims the speaker, and what was playing over the music goes with it. The view
// still wants a sound, so it is asked for again — and a sound silenced from the screen is asked for again
// silenced, because it is asked for so that it is there to bring back, not so that it starts talking.
func TestASoundTakenFromTheViewIsAskedForAgain(t *testing.T) {
	fake := fakeOverFor(t)
	f := &Feature{}
	cameraUp(t, f, "camera.deck", "camera.deck", fake.ask())
	first := f.camOver
	fake.playing(first)
	fake.mute(first, true)
	f.camMuted = true

	go f.watchCameraSound("camera.deck", first)
	fake.taken(first)

	eventually(t, "the sound asked for again", func() bool {
		asked, _, _ := fake.counts()
		return asked == 2
	})
	if f.camOver == first {
		t.Fatal("the request that was taken is still the one the view holds")
	}
	if !fake.mutedNow(f.camOver) {
		t.Fatal("a silenced sound was asked for again audible, which would have it start talking")
	}
	if f.camSound != "camera.deck" {
		t.Fatalf("the view's sound was forgotten when it was taken: %q", f.camSound)
	}
	if f.CameraSoundLive() {
		t.Fatal("a sound asked for again silenced is one to bring back, not one to silence")
	}
}

// The call that starts a stream is answered after the stream is set up, so it can fail with the sound
// already playing: a slow one times out while the camera is being heard. A sound that has arrived is left
// playing and left stoppable; a request that nothing answered is given up on, so that a url arriving later
// is dropped rather than played as a track over the room's music.
func TestAFailedCallKeepsASoundThatArrivedAndGivesUpOneThatDidNot(t *testing.T) {
	fake := fakeOverFor(t)
	playStream = func(string, string) error { return errNoStream }
	f := &Feature{}

	cameraUp(t, f, "camera.deck", "camera.deck", fake.ask())
	fake.playing(f.camOver)
	f.askCameraSound("camera.deck", f.camOver)
	if fake.wasDropped(f.camOver) {
		t.Fatal("a sound that had already arrived was given up on because the call failed")
	}
	if !f.CameraSoundLive() {
		t.Fatal("a sound playing under a call that failed is not one the control offers to silence")
	}

	f2 := &Feature{}
	cameraUp(t, f2, "camera.deck", "camera.deck", fake.ask())
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
