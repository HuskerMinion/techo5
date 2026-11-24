package dlna

import (
	"bytes"
	"context"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/video"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// An MP3 with Fraunhofer's VBRI table is moved through by its table. The time between its entries is
// worked out in 64 bits: in an int, 32 on the device, it overflowed (GOARCH=386 shows it here).
func TestAnMP3sVBRITable(t *testing.T) {
	const entries, every, step = 10, 100, 4000
	file := make([]byte, 64<<10)
	frame := []byte{0xFF, 0xFB, 0x90, 0x00} // MPEG-1 Layer III, 128 kbit/s, 44.1 kHz: 417 bytes a frame
	copy(file, frame)
	copy(file[417:], frame)
	v := file[36:]
	copy(v, "VBRI")
	binary.BigEndian.PutUint32(v[14:], 1000) // frames
	binary.BigEndian.PutUint16(v[18:], entries)
	binary.BigEndian.PutUint16(v[20:], 1) // scale
	binary.BigEndian.PutUint16(v[22:], 2) // bytes an entry
	binary.BigEndian.PutUint16(v[24:], every)
	for e := range entries {
		binary.BigEndian.PutUint16(v[26+2*e:], step)
	}
	g := &ranger{ctx: context.Background(), length: int64(len(file)), head: file}
	m := &seekMap{length: int64(len(file))}
	if err := m.mapMP3(g, 0); err != nil {
		t.Fatal(err)
	}
	want := time.Duration(int64(every) * 1152 * int64(time.Second) / 44100)
	if m.vbriEvery != want || len(m.vbri) != entries {
		t.Fatalf("an entry every %v (want %v), %d entries", m.vbriEvery, want, len(m.vbri))
	}
	if m.dur != time.Duration(int64(1000)*1152*int64(time.Second)/44100) {
		t.Errorf("length %v", m.dur)
	}
	// 5s is in the second entry, most of the way to the third.
	if off, _ := m.place(5 * time.Second); off <= m.audioStart+step || off >= m.audioStart+2*step {
		t.Errorf("5s at byte %d, want between %d and %d", off, m.audioStart+step, m.audioStart+2*step)
	}
}

// A seek asked for and not carried out goes with its song: the song stopped from elsewhere, or a video
// that is no longer the player's, leaves nothing that would move the next one to its place.
func TestAPendingSeekGoesWithItsSong(t *testing.T) {
	stubs(t)
	oldSettle := seekSettle
	seekSettle = time.Hour
	t.Cleanup(func() { seekSettle = oldSettle })
	_, r := newRenderer()
	u := "http://192.0.2.10/a.wav"
	r.cur = song{uri: u, title: "A test tone", dur: 20 * time.Second}
	r.smap = &seekMap{uri: u, kind: "wav", length: 44 + 20*wavBytes, dataStart: 44, dataEnd: 44 + 20*wavBytes, align: 4, dur: 20 * time.Second}
	r.state, r.started = stPlaying, time.Now()
	if err := r.seekPlace(12 * time.Second); err != nil {
		t.Fatal(err)
	}
	speakerState = func() (bool, bool) { return false, false } // a station took the speaker
	if st := r.sync(); st != stStopped {
		t.Fatalf("after the speaker was taken: %s", st)
	}
	r.mu.Lock()
	if r.seeking || r.seekTimer != nil || r.seekOnPlay {
		t.Errorf("the seek outlived its song: seeking %v, timer %v", r.seeking, r.seekTimer != nil)
	}
	r.mu.Unlock()

	// A video's, likewise.
	r.cur = song{uri: "http://192.0.2.10/film.mp4", video: true, dur: 10 * time.Minute}
	r.videoID, r.state = 7, stPlaying
	videoSeekable = func(uint64) (bool, bool) { return true, true }
	currentVideo = func() video.State {
		return video.State{ID: 7, Phase: video.Playing, Dur: 10 * time.Minute, Frames: true}
	}
	if err := r.seekPlace(time.Minute); err != nil {
		t.Fatal(err)
	}
	currentVideo = func() video.State { return video.State{Phase: video.Idle} }
	if st := r.sync(); st != stStopped {
		t.Fatalf("after the video went: %s", st)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.seeking || r.seekTimer != nil {
		t.Error("the video's seek outlived it")
	}
}

// A settle timer that went off just as its seek was forgotten, and got the lock only after another
// seek had armed its own, leaves that one alone: it is neither carried out early nor left without its
// timer.
func TestAStaleSettleLeavesTheNextSeekAlone(t *testing.T) {
	stubs(t)
	oldSettle := seekSettle
	seekSettle = time.Hour
	t.Cleanup(func() { seekSettle = oldSettle })
	_, r := newRenderer()
	r.cur = song{uri: "http://192.0.2.10/film.mp4", video: true, dur: 10 * time.Minute}
	r.videoID, r.state = 7, stPlaying
	videoSeekable = func(uint64) (bool, bool) { return true, true }
	currentVideo = func() video.State {
		return video.State{ID: 7, Phase: video.Playing, Dur: 10 * time.Minute, Frames: true}
	}
	var moved atomic.Int32
	seekVideo = func(uint64, time.Duration) error { moved.Add(1); return nil }
	t.Cleanup(func() {
		r.mu.Lock()
		r.clearSeekLocked()
		r.mu.Unlock()
	})

	if err := r.seekPlace(time.Minute); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	stale := r.seekArm
	r.clearSeekLocked() // too late to stop a timer that has gone off
	r.mu.Unlock()
	if err := r.seekPlace(2 * time.Minute); err != nil {
		t.Fatal(err)
	}
	r.settled(stale) // the timer that went off gets the lock now
	r.mu.Lock()
	armed, seeking := r.seekTimer != nil, r.seeking
	r.mu.Unlock()
	if !armed || !seeking || moved.Load() != 0 {
		t.Errorf("the next seek: timer %v, seeking %v, moved %d times", armed, seeking, moved.Load())
	}
}

// A pause while a seek's place is going to the speaker holds the song there: the pause reached the
// source the seek replaced, and the new one is paused as well rather than playing on under a paused
// transport. Where the song is is the seek's place, so a seek that overtakes it and fails carries on
// from there.
func TestAPauseAsASeekLandsHolds(t *testing.T) {
	stubs(t)
	var fail atomic.Bool
	body := wav(20 * speaker.Rate)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() && r.Header.Get("Range") != "" && !strings.HasPrefix(r.Header.Get("Range"), "bytes=0-") {
			http.Error(w, "no", http.StatusInternalServerError)
			return
		}
		http.ServeContent(w, r, "a.wav", time.Time{}, bytes.NewReader(body))
	}))
	defer srv.Close()
	_, r := newRenderer()
	r.cur = song{uri: srv.URL + "/a.wav", title: "A test tone"}

	var mu sync.Mutex
	var on, landed bool
	pauses := 0
	pausedAfter := false
	speakerState = func() (bool, bool) { mu.Lock(); defer mu.Unlock(); return on, false }
	pauseSpeaker = func() {
		mu.Lock()
		defer mu.Unlock()
		pauses++
		pausedAfter = pausedAfter || landed
	}
	played := make(chan int, 8)
	var during func(n int)
	playOnSpeaker = func(src *source, _ song) {
		n := frameOf(t, src)
		mu.Lock()
		on = true
		mu.Unlock()
		if during != nil {
			during(n)
		}
		mu.Lock()
		landed = n != 0
		mu.Unlock()
		_ = src.Close()
		played <- n
	}
	next := func(what string) int {
		t.Helper()
		select {
		case n := <-played:
			return n
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: nothing reached the speaker", what)
		}
		return 0
	}
	if err := r.play(); err != nil {
		t.Fatal(err)
	}
	next("the start")
	waitFor(t, r, "mapped", func() bool { return r.smap != nil })

	// Paused just as the seek's place goes to the speaker.
	during = func(n int) {
		if n == 5*speaker.Rate {
			if err := r.pause(); err != nil {
				t.Errorf("pause: %v", err)
			}
		}
	}
	r.mu.Lock()
	r.startSeekLocked(5 * time.Second)
	r.mu.Unlock()
	next("the seek")
	waitFor(t, r, "the kept source paused", func() bool { mu.Lock(); defer mu.Unlock(); return pausedAfter })
	r.mu.Lock()
	st, pausedAt := r.state, r.pausedAt
	r.mu.Unlock()
	if st != stPaused || pausedAt.IsZero() {
		t.Errorf("after the pause: %s, paused at %v", st, pausedAt)
	}

	// Carried on, and moved again: that seek fails, and the song plays on from the first one's place.
	r.mu.Lock()
	r.seeking, r.seekOnPlay = false, false
	r.pausedFor += time.Since(r.pausedAt)
	r.state, r.pausedAt = stPlaying, time.Time{}
	r.mu.Unlock()
	fail.Store(true)
	r.mu.Lock()
	r.startSeekLocked(15 * time.Second)
	r.mu.Unlock()
	waitFor(t, r, "the failed seek over", func() bool { return r.state == stPlaying && r.seekFetch == nil })
	r.mu.Lock()
	pos := r.positionLocked()
	r.mu.Unlock()
	if pos < 5*time.Second || pos > 6*time.Second {
		t.Errorf("position %v after the failed seek, want about 5s", pos)
	}
	r.stop()
}

