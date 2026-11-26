package addonhost

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/feature/video"
	"github.com/HuskerMinion/techo5/echod/pkg/addonapi"
)

// fakePlayers is the video player and the media player as an add-on's media sees them, and as the
// device's own buttons act on them: each change fires the hook the real one does.
type fakePlayers struct {
	mu     sync.Mutex
	seq    uint64
	vid    video.State
	audio  *media.Title
	paused bool
	heard  bool
	at     time.Duration

	ended          func(media.NamedEnd)
	audioCh, vidCh func()
}

func (f *fakePlayers) listen(ended func(media.NamedEnd), audio, vid func()) {
	f.mu.Lock()
	f.ended, f.audioCh, f.vidCh = ended, audio, vid
	f.mu.Unlock()
}

func (f *fakePlayers) fire(audio bool) {
	f.mu.Lock()
	fn := f.vidCh
	if audio {
		fn = f.audioCh
	}
	f.mu.Unlock()
	if fn != nil {
		fn()
	}
}

func (f *fakePlayers) playVideo(video.Request) (uint64, error) {
	f.mu.Lock()
	f.seq++
	f.vid = video.State{Phase: video.Loading, ID: f.seq, Ended: f.vid.Ended, Failed: f.vid.Failed}
	id := f.seq
	f.mu.Unlock()
	f.fire(false)
	return id, nil
}

func (f *fakePlayers) videoNow() video.State { f.mu.Lock(); defer f.mu.Unlock(); return f.vid }

func (f *fakePlayers) setVideo(phase video.Phase, pos, dur time.Duration) {
	f.mu.Lock()
	f.vid.Phase, f.vid.Pos, f.vid.Dur = phase, pos, dur
	f.mu.Unlock()
	f.fire(false)
}

func (f *fakePlayers) pauseVideo()  { f.setVideo(video.Paused, f.videoNow().Pos, f.videoNow().Dur) }
func (f *fakePlayers) resumeVideo() { f.setVideo(video.Playing, f.videoNow().Pos, f.videoNow().Dur) }

// stopVideo is video.StopID, and video.Stop (the Stop button) for the video up.
func (f *fakePlayers) stopVideo(id uint64) {
	f.mu.Lock()
	if f.vid.ID == id {
		f.vid = video.State{Phase: video.Idle, Ended: f.vid.Ended, Failed: f.vid.Failed}
	}
	f.mu.Unlock()
	f.fire(false)
}

// videoEnds is the video up playing to its end.
func (f *fakePlayers) videoEnds() {
	f.mu.Lock()
	f.vid = video.State{Phase: video.Idle, Ended: f.vid.ID, Failed: f.vid.Failed}
	f.mu.Unlock()
	f.fire(false)
}

func (f *fakePlayers) playAudio(_ string, t *media.Title) {
	f.mu.Lock()
	prev := f.audio
	f.audio, f.paused, f.heard, f.at = t, false, false, 0
	ended := f.ended
	f.mu.Unlock()
	if prev != nil && ended != nil {
		ended(media.NamedEnd{Title: prev, How: media.EndReplaced})
	}
	f.fire(true)
}

func (f *fakePlayers) audioNamed(t *media.Title) (bool, bool, time.Duration, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.audio == nil || f.audio != t {
		return false, false, 0, false
	}
	return f.paused, f.heard, f.at, true
}

func (f *fakePlayers) setAudio(paused, heard bool, at time.Duration) {
	f.mu.Lock()
	f.paused, f.heard, f.at = paused, heard, at
	f.mu.Unlock()
	f.fire(true)
}

func (f *fakePlayers) pauseAudio()  { f.mu.Lock(); f.paused = true; f.mu.Unlock(); f.fire(true) }
func (f *fakePlayers) resumeAudio() { f.mu.Lock(); f.paused = false; f.mu.Unlock(); f.fire(true) }

// stopAudio is the media player's Stop: the add-on's media.stop, or the device's Stop button.
func (f *fakePlayers) stopAudio() { f.audioEnds(media.EndStopped) }

