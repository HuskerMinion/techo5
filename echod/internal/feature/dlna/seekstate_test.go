package dlna

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"math"
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

// The seek's states: a seek asked for while a song starts, one overtaken as it lands, one that fails,
// one the speaker is taken from, and how a song's map is kept.

// fakeSpeaker is a speaker that holds the renderer's song from the moment one is handed to it until
// the test takes it away, telling each song's first frame to played.
type fakeSpeaker struct {
	on     atomic.Bool
	played chan int
	// handed, when set, is called with each source handed over, in place of reading its first frame.
	handed func(src *source) int
}

func useFakeSpeaker(t *testing.T) *fakeSpeaker {
	t.Helper()
	stubs(t)
	sp := &fakeSpeaker{played: make(chan int, 8)}
	speakerState = func() (bool, bool) { return sp.on.Load(), false }
	playOnSpeaker = func(src *source, _ song) {
		sp.on.Store(true)
		if sp.handed != nil {
			sp.played <- sp.handed(src)
			return
		}
		sp.played <- frameOf(t, src)
		_ = src.Close()
	}
	return sp
}

func (sp *fakeSpeaker) next(t *testing.T, what string) int {
	t.Helper()
	select {
	case n := <-sp.played:
		return n
	case <-time.After(5 * time.Second):
		t.Fatalf("%s: nothing reached the speaker", what)
	}
	return 0
}

// waitFor polls cond, under the renderer's lock, until it holds.
func waitFor(t *testing.T, r *renderer, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		r.mu.Lock()
		ok := cond()
		r.mu.Unlock()
		if ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("never %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// gated serves body at /name, Range requests answered; a whole-file request waits for the gate while
// it is closed, and noRanges says the server sends no parts (Accept-Ranges: none).
type gated struct {
	mu       sync.Mutex
	closed   bool
	open     chan struct{}
	arrived  chan struct{}
	noRanges bool
}

func serveGated(t *testing.T, name string, body []byte, g *gated) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") == "" {
			g.mu.Lock()
			closed, open := g.closed, g.open
			g.mu.Unlock()
			if closed {
				g.arrived <- struct{}{}
				select {
				case <-open:
				case <-r.Context().Done():
					return
				}
			}
		}
		if g.noRanges {
			w.Header().Set("Accept-Ranges", "none")
			_, _ = w.Write(body)
			return
		}
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/" + name
}

func (g *gated) close() {
	g.mu.Lock()
	g.closed, g.open, g.arrived = true, make(chan struct{}), make(chan struct{}, 4)
	g.mu.Unlock()
}

func (g *gated) release() {
	g.mu.Lock()
	g.closed = false
	close(g.open)
	g.mu.Unlock()
}

func newRenderer() (*Feature, *renderer) {
	f := &Feature{}
	f.r.f = f
	return f, &f.r
}

func quickSettle(t *testing.T) {
	t.Helper()
	old := seekSettle
	seekSettle = time.Millisecond
	t.Cleanup(func() { seekSettle = old })
}

// A seek asked for while a song is starting, of a song that cannot be moved through (its server sends
// no parts), is forgotten once it has started: the position does not stay where the seek asked.
func TestASeekWhileStartingASongThatCannotMoveIsForgotten(t *testing.T) {
	sp := useFakeSpeaker(t)
	quickSettle(t)
	g := &gated{noRanges: true}
	g.close()
	f, r := newRenderer()
	r.cur = song{uri: serveGated(t, "a.wav", wav(20*speaker.Rate), g), title: "A test tone", dur: 20 * time.Second}
	if err := r.play(); err != nil {
		t.Fatal(err)
	}
	<-g.arrived
	if code, b := call(t, f, "AVTransport", "Seek", seekArgs("REL_TIME", "0:00:12")); code != http.StatusOK {
		t.Fatalf("Seek while starting: %d %s", code, b)
	}
	waitFor(t, r, "settled", func() bool { return r.seekTimer == nil })
	g.release()
	if n := sp.next(t, "the start"); n != 0 {
		t.Errorf("started at frame %d", n)
	}
	waitFor(t, r, "the seek forgotten", func() bool { return !r.seeking })
	r.mu.Lock()
	pos, st := r.positionLocked(), r.state
	r.mu.Unlock()
	if st != stPlaying || pos >= 12*time.Second {
		t.Errorf("after the start: %s at %v", st, pos)
	}
	r.stop()
}