// A target that would overflow a time once multiplied out is not one; nor is a length or a start
// that is no number.
func TestTimesThatOverflowAreRefused(t *testing.T) {
	for _, v := range []string{"999999999999:00", "60001:00", "0:00:00.1/9999999999", "1001:00:00", "1234567:00:00"} {
		if d, ok := parseSeekTarget(v); ok {
			t.Errorf("%q taken as %v", v, d)
		}
	}
	if d, ok := parseSeekTarget("60000:00"); !ok || d != 1000*time.Hour {
		t.Errorf("60000:00: %v %v", d, ok)
	}
	if d, ok := parseSeekTarget("0:01:02.1/4"); !ok || d != 62*time.Second+250*time.Millisecond {
		t.Errorf("0:01:02.1/4: %v %v", d, ok)
	}
	for _, v := range []string{"0:00:Inf", "0:00:NaN", "0:00:1e300", "0:00:-1", "1001:00:00", "99999999999999:00:00", "0:100:00", "0:00:100", "0:00:+Inf"} {
		if d := parseHMS(v); d != 0 {
			t.Errorf("%q read as %v", v, d)
		}
	}
	if d := parseHMS("1:02:03.5"); d != time.Hour+2*time.Minute+3500*time.Millisecond {
		t.Errorf("1:02:03.5: %v", d)
	}
}

// A FLAC whose seek table has no point near the place is not moved there, rather than decoding the
// hour before it to drop.
func TestAFLACIsNotMovedFarPastItsSeekPoint(t *testing.T) {
	m := &seekMap{uri: "http://127.0.0.1:9/a.flac", kind: "flac", length: 1 << 30, rate: 48000, firstFrame: 42,
		points: []flacPoint{{0, 0}, {48000 * 3600, 1 << 20}}, dur: 2 * time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, _, err := m.open(ctx, &http.Client{}, 30*time.Minute)
	if err == nil || !strings.Contains(err.Error(), "seek table") {
		t.Errorf("half an hour past the nearest point: %v", err)
	}
	// Near a point it goes on to fetch (here, from a fetch already ended).
	_, _, _, err = m.open(ctx, &http.Client{}, time.Hour+30*time.Second)
	if err == nil || strings.Contains(err.Error(), "seek table") {
		t.Errorf("half a minute past a point: %v", err)
	}
}