func (f *fakePlayers) audioEnds(how media.End) {
	f.mu.Lock()
	prev := f.audio
	f.audio = nil
	ended := f.ended
	f.mu.Unlock()
	if prev != nil && ended != nil {
		ended(media.NamedEnd{Title: prev, How: how})
	}
	f.fire(true)
}

// mediaRig is an add-on granted media, connected, with its media.state events on states.
func mediaRig(t *testing.T) (*fakePlayers, *addonapi.Client, <-chan addonapi.MediaState) {
	t.Helper()
	r := newHostRig(t)
	r.g.Man.Capabilities = append(r.g.Man.Capabilities, addonapi.CapMedia)
	r.g.Agreed.Capabilities = append(r.g.Agreed.Capabilities, addonapi.CapMedia)
	fp := &fakePlayers{}
	r.h.dev.play = fp
	path, err := r.h.Open(r.g)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := addonapi.Dial(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return fp, c, c.WatchMedia()
}

func mctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return c
}

// want waits for the next media.state and checks it.
func want(t *testing.T, ch <-chan addonapi.MediaState, media, state, reason string) addonapi.MediaState {
	t.Helper()
	select {
	case st := <-ch:
		if st.Media != media || st.State != state || st.Reason != reason {
			t.Fatalf("media.state %+v, want media %s %s %q", st, media, state, reason)
		}
		return st
	case <-time.After(5 * time.Second):
		t.Fatalf("no media.state; want media %s %s %q", media, state, reason)
	}
	return addonapi.MediaState{}
}

func quiet(t *testing.T, ch <-chan addonapi.MediaState) {
	t.Helper()
	select {
	case st := <-ch:
		t.Fatalf("a media.state about what is not the add-on's: %+v", st)
	case <-time.After(addonapi.MediaCoalesce + 300*time.Millisecond):
	}
}

// An add-on's audio is followed through buffering, playing, its own pause and resume; replaced by
// something else, it is told so and nothing about what replaced it, and nothing after.
func TestAddonAudioState(t *testing.T) {
	fp, c, states := mediaRig(t)
	m, err := c.StartMedia(mctx(t), addonapi.MediaAudio, "http://192.0.2.1/a.mp3", "Episode")
	if err != nil {
		t.Fatal(err)
	}
	want(t, states, m, addonapi.MediaBuffering, "")
	fp.setAudio(false, true, 5*time.Second)
	if st := want(t, states, m, addonapi.MediaPlaying, ""); st.PositionMs == nil || *st.PositionMs != 5000 || st.DurationMs != nil {
		t.Errorf("playing %+v", st)
	}
	if err := c.PauseMedia(mctx(t)); err != nil {
		t.Fatal(err)
	}
	want(t, states, m, addonapi.MediaPaused, "")
	if err := c.ResumeMedia(mctx(t)); err != nil {
		t.Fatal(err)
	}
	want(t, states, m, addonapi.MediaPlaying, "")

	// Home Assistant plays something of its own: the add-on's is replaced.
	other := &media.Title{Title: "the news"}
	fp.playAudio("http://192.0.2.2/news.mp3", other)
	if st := want(t, states, m, addonapi.MediaStopped, addonapi.MediaReplaced); st.PositionMs == nil || *st.PositionMs != 5000 {
		t.Errorf("its end says no position: %+v", st)
	}
	// What plays now is not the add-on's: nothing of it is said, and the add-on cannot touch it.
	fp.setAudio(true, true, time.Minute)
	fp.audioEnds(media.EndItself)
	quiet(t, states)
	if err := c.PauseMedia(mctx(t)); addonapi.CodeOf(err) != addonapi.CodeBadRequest {
		t.Errorf("pausing what is not the add-on's: %v", err)
	}
	if st, err := c.MediaStatus(mctx(t)); err != nil || st.Media != m || st.State != addonapi.MediaStopped || st.Reason != addonapi.MediaReplaced {
		t.Errorf("status %+v %v", st, err)
	}
}