// Play again after a stop, of a song mapped before, with a Seek while it starts: the song starts and
// is then moved, where the seek used to throw the start away and stop the transport.
func TestASeekWhileAMappedSongStartsAgainIsCarriedOut(t *testing.T) {
	sp := useFakeSpeaker(t)
	quickSettle(t)
	g := &gated{}
	f, r := newRenderer()
	r.cur = song{uri: serveGated(t, "a.wav", wav(20*speaker.Rate), g), title: "A test tone"}
	if err := r.play(); err != nil {
		t.Fatal(err)
	}
	sp.next(t, "the first start")
	waitFor(t, r, "mapped", func() bool { return r.smap != nil })
	r.stop()
	sp.on.Store(false) // the stop took the song off the speaker

	g.close()
	if err := r.play(); err != nil {
		t.Fatal(err)
	}
	<-g.arrived
	if code, b := call(t, f, "AVTransport", "Seek", seekArgs("REL_TIME", "0:00:07")); code != http.StatusOK {
		t.Fatalf("Seek while starting: %d %s", code, b)
	}
	waitFor(t, r, "settled", func() bool { return r.seekTimer == nil })
	g.release()
	if n := sp.next(t, "the start again"); n != 0 {
		t.Errorf("started again at frame %d", n)
	}
	if n := sp.next(t, "the seek"); n != 7*speaker.Rate {
		t.Errorf("moved to frame %d, want %d", n, 7*speaker.Rate)
	}
	waitFor(t, r, "playing from the seek", func() bool { return r.state == stPlaying && !r.seeking })
	r.mu.Lock()
	pos := r.positionLocked()
	r.mu.Unlock()
	if pos < 7*time.Second || pos > 8*time.Second {
		t.Errorf("position %v after the seek", pos)
	}
	r.stop()
}

// A seek from paused that fails carries the song on from where it was, its time paused not counted
// and not counted again: the position moves on from there.
func TestAFailedSeekFromPausedCarriesOnFromWhereItWas(t *testing.T) {
	stubs(t)
	speakerState = func() (bool, bool) { return true, true } // the song paused on the speaker
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no", http.StatusInternalServerError)
	}))
	defer srv.Close()
	_, r := newRenderer()
	u := srv.URL + "/a.wav"
	now := time.Now()
	r.cur = song{uri: u, title: "A test tone", dur: 20 * time.Second}
	r.smap = &seekMap{uri: u, kind: "wav", length: 44 + 20*wavBytes, dataStart: 44, dataEnd: 44 + 20*wavBytes, align: 4, dur: 20 * time.Second}
	r.state, r.started, r.pausedAt = stPaused, now.Add(-10*time.Second), now.Add(-4*time.Second)
	r.seeking, r.seekOnPlay, r.seekTo = true, true, 15*time.Second
	if err := r.play(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, r, "the seek over", func() bool { return r.state != stLoading })
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state != stPlaying || !r.pausedAt.IsZero() || r.seeking {
		t.Fatalf("after the failed seek: %s, paused at %v, seeking %v", r.state, r.pausedAt, r.seeking)
	}
	if pos := r.positionLocked(); pos < 6*time.Second || pos > 7*time.Second {
		t.Errorf("position %v, want about 6s (10s in, 4s of it paused)", pos)
	}
}

// wavBytes is a second of the speaker's WAV.
const wavBytes = speaker.Rate * 4

// A Play while the video the player has is starting (or starting again after a seek) leaves it be,
// where it used to start the video again from its beginning; a Pause then is not available (701).
func TestAPlayWhileAVideoLoadsLeavesItBe(t *testing.T) {
	stubs(t)
	oldPlay := playVideo
	t.Cleanup(func() { playVideo = oldPlay })
	playVideo = func(video.Request) (uint64, error) {
		t.Error("the video was started again")
		return 8, nil
	}
	currentVideo = func() video.State { return video.State{ID: 7, Phase: video.Loading, Dur: 10 * time.Minute} }
	f, r := newRenderer()
	r.cur = song{uri: "http://192.0.2.10/film.mp4", video: true, dur: 10 * time.Minute}
	r.videoID, r.state = 7, stLoading
	if code, b := call(t, f, "AVTransport", "Play", `<InstanceID>0</InstanceID><Speed>1</Speed>`); code != http.StatusOK {
		t.Fatalf("Play: %d %s", code, b)
	}
	if r.videoID != 7 || r.state != stLoading {
		t.Errorf("after Play: video %d, %s", r.videoID, r.state)
	}
	if code, b := call(t, f, "AVTransport", "Pause", `<InstanceID>0</InstanceID>`); code != 500 || !strings.Contains(b, "<errorCode>701</errorCode>") {
		t.Errorf("Pause while loading: %d %s", code, b)
	}
}

