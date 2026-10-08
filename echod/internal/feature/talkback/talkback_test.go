//go:build !dot

package talkback

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// fakeSession is a camera that takes whatever it is sent, and hangs up when told to.
type fakeSession struct {
	mu     sync.Mutex
	closed bool
	done   chan struct{}
}

func (s *fakeSession) Write([]int16) error   { return nil }
func (s *fakeSession) Codec() string         { return "PCMU" }
func (s *fakeSession) Done() <-chan struct{} { return s.done }
func (s *fakeSession) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return nil
}

func (s *fakeSession) wasClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

const door = "camera.front_door"

// setUp is a device with the switch on and the door's address given, a screen that draws the camera
// page while showing says so, and a camera behind open that is handed back on the channel, or err.
func setUp(t *testing.T, err error) (opened chan *fakeSession, showing *atomic.Bool) {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if e := config.Set().Security().TalkBack(true); e != nil {
		t.Fatal(e)
	}
	if e := config.Set().TalkBack().Save("admin", nil, map[string]string{door: "rtsp://192.168.1.40:554/h264Preview_01_main"}); e != nil {
		t.Fatal(e)
	}
	opened = make(chan *fakeSession, 8)
	was := open
	open = func(ctx context.Context, addr, user, pass string) (session, error) {
		if err != nil {
			return nil, err
		}
		s := &fakeSession{done: make(chan struct{})}
		opened <- s
		return s, nil
	}
	wasCheck, wasDuck := check, duckRoom
	check = 10 * time.Millisecond
	ducked := &atomic.Int32{}
	duckRoom = func(on bool) {
		if on {
			ducked.Add(1)
		} else {
			ducked.Add(-1)
		}
	}
	showing = &atomic.Bool{}
	showing.Store(true)
	screen, stop := context.WithCancel(context.Background())
	go func() {
		for screen.Err() == nil {
			if showing.Load() {
				shared.Seen(door)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	t.Cleanup(func() {
		stop()
		shared.Stop()
		open, check, duckRoom = was, wasCheck, wasDuck
		if n := ducked.Load(); n != 0 {
			t.Errorf("the room was left ducked (%d)", n)
		}
		home.Get().HideCamera()
	})
	return opened, showing
}

// waitFor waits for the talk to reach phase.
func waitFor(t *testing.T, phase Phase) State {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st := shared.State(); st.Phase == phase {
			return st
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("the talk did not reach phase %d; it is %+v", phase, shared.State())
	return State{}
}

// Talk is offered only with the switch on, for a camera with somewhere to send it, and never for the
// device's own.
func TestTalkIsOfferedOnlyWhereItCanWork(t *testing.T) {
	setUp(t, nil)
	if !Offered(door) {
		t.Error("a camera with an address was not offered Talk")
	}
	for _, e := range []string{"camera.deck", home.LocalCamera, ""} {
		if Offered(e) {
			t.Errorf("%q was offered Talk", e)
		}
	}
	config.Set().Security().TalkBack(false)
	if Offered(door) {
		t.Error("Talk was offered with the switch off")
	}
}

// A talk runs while the view of its camera is up, holds the view up, and ends, letting the camera go,
// when the view closes. A view that closes is not a failure to show.
func TestATalkLastsWhileItsViewIsUp(t *testing.T) {
	opened, _ := setUp(t, nil)
	home.Get().ShowCamera(door, time.Second)
	shared.Start(door)
	s := <-opened
	waitFor(t, Talking)
	time.Sleep(1500 * time.Millisecond) // past the view's own second: the talk is holding it
	if v, up := home.Get().Camera(); !up || v.Entity != door {
		t.Fatal("the view timed out under a talk")
	}
	if st := shared.State(); st.Phase != Talking || st.Left <= 0 || st.Left > maxTalk {
		t.Fatalf("mid-talk the state is %+v", st)
	}
	home.Get().HideCamera()
	st := waitFor(t, Idle)
	if st.Error != "" {
		t.Errorf("a closed view was shown as a failure: %q", st.Error)
	}
	if !s.wasClosed() {
		t.Error("the camera was not let go")
	}
}

// Anything drawn over the camera page ends a talk: the room is sent only while the red bar is seen.
func TestATalkEndsWhenThePageIsCovered(t *testing.T) {
	opened, showing := setUp(t, nil)
	wasSeen := seenWithin
	seenWithin = 200 * time.Millisecond
	defer func() { seenWithin = wasSeen }()
	home.Get().ShowCamera(door, time.Minute)
	shared.Start(door)
	s := <-opened
	waitFor(t, Talking)
	showing.Store(false)
	waitFor(t, Idle)
	if !s.wasClosed() {
		t.Error("the camera was not let go")
	}

	// Covered before it ever showed: nothing is sent at all.
	shared.Start(door)
	<-opened
	waitFor(t, Idle)
}

// The switch going off ends a talk; a second tap on Talk does too, at once.
func TestATalkEndsWhenAskedTo(t *testing.T) {
	opened, _ := setUp(t, nil)
	home.Get().ShowCamera(door, time.Minute)
	shared.Start(door)
	<-opened
	waitFor(t, Talking)
	config.Set().Security().TalkBack(false)
	waitFor(t, Idle)

	config.Set().Security().TalkBack(true)
	shared.Toggle(door)
	s := <-opened
	waitFor(t, Talking)
	shared.Toggle(door)
	if st := shared.State(); st.Phase != Idle || !s.wasClosed() {
		t.Errorf("a second tap left %+v, closed %v", st, s.wasClosed())
	}
}

// Taps close together never leave a talk that nothing can stop: after any number of them, one Stop
// lets every camera go.
func TestTapsCloseTogetherLeaveNoTalkBehind(t *testing.T) {
	opened, _ := setUp(t, nil)
	home.Get().ShowCamera(door, time.Minute)
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); shared.Toggle(door) }()
	}
	wg.Wait()
	shared.Stop()
	close(opened)
	for s := range opened {
		if !s.wasClosed() {
			t.Fatal("a talk was left holding its camera")
		}
	}
	if shared.Busy() {
		t.Error("still busy after Stop")
	}
}

// A second tap while the camera is still being asked ends the asking at once.
func TestStopWhileConnecting(t *testing.T) {
	setUp(t, nil)
	open = func(ctx context.Context, addr, user, pass string) (session, error) {
		<-ctx.Done() // a camera that never answers
		return nil, ctx.Err()
	}
	home.Get().ShowCamera(door, time.Minute)
	shared.Start(door)
	waitFor(t, Opening)
	at := time.Now()
	shared.Toggle(door)
	if took := time.Since(at); took > time.Second {
		t.Errorf("stopping took %v", took)
	}
	if st := shared.State(); st.Phase != Idle || st.Error != "" {
		t.Errorf("after the stop: %+v", st)
	}
}

// A talk that panics is not left looking busy, which would keep the wake word off.
func TestAPanicDoesNotLeaveItBusy(t *testing.T) {
	setUp(t, nil)
	open = func(ctx context.Context, addr, user, pass string) (session, error) { panic("a bad camera") }
	home.Get().ShowCamera(door, time.Minute)
	shared.Start(door)
	if st := waitFor(t, Idle); st.Error == "" {
		t.Error("a panic showed no failure")
	}
}

// A camera that hangs up, or will not take a talk at all, says so on the camera page.
func TestACameraThatWillNotTalkSaysWhy(t *testing.T) {
	opened, _ := setUp(t, nil)
	home.Get().ShowCamera(door, time.Minute)
	check = time.Hour // only the camera ends this one
	shared.Start(door)
	s := <-opened
	waitFor(t, Talking)
	close(s.done)
	if st := waitFor(t, Idle); !strings.Contains(st.Error, "hung up") {
		t.Errorf("a hang-up showed %q", st.Error)
	}

	setUp(t, errors.New("the camera offers no talk-back channel"))
	home.Get().ShowCamera(door, time.Minute)
	shared.Start(door)
	if st := waitFor(t, Idle); st.Error != "the camera offers no talk-back channel" {
		t.Errorf("a refusal showed %q", st.Error)
	}
}

// The log gets an address without its login, and the page a failure no longer than it has room for.
func TestRedactedAndClipped(t *testing.T) {
	if got := redacted("rtsp://admin:secret@192.168.1.40:554/x"); strings.Contains(got, "secret") || strings.Contains(got, "admin") {
		t.Errorf("redacted to %q", got)
	}
	if got := []rune(clip(strings.Repeat("x", 500))); len(got) != maxError {
		t.Errorf("clipped to %d", len(got))
	}
}

// A talk goes out 12 dB under the microphones, and leaves the frame every other listener shares as it was.
func TestATalkIsQuieterThanTheMicrophones(t *testing.T) {
	heard := []int16{32767, -32768, 4000, -4000, 0}
	got := quieter(heard)
	for i, want := range []int16{8191, -8192, 1000, -1000, 0} {
		if got[i] != want {
			t.Errorf("sample %d: %d, want %d", i, got[i], want)
		}
	}
	if heard[0] != 32767 || heard[2] != 4000 {
		t.Error("the shared frame was changed")
	}
}
