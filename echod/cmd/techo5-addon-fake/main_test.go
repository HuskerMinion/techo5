package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/pkg/addonapi"
)

// syncBuffer is output read while it is written.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}
func (s *syncBuffer) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func waitFor(t *testing.T, out *syncBuffer, what string) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if strings.Contains(out.String(), what) {
			return
		}
	}
	t.Fatalf("waited for %q in:\n%s", what, out.String())
}

// An author's add-on, run against the fake: it says hello, answers a command typed at the keyboard,
// has its requests logged, and is refused what its manifest does not use.
func TestTheFakeServesAnAddon(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("the API refuses root")
	}
	dir, err := os.MkdirTemp("", "afake")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	man := filepath.Join(dir, "addon.json")
	if err := os.WriteFile(man, []byte(`{"id":"meals","version":"0.1.0","command":["meals"],"ports":[],"root":false,
	 "memory_mb":10,"api":1,"capabilities":["commands","timers"],"commands":[{"name":"next"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	in, keys := io.Pipe()
	out := &syncBuffer{}
	done := make(chan error, 1)
	go func() { done <- run(man, "", filepath.Join(dir, "run"), "show", nil, in, out) }()
	waitFor(t, out, "export TECHO5_ADDON_API=")
	path := regexp.MustCompile(`TECHO5_ADDON_API=(\S+)`).FindStringSubmatch(out.String())[1]

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := addonapi.Dial(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if w := c.Welcome(); w.Addon != "meals" || w.Device.Width != 960 {
		t.Errorf("welcome %+v", w)
	}
	go func() {
		for ev := range c.Events() {
			var ce addonapi.CommandEvent
			if ev.Event == addonapi.EventCommand && ev.Decode(&ce) == nil && ce.Call != 0 {
				_ = c.ReplyCommand(context.Background(), ce.Call, true, "Step two")
			}
		}
	}()
	if _, err := c.StartTimer(ctx, "rice", time.Minute); err != nil {
		t.Fatal(err)
	}
	waitFor(t, out, "request timer.start")
	if _, err := c.Speak(ctx, "hi"); addonapi.CodeOf(err) != addonapi.CodeDenied {
		t.Errorf("speak without the capability: %v", err)
	}
	if _, err := io.WriteString(keys, "command next\n"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, out, `Home Assistant is answered: "Step two"`)
	_, _ = io.WriteString(keys, "quit\n")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// A manifest the device would refuse is refused here too.
func TestTheFakeReadsTheManifestAsTheDeviceDoes(t *testing.T) {
	dir := t.TempDir()
	man := filepath.Join(dir, "addon.json")
	_ = os.WriteFile(man, []byte(`{"id":"meals","version":"0.1.0","command":["meals"],"ports":[],"root":false,
	 "memory_mb":10,"api":1,"capabilities":["commands"]}`), 0o644)
	if err := run(man, "", "", "show", nil, strings.NewReader(""), io.Discard); err == nil {
		t.Error("commands without any declared was taken")
	}
}

// The fake plays an add-on's media as a device would tell of it: buffering, then playing, and what the
// keyboard says it does, its end included.
func TestTheFakeSimulatesMediaState(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("the API refuses root")
	}
	old := fakeBuffers
	fakeBuffers = 10 * time.Millisecond
	defer func() { fakeBuffers = old }()
	dir, err := os.MkdirTemp("", "afake")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	man := filepath.Join(dir, "addon.json")
	if err := os.WriteFile(man, []byte(`{"id":"plex","version":"0.1.0","command":["plex"],"ports":[],"root":false,
	 "memory_mb":10,"api":1,"capabilities":["media"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	in, keys := io.Pipe()
	out := &syncBuffer{}
	done := make(chan error, 1)
	go func() { done <- run(man, "", filepath.Join(dir, "run"), "show", nil, in, out) }()
	waitFor(t, out, "export TECHO5_ADDON_API=")
	path := regexp.MustCompile(`TECHO5_ADDON_API=(\S+)`).FindStringSubmatch(out.String())[1]
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := addonapi.Dial(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	states := c.WatchMedia()
	next := func(state, reason string) addonapi.MediaState {
		t.Helper()
		select {
		case st := <-states:
			if st.State != state || st.Reason != reason {
				t.Fatalf("media.state %+v, want %s %q", st, state, reason)
			}
			return st
		case <-time.After(5 * time.Second):
			t.Fatalf("no media.state; want %s", state)
		}
		return addonapi.MediaState{}
	}
	m, err := c.StartMedia(ctx, addonapi.MediaVideo, "http://plex.local/film.mkv", "Film")
	if err != nil {
		t.Fatal(err)
	}
	// Buffering may be gathered into playing, as a device gathers quick changes.
	if st := <-states; st.Media != m || (st.State != addonapi.MediaBuffering && st.State != addonapi.MediaPlaying) {
		t.Fatalf("first %+v", st)
	} else if st.State == addonapi.MediaBuffering {
		next(addonapi.MediaPlaying, "")
	}
	_, _ = io.WriteString(keys, "media paused 42\n")
	if st := next(addonapi.MediaPaused, ""); *st.PositionMs != 42000 {
		t.Errorf("paused at %d", *st.PositionMs)
	}
	_, _ = io.WriteString(keys, "media device\n")
	next(addonapi.MediaStopped, addonapi.MediaStoppedOnDevice)
	if st, err := c.MediaStatus(ctx); err != nil || st.Reason != addonapi.MediaStoppedOnDevice {
		t.Errorf("status %+v %v", st, err)
	}
	_, _ = io.WriteString(keys, "quit\n")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// fakeWith runs the fake for a manifest of these capabilities and family, and connects to it.
func fakeWith(t *testing.T, caps, family string) (*addonapi.Client, *io.PipeWriter, *syncBuffer, chan error) {
	t.Helper()
	if os.Getuid() == 0 {
		t.Skip("the API refuses root")
	}
	dir, err := os.MkdirTemp("", "afake")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	man := filepath.Join(dir, "addon.json")
	if err := os.WriteFile(man, []byte(`{"id":"meals","version":"0.1.0","command":["meals"],"ports":[],"root":false,
	 "memory_mb":10,"api":1,"capabilities":[`+caps+`]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	in, keys := io.Pipe()
	out := &syncBuffer{}
	done := make(chan error, 1)
	go func() { done <- run(man, "", filepath.Join(dir, "run"), family, nil, in, out) }()
	waitFor(t, out, "export TECHO5_ADDON_API=")
	path := regexp.MustCompile(`TECHO5_ADDON_API=(\S+)`).FindStringSubmatch(out.String())[1]
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := addonapi.Dial(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c, keys, out, done
}

// The fake takes an add-on's scenes as a device does, says them in its log, brings the page up when
// asked, and sends taps, swipes and the back gesture from the keyboard.
func TestTheFakeTakesScenes(t *testing.T) {
	c, keys, out, done := fakeWith(t, `"screen"`, "show")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ev := c.WatchScreen()
	page := addonapi.Column(addonapi.Title("Stir"), addonapi.Button("next", "Next", "mdi:arrow-right"))
	if err := c.SetScene(ctx, addonapi.OnPage, page); err != nil {
		t.Fatal(err)
	}
	waitFor(t, out, "the page is now 3 blocks")
	if err := c.Call(ctx, addonapi.OpSceneSet, addonapi.SceneSet{On: addonapi.OnPage, Scene: addonapi.Block{Type: "webview"}}, nil); addonapi.CodeOf(err) != addonapi.CodeBadRequest {
		t.Errorf("a scene the device would refuse: %v", err)
	}
	// Nobody asked for it: refused, as a device refuses it.
	if err := c.OpenScreen(ctx); addonapi.CodeOf(err) != addonapi.CodeBusy {
		t.Fatalf("an open nobody asked for: %v", err)
	}
	// A tap on its tile is a person asking.
	_, _ = io.WriteString(keys, "tile\n")
	waitFor(t, out, "a tap on the add-on's tile")
	if err := c.OpenScreen(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, out, "brings the page up")
	want := func(name string) addonapi.ScreenEvent {
		t.Helper()
		select {
		case e := <-ev:
			if e.Event != name {
				t.Fatalf("%+v, not %s", e, name)
			}
			return e
		case <-time.After(5 * time.Second):
			t.Fatalf("no %s", name)
		}
		return addonapi.ScreenEvent{}
	}
	if e := want(addonapi.EventScreenState); !e.State.Shown {
		t.Errorf("%+v", e)
	}
	_, _ = io.WriteString(keys, "touch next\n")
	if e := want(addonapi.EventTouch); e.Touch.ID != "next" {
		t.Errorf("%+v", e)
	}
	_, _ = io.WriteString(keys, "swipe right\n")
	if e := want(addonapi.EventSwipe); e.Swipe.Direction != addonapi.SwipeRight {
		t.Errorf("%+v", e)
	}
	_, _ = io.WriteString(keys, "back\n")
	want(addonapi.EventBack)
	_, _ = io.WriteString(keys, "scene page\n")
	waitFor(t, out, `"label":"Next"`)
	_, _ = io.WriteString(keys, "quit\n")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// A dot has no screen, and refuses the screen's ops as a device does.
func TestTheFakeDotHasNoScreen(t *testing.T) {
	c, keys, _, done := fakeWith(t, `"screen"`, "dot")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.SetScene(ctx, addonapi.OnPage, addonapi.Text("x")); addonapi.CodeOf(err) != addonapi.CodeUnavailable {
		t.Errorf("a scene on a dot: %v", err)
	}
	_, _ = io.WriteString(keys, "quit\n")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