// A seek overtaken just as its place reaches the speaker does not end what the speaker now has: the
// next seek takes over from it.
func TestASeekOvertakenAsItLandsKeepsItsSound(t *testing.T) {
	sp := useFakeSpeaker(t)
	_, r := newRenderer()
	r.cur = song{uri: serve(t, "a.wav", wav(20*speaker.Rate)) + "/a.wav", title: "A test tone"}
	if err := r.play(); err != nil {
		t.Fatal(err)
	}
	sp.next(t, "the start")
	waitFor(t, r, "mapped", func() bool { return r.smap != nil })

	var readErr error
	sp.handed = func(src *source) int {
		n := frameOf(t, src)
		if n == 5*speaker.Rate {
			// The first seek's place has just reached the speaker: another seek overtakes it now, and
			// what the speaker has must still play.
			r.mu.Lock()
			r.startSeekLocked(15 * time.Second)
			r.mu.Unlock()
			_, readErr = io.ReadFull(src, make([]byte, 1<<20))
		}
		_ = src.Close()
		return n
	}
	r.mu.Lock()
	r.startSeekLocked(5 * time.Second)
	r.mu.Unlock()
	// The second can land while the first's sound is still being read: either may be told first.
	a, b := sp.next(t, "a seek"), sp.next(t, "the other seek")
	if min(a, b) != 5*speaker.Rate || max(a, b) != 15*speaker.Rate {
		t.Fatalf("the seeks at frames %d and %d", a, b)
	}
	if readErr != nil {
		t.Errorf("the overtaken seek's sound ended: %v", readErr)
	}
	waitFor(t, r, "playing from the second seek", func() bool { return r.state == stPlaying })
	r.stop()
}

// Something else taking the speaker while a seek fetches stops the transport for good: the fetch,
// ended by that, does not carry the song on as a seek that failed would.
func TestASeekWhoseSpeakerIsTakenStaysStopped(t *testing.T) {
	sp := useFakeSpeaker(t)
	hang := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer hang.Close()
	_, r := newRenderer()
	u := hang.URL + "/a.wav"
	r.cur = song{uri: u, title: "A test tone", dur: 20 * time.Second}
	r.smap = &seekMap{uri: u, kind: "wav", length: 44 + 20*wavBytes, dataStart: 44, dataEnd: 44 + 20*wavBytes, align: 4, dur: 20 * time.Second}
	r.state, r.started = stPlaying, time.Now()
	sp.on.Store(true)
	r.mu.Lock()
	r.startSeekLocked(5 * time.Second)
	gen := r.gen
	r.mu.Unlock()
	sp.on.Store(false) // a station took the speaker
	if st := r.sync(); st != stStopped {
		t.Fatalf("after the speaker was taken: %s", st)
	}
	r.mu.Lock()
	if r.gen == gen {
		t.Error("the seek's start was not overtaken")
	}
	r.mu.Unlock()
	time.Sleep(200 * time.Millisecond) // the fetch's end, had it carried the song on
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state != stStopped {
		t.Errorf("the ended fetch left the transport %s", r.state)
	}
}

// A song's map is the current song's only: a song set (the same address again too, whose file may have
// changed) is mapped afresh, and a mapping under way ends with the song.
func TestASongsMapIsKeptForItAlone(t *testing.T) {
	sp := useFakeSpeaker(t)
	started := make(chan struct{}, 4)
	ended := make(chan struct{}, 4)
	body := wav(20 * speaker.Rate)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			// The map's fetch: held until the mapping is ended.
			started <- struct{}{}
			<-r.Context().Done()
			ended <- struct{}{}
			return
		}
		http.ServeContent(w, r, "a.wav", time.Time{}, bytes.NewReader(body))
	}))
	defer srv.Close()
	_, r := newRenderer()
	u := srv.URL + "/a.wav"
	r.cur = song{uri: u, title: "A test tone"}
	if err := r.play(); err != nil {
		t.Fatal(err)
	}
	sp.next(t, "the start")
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("never mapped")
	}
	r.stop()
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the mapping went on after the stop")
	}
	r.mu.Lock()
	if r.mapping != "" || r.mapCancel != nil {
		t.Errorf("after the stop: mapping %q", r.mapping)
	}
	r.smap = &seekMap{uri: u, kind: "wav"}
	r.mu.Unlock()
	if err := r.set(u, "", "test"); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.smap != nil {
		t.Error("the song set again kept the old map")
	}
}