// The device's own Stop is told as a stop on the device, a track playing out as ended, and the
// add-on's own stop as its own.
func TestAddonAudioEnds(t *testing.T) {
	fp, c, states := mediaRig(t)
	m := start(t, c, addonapi.MediaAudio)
	want(t, states, m, addonapi.MediaBuffering, "")
	fp.stopAudio() // the Stop button
	want(t, states, m, addonapi.MediaStopped, addonapi.MediaStoppedOnDevice)

	m = start(t, c, addonapi.MediaAudio)
	want(t, states, m, addonapi.MediaBuffering, "")
	fp.audioEnds(media.EndItself)
	want(t, states, m, addonapi.MediaStopped, addonapi.MediaEnded)

	m = start(t, c, addonapi.MediaAudio)
	want(t, states, m, addonapi.MediaBuffering, "")
	if err := c.StopMedia(mctx(t)); err != nil {
		t.Fatal(err)
	}
	want(t, states, m, addonapi.MediaStopped, addonapi.MediaStoppedByAddon)
	quiet(t, states) // and once: not again as a stop on the device
}

// A track that cannot be played is told as an error.
func TestAddonAudioFails(t *testing.T) {
	fp, c, states := mediaRig(t)
	m := start(t, c, addonapi.MediaAudio)
	want(t, states, m, addonapi.MediaBuffering, "")
	fp.audioEnds(media.EndFailed)
	want(t, states, m, addonapi.MediaStopped, addonapi.MediaError)
}

// start is a media.play that has to work: three at once, then one every 30 seconds.
func start(t *testing.T, c *addonapi.Client, kind string) string {
	t.Helper()
	m, err := c.StartMedia(mctx(t), kind, "http://192.0.2.1/a", "")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// An add-on's video is followed with its position and length, its end on the device's Stop said as
// such, and its own next video ends its last first.
func TestAddonVideoState(t *testing.T) {
	fp, c, states := mediaRig(t)
	if st, err := c.MediaStatus(mctx(t)); err != nil || st.State != addonapi.MediaIdle {
		t.Errorf("status before anything %+v %v", st, err)
	}
	m, err := c.StartMedia(mctx(t), addonapi.MediaVideo, "http://192.0.2.1/film.mkv", "Film")
	if err != nil {
		t.Fatal(err)
	}
	want(t, states, m, addonapi.MediaBuffering, "")
	fp.setVideo(video.Playing, time.Minute, 2*time.Hour)
	if st := want(t, states, m, addonapi.MediaPlaying, ""); st.DurationMs == nil || *st.DurationMs != 7200000 || *st.PositionMs != 60000 {
		t.Errorf("playing %+v", st)
	}
	fp.setVideo(video.Paused, 61*time.Second, 2*time.Hour)
	want(t, states, m, addonapi.MediaPaused, "")
	if st, _ := c.MediaStatus(mctx(t)); st.State != addonapi.MediaPaused || *st.PositionMs != 61000 {
		t.Errorf("status %+v", st)
	}
	fp.stopVideo(fp.videoNow().ID) // the Stop button
	if st := want(t, states, m, addonapi.MediaStopped, addonapi.MediaStoppedOnDevice); *st.PositionMs != 61000 {
		t.Errorf("its end at %v", *st.PositionMs)
	}

	a := start(t, c, addonapi.MediaVideo)
	want(t, states, a, addonapi.MediaBuffering, "")
	b := start(t, c, addonapi.MediaVideo)
	want(t, states, a, addonapi.MediaStopped, addonapi.MediaReplaced)
	want(t, states, b, addonapi.MediaBuffering, "")
	fp.setVideo(video.Playing, 0, time.Minute)
	want(t, states, b, addonapi.MediaPlaying, "")
	fp.videoEnds()
	want(t, states, b, addonapi.MediaStopped, addonapi.MediaEnded)

	// A video somebody else puts on is not the add-on's: nothing of it is said.
	if _, err := fp.playVideo(video.Request{URL: "http://192.0.2.9/other.mp4"}); err != nil {
		t.Fatal(err)
	}
	fp.setVideo(video.Playing, time.Second, time.Hour)
	quiet(t, states)
}
