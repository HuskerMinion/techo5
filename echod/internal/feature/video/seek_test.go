//go:build !dot

package video

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// runs is what the stand-in decoder was started with, a run a line, probes and decodes.
func runs(t *testing.T, path string) (probes int, decodes [][]string) {
	t.Helper()
	b, _ := os.ReadFile(path)
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if l == "" {
			continue
		}
		a := strings.Fields(l)
		if slices.Contains(a, "pipe:3") {
			decodes = append(decodes, a)
		} else {
			probes++
		}
	}
	return probes, decodes
}

// startsAt is the -ss a decode was started with, "" for none: before -i, so the input is what moves.
func startsAt(a []string) string {
	i, in := slices.Index(a, "-ss"), slices.Index(a, "-i")
	if i < 0 || i+1 >= len(a) || i > in {
		return ""
	}
	return a[i+1]
}

func recordRuns(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "runs")
	decoderEnv = append(decoderEnv, "TECHO5_FAKE_ARGS="+p)
	return p
}

// A seek starts the decoder again at the time asked, under the same id and without asking the server
// what the video is a second time; the position counts from there, and a paused video stays paused.
func TestASeekStartsTheDecoderAgainThere(t *testing.T) {
	f := fake(t, 100000, true)
	log := recordRuns(t)
	id, err := f.Play(Request{URL: "http://192.168.1.20/a.mp4", Title: "A film"})
	if err != nil {
		t.Fatal(err)
	}
	until(t, f, Playing)
	if ok, known := f.Seekable(id); !ok || !known {
		t.Fatalf("a video with a length: seekable %v, known %v", ok, known)
	}
	if err := f.Seek(id, 90*time.Second); err != nil {
		t.Fatal(err)
	}
	if st := f.State(); st.ID != id || !st.Active() || st.Pos < 90*time.Second {
		t.Errorf("just after the seek: %+v", st)
	}
	st := until(t, f, Playing)
	if st.ID != id || st.Pos < 90*time.Second || st.Pos > 95*time.Second || st.Title != "A film" {
		t.Errorf("after the seek: %+v", st)
	}
	probes, decodes := runs(t, log)
	if probes != 1 || len(decodes) != 2 {
		t.Fatalf("%d probes and %d decodes, want 1 and 2", probes, len(decodes))
	}
	if at := startsAt(decodes[0]); at != "" {
		t.Errorf("the first decode started at %s", at)
	}
	if at := startsAt(decodes[1]); at != "90.000" {
		t.Errorf("the decode after the seek started at %q: %v", at, decodes[1])
	}

	// Paused, a seek leaves it paused where it was moved to.
	f.Pause()
	until(t, f, Paused)
	if err := f.Seek(id, 30*time.Second); err != nil {
		t.Fatal(err)
	}
	st = until(t, f, Paused)
	if st.ID != id || st.Pos < 30*time.Second || st.Pos > 31*time.Second {
		t.Errorf("a seek while paused: %+v", st)
	}
	if _, decodes = runs(t, log); len(decodes) != 3 || startsAt(decodes[2]) != "30.000" {
		t.Errorf("decodes %v", decodes)
	}
	f.Resume()
	until(t, f, Playing)

	// Another video's id moves nothing.
	if err := f.Seek(id+1, time.Second); !errors.Is(err, ErrNotSeekable) {
		t.Errorf("another id: %v", err)
	}
}

// A live stream says no length, and cannot be moved through.
func TestALiveStreamCannotBeMovedThrough(t *testing.T) {
	f := fake(t, 100000, true)
	decoderEnv = append(decoderEnv, "TECHO5_FAKE_LIVE=1")
	id, err := f.Play(Request{URL: "http://192.168.1.20/live.ts"})
	if err != nil {
		t.Fatal(err)
	}
	until(t, f, Playing)
	if ok, known := f.Seekable(id); ok || !known {
		t.Errorf("live: seekable %v, known %v", ok, known)
	}
	if err := f.Seek(id, time.Minute); !errors.Is(err, ErrNotSeekable) {
		t.Errorf("live: %v", err)
	}
}