// flacFile is a FLAC's headers (STREAMINFO at rate with total samples, then each table as a SEEKTABLE
// of its points, the last block last) and frames bytes after them.
func flacFile(rate int, total uint64, frames int, tables ...[]flacPoint) []byte {
	var b bytes.Buffer
	b.WriteString("fLaC")
	block := func(typ byte, last bool, body []byte) {
		if last {
			typ |= 0x80
		}
		n := len(body)
		b.Write([]byte{typ, byte(n >> 16), byte(n >> 8), byte(n)})
		b.Write(body)
	}
	info := make([]byte, 34)
	// The rate in 20 bits from byte 10, the total in 36 bits ending at byte 18: all mapFLAC reads.
	info[10], info[11], info[12] = byte(rate>>12), byte(rate>>4), byte(rate<<4)
	info[13] = byte(total >> 32 & 0x0f)
	binary.BigEndian.PutUint32(info[14:], uint32(total))
	block(0, len(tables) == 0, info)
	for i, tb := range tables {
		body := make([]byte, 18*len(tb))
		for j, p := range tb {
			binary.BigEndian.PutUint64(body[18*j:], p.sample)
			binary.BigEndian.PutUint64(body[18*j+8:], p.off)
		}
		block(3, i == len(tables)-1, body)
	}
	b.Write(make([]byte, frames))
	return b.Bytes()
}

func mapFLACBytes(t *testing.T, file []byte) (*seekMap, error) {
	t.Helper()
	g := &ranger{ctx: context.Background(), length: int64(len(file)), head: file}
	m := &seekMap{length: int64(len(file))}
	return m, m.mapFLAC(g, 0)
}

// A FLAC's seek points are taken from its first table only, and only those that are places in it: a
// point past its samples or past the file is dropped.
func TestAFLACsSeekPointsAreOnlyItsOwn(t *testing.T) {
	file := flacFile(48000, 10*48000, 4000,
		[]flacPoint{{0, 0}, {48000, 1000}, {20 * 48000, 2000}, {96000, 5000}, {144000, 1 << 63}, {^uint64(0), 0}},
		[]flacPoint{{24000, 500}})
	m, err := mapFLACBytes(t, file)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.points) != 2 || m.points[0] != (flacPoint{0, 0}) || m.points[1] != (flacPoint{48000, 1000}) {
		t.Errorf("points %v", m.points)
	}
	if off, at := m.place(5 * time.Second); off != m.firstFrame+1000 || at != time.Second {
		t.Errorf("5s at byte %d (first frame %d), from %v", off, m.firstFrame, at)
	}
	if m.dur != 10*time.Second {
		t.Errorf("length %v", m.dur)
	}

	// However many points a table says, no more than flacMostPoints are taken.
	many := make([]flacPoint, flacMostPoints+1000)
	for i := range many {
		many[i] = flacPoint{uint64(i + 1), 0}
	}
	if m, err = mapFLACBytes(t, flacFile(48000, 1<<30, 16, many)); err != nil {
		t.Fatal(err)
	}
	if len(m.points) != flacMostPoints {
		t.Errorf("%d points taken", len(m.points))
	}
}

// Samples and time are turned into each other without the product overflowing, for a long file.
func TestFLACTimesDoNotOverflow(t *testing.T) {
	if d := samplesTime(1<<35, 48000); d != 715827*time.Second+time.Duration(1<<35%48000)*time.Second/48000 {
		t.Errorf("2^35 samples at 48 kHz: %v", d)
	}
	if d := samplesTime(1<<36-1, 1); d != math.MaxInt64 {
		t.Errorf("past the longest time: %v", d)
	}
	if n := timeSamples(1000*time.Hour, 655350); n != 1000*3600*655350 {
		t.Errorf("1000 hours at 655,350 Hz: %d", n)
	}
	if n := timeSamples(1500*time.Millisecond, 48000); n != 72000 {
		t.Errorf("1.5s at 48 kHz: %d", n)
	}
}

// A seek to the very end of a WAV fetches its last frame, where a byte past the file was refused.
func TestASeekToTheEndOfAWAVIsItsLastFrame(t *testing.T) {
	stubs(t)
	const frames = 2 * speaker.Rate
	body := wav(frames)
	u := serve(t, "a.wav", body) + "/a.wav"
	m, err := mapSong(context.Background(), fetchClient, u, int64(len(body)), 0)
	if err != nil {
		t.Fatal(err)
	}
	off, _ := m.place(m.dur)
	if off != int64(len(body))-4 {
		t.Errorf("the end at byte %d, want %d", off, len(body)-4)
	}
	pcm, b, _, err := m.open(context.Background(), fetchClient, m.dur)
	if err != nil {
		t.Fatalf("a seek to the end: %v", err)
	}
	defer b.Close()
	if n := frameOf(t, pcm); n != frames-1 {
		t.Errorf("frame %d at the end, want %d", n, frames-1)
	}
}