// A seek before the decoder starts (the controller seeking straight after Play) is where it starts.
func TestASeekBeforeTheDecodeIsWhereItStarts(t *testing.T) {
	f := fake(t, 100000, true)
	log := recordRuns(t)
	id, err := f.Play(Request{URL: "http://192.168.1.20/a.mp4", Start: 0})
	if err != nil {
		t.Fatal(err)
	}
	// The probe has not answered yet in the moment after Play, nearly always: either way the video
	// ends up at the time asked, by one decode or two.
	if err := f.Seek(id, 2*time.Minute); err != nil {
		t.Fatal(err)
	}
	st := until(t, f, Playing)
	if st.Pos < 2*time.Minute {
		t.Errorf("position %v", st.Pos)
	}
	_, decodes := runs(t, log)
	if len(decodes) == 0 || startsAt(decodes[len(decodes)-1]) != "120.000" {
		t.Errorf("decodes %v", decodes)
	}
}

func TestTheDecoderIsMovedBeforeItsInput(t *testing.T) {
	a := DecodeArgs(Decoding{URL: "http://192.168.1.20/a.mp4", Info: Info{Width: 640, Height: 360, AudioIndex: -1}, Start: 83500 * time.Millisecond})
	if at := startsAt(a); at != "83.500" {
		t.Errorf("-ss %q in %v", at, a)
	}
	if a := DecodeArgs(Decoding{URL: "http://192.168.1.20/a.mp4", Info: Info{Width: 640, Height: 360, AudioIndex: -1}}); slices.Contains(a, "-ss") {
		t.Errorf("from the start: %v", a)
	}
}

// A seek to the very end lands a little short of it: the decoder started at the end has no frame to
// show, which is a video that failed, and one started past it would start the video again.
func TestASeekToTheEndLandsShortOfIt(t *testing.T) {
	f := fake(t, 100000, true)
	log := recordRuns(t)
	id, err := f.Play(Request{URL: "http://192.168.1.20/a.mp4"})
	if err != nil {
		t.Fatal(err)
	}
	st := until(t, f, Playing)
	if err := f.Seek(id, st.Dur); err != nil {
		t.Fatal(err)
	}
	st = until(t, f, Playing)
	if st.ID != id || st.Failed == id {
		t.Errorf("after a seek to the end: %+v", st)
	}
	_, decodes := runs(t, log)
	if len(decodes) != 2 || startsAt(decodes[1]) != "633.530" {
		t.Errorf("decodes %v, want the second from 633.530 (the 10:34.53 film's end, less a second)", decodes)
	}
	if got := shortOfEnd(time.Hour, 500*time.Millisecond); got != 0 {
		t.Errorf("past the end of half a second: %v", got)
	}
}

// The session a seek replaced shares its id with the one in its place: its end, by itself or failing
// as it was replaced, is not that one's.
func TestAReplacedSessionsEndIsNotItsSuccessors(t *testing.T) {
	f := fake(t, 10, false)
	old := newSession(5, Request{}, nil, f.scr, f.panelW, f.panelH)
	ns := newSession(5, Request{}, nil, f.scr, f.panelW, f.panelH)
	f.mu.Lock()
	f.cur = ns
	f.mu.Unlock()
	f.sessionEnded(old, errors.New("the decoder could not be started"), true)
	f.mu.Lock()
	failed, ended := f.failed, f.ended
	f.cur = nil
	f.mu.Unlock()
	if failed == 5 || ended == 5 {
		t.Errorf("the replaced session marked its successor: failed %d, ended %d", failed, ended)
	}
}

// A session stopped from outside before its decoder starts has not failed: what failed after the stop
// failed because of it.
func TestAStoppedSessionHasNotFailed(t *testing.T) {
	f := fake(t, 10, false)
	s := newSession(6, Request{}, nil, f.scr, f.panelW, f.panelH)
	s.probed = true
	var got error
	var told bool
	s.ended = func(_ *session, err error, _ bool) { got, told = err, true }
	s.changed = func() {}
	s.stop(true)
	s.run(false)
	if !told || got != nil {
		t.Errorf("a stopped session ended with %v (told %v)", got, told)
	}
}
