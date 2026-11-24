package dlna

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mewkiz/flac"
	flacframe "github.com/mewkiz/flac/frame"
	"github.com/mewkiz/flac/meta"

	"github.com/HuskerMinion/techo5/echod/internal/feature/video"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

func TestSeekTargets(t *testing.T) {
	for _, c := range []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"0:01:00", time.Minute, true},
		{"00:01:30.500", 90*time.Second + 500*time.Millisecond, true},
		{"1:02:03", time.Hour + 2*time.Minute + 3*time.Second, true},
		{"12:00:00.25", 12*time.Hour + 250*time.Millisecond, true},
		{" 0:00:10 ", 10 * time.Second, true},
		{"0:00:10.1/4", 10*time.Second + 250*time.Millisecond, true},
		{"3:25", 3*time.Minute + 25*time.Second, true},
		{"0:00:00", 0, true},
		{"0:60:00", 0, false},
		{"0:00:60", 0, false},
		{"-0:00:10", 0, false},
		{"0:00:1x", 0, false},
		{"0:00:10.", 0, false},
		{"0:00:10.3/2", 0, false},
		{"0:00:10.1/0", 0, false},
		{"10", 0, false},
		{"", 0, false},
		{"1:2:3:4", 0, false},
		{"NOT_IMPLEMENTED", 0, false},
	} {
		got, ok := parseSeekTarget(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("%q = %v %v, want %v %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

// stubs puts a speaker and a video player of the test's own in the renderer's place, the speaker
// holding the renderer's song.
func stubs(t *testing.T) {
	t.Helper()
	oldState, oldPlay, oldPause, oldSeek, oldSeekable, oldCurrent, oldClient := speakerState, playOnSpeaker, pauseSpeaker, seekVideo, videoSeekable, currentVideo, fetchClient
	t.Cleanup(func() {
		speakerState, playOnSpeaker, pauseSpeaker, seekVideo, videoSeekable, currentVideo, fetchClient = oldState, oldPlay, oldPause, oldSeek, oldSeekable, oldCurrent, oldClient
	})
	speakerState = func() (bool, bool) { return true, false }
	playOnSpeaker = func(src *source, _ song) { _ = src.Close() }
	pauseSpeaker = func() {}
	currentVideo = func() video.State { return video.State{Phase: video.Idle} }
	// The tests' servers are on loopback, which the renderer's own client refuses.
	fetchClient = &http.Client{}
}

func seekArgs(unit, target string) string {
	return `<InstanceID>0</InstanceID><Unit>` + unit + `</Unit><Target>` + target + `</Target>`
}

// A burst of seeks, as a slider sends, is answered at once with the position read as the last, and
// only the last is carried out once it has settled. The settle is held open and ended by hand, so a
// loaded run cannot see it end part way through the burst.
func TestABurstOfSeeksIsCarriedOutOnce(t *testing.T) {
	stubs(t)
	oldSettle := seekSettle
	seekSettle = time.Hour
	var mu sync.Mutex
	var moved []time.Duration
	seekVideo = func(id uint64, to time.Duration) error {
		mu.Lock()
		defer mu.Unlock()
		if id != 7 {
			t.Errorf("moved video %d", id)
		}
		moved = append(moved, to)
		return nil
	}
	videoSeekable = func(uint64) (bool, bool) { return true, true }
	currentVideo = func() video.State {
		return video.State{ID: 7, Phase: video.Playing, Pos: 10 * time.Second, Dur: 10 * time.Minute, Frames: true}
	}
	f := &Feature{}
	f.r.f = f
	f.r.cur = song{uri: "http://192.0.2.10/film.mp4", video: true, dur: 10 * time.Minute}
	f.r.videoID, f.r.state = 7, stPlaying
	t.Cleanup(func() {
		f.r.mu.Lock()
		f.r.clearSeekLocked()
		f.r.mu.Unlock()
		seekSettle = oldSettle
	})

	if _, body := call(t, f, "AVTransport", "GetCurrentTransportActions", `<InstanceID>0</InstanceID>`); !strings.Contains(body, "<Actions>Pause,Stop,Seek</Actions>") {
		t.Errorf("actions: %s", body)
	}
	for _, at := range []string{"0:01:00", "0:01:10", "0:01:20", "0:01:30", "0:01:45"} {
		if code, body := call(t, f, "AVTransport", "Seek", seekArgs("REL_TIME", at)); code != http.StatusOK {
			t.Fatalf("Seek %s: %d %s", at, code, body)
		}
	}
	if _, body := call(t, f, "AVTransport", "GetPositionInfo", `<InstanceID>0</InstanceID>`); !strings.Contains(body, "<RelTime>0:01:45</RelTime>") {
		t.Errorf("the position before the seek is carried out: %s", body)
	}
	// The burst has settled: its one timer is ended here, and what it would do done.
	f.r.mu.Lock()
	armed := f.r.seekTimer != nil && f.r.seekTimer.Stop()
	f.r.mu.Unlock()
	if !armed {
		t.Fatal("the burst had no settle running")
	}
	f.r.seekNow()
	mu.Lock()
	if len(moved) != 1 || moved[0] != 105*time.Second {
		t.Errorf("moved %v, want once to 1:45", moved)
	}
	mu.Unlock()
	// ABS_TIME is the same with one track; past the end is not a place.
	if code, _ := call(t, f, "AVTransport", "Seek", seekArgs("ABS_TIME", "0:02:00")); code != http.StatusOK {
		t.Errorf("ABS_TIME: %d", code)
	}
	if code, body := call(t, f, "AVTransport", "Seek", seekArgs("REL_TIME", "0:20:00")); code != 500 || !strings.Contains(body, "<errorCode>711</errorCode>") {
		t.Errorf("past the end: %d %s", code, body)
	}
	if code, body := call(t, f, "AVTransport", "Seek", seekArgs("REL_TIME", "soon")); code != 500 || !strings.Contains(body, "<errorCode>711</errorCode>") {
		t.Errorf("not a time: %d %s", code, body)
	}
	if code, body := call(t, f, "AVTransport", "Seek", seekArgs("TRACK_NR", "1")); code != 500 || !strings.Contains(body, "<errorCode>710</errorCode>") {
		t.Errorf("TRACK_NR: %d %s", code, body)
	}
}

// What cannot be moved through says so: a live video, and a song whose server sends no parts of it.
func TestWhatCannotBeMovedThroughSaysSo(t *testing.T) {
	stubs(t)
	videoSeekable = func(uint64) (bool, bool) { return false, true }
	currentVideo = func() video.State { return video.State{ID: 7, Phase: video.Playing, Frames: true} }
	f := &Feature{}
	f.r.f = f
	f.r.cur = song{uri: "http://192.0.2.10/live.ts", video: true}
	f.r.videoID, f.r.state = 7, stPlaying
	if code, body := call(t, f, "AVTransport", "Seek", seekArgs("REL_TIME", "0:01:00")); code != 500 || !strings.Contains(body, "<errorCode>710</errorCode>") {
		t.Errorf("a live video: %d %s", code, body)
	}
	if _, body := call(t, f, "AVTransport", "GetCurrentTransportActions", `<InstanceID>0</InstanceID>`); strings.Contains(body, "Seek") {
		t.Errorf("a live video's actions: %s", body)
	}

	f.r.cur, f.r.videoID = song{uri: "http://192.0.2.10/radio.mp3"}, 0
	if code, body := call(t, f, "AVTransport", "Seek", seekArgs("REL_TIME", "0:01:00")); code != 500 || !strings.Contains(body, "<errorCode>710</errorCode>") {
		t.Errorf("a song not mapped: %d %s", code, body)
	}

	// A server that sends the whole file for a Range request, and one that gives no length.
	whole := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(wav(48000))
	}))
	defer whole.Close()
	if _, err := mapSong(context.Background(), fetchClient, whole.URL+"/a.wav", int64(len(wav(48000))), 0); err == nil {
		t.Error("a server that sends no parts was mapped")
	}
	if _, err := mapSong(context.Background(), fetchClient, whole.URL+"/a.wav", -1, 0); err == nil {
		t.Error("a song of no length was mapped")
	}
	// A FLAC without a seek table.
	plain := serve(t, "a.flac", encodeFLAC(t, 48000, 20, false))
	if _, err := mapSong(context.Background(), fetchClient, plain+"/a.flac", int64(len(encodeFLAC(t, 48000, 20, false))), 0); err == nil {
		t.Error("a FLAC with no seek table was mapped")
	}
}

// serve is an httptest server that sends body at /name with Range requests answered (206).
func serve(t *testing.T, name string, body []byte) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// wav is frames of the speaker's own WAV, each frame saying which it is: its number, low half on the
// left and high half on the right.
func wav(frames int) []byte {
	var b bytes.Buffer
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(36+frames*4+12))
	b.WriteString("WAVE")
	b.WriteString("LIST") // a chunk before the format, as some writers put one
	_ = binary.Write(&b, binary.LittleEndian, uint32(4))
	b.WriteString("INFO")
	b.WriteString("fmt ")
	for _, v := range []any{uint32(16), uint16(1), uint16(2), uint32(speaker.Rate), uint32(speaker.Rate * 4), uint16(4), uint16(16)} {
		_ = binary.Write(&b, binary.LittleEndian, v)
	}
	b.WriteString("data")
	_ = binary.Write(&b, binary.LittleEndian, uint32(frames*4))
	for i := range frames {
		_ = binary.Write(&b, binary.LittleEndian, []uint16{uint16(i), uint16(i >> 16)})
	}
	return b.Bytes()
}

// frameOf is which frame of wav (or encodeFLAC) the first samples of pcm are.
func frameOf(t *testing.T, pcm io.Reader) int {
	t.Helper()
	var s [4]byte
	if _, err := io.ReadFull(pcm, s[:]); err != nil {
		t.Fatal(err)
	}
	return int(binary.LittleEndian.Uint16(s[:2])) | int(binary.LittleEndian.Uint16(s[2:]))<<16
}

// A WAV is moved through exactly: the samples after a seek are the ones at the time asked for.
func TestAWAVIsMovedThroughExactly(t *testing.T) {
	stubs(t)
	body := wav(10 * speaker.Rate)
	u := serve(t, "a.wav", body) + "/a.wav"
	m, err := mapSong(context.Background(), fetchClient, u, int64(len(body)), 0)
	if err != nil {
		t.Fatal(err)
	}
	if m.kind != "wav" || m.dur != 10*time.Second {
		t.Fatalf("map %+v", m)
	}
	pcm, b, at, err := m.open(context.Background(), fetchClient, 3500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if at != 3500*time.Millisecond {
		t.Errorf("at %v", at)
	}
	if n := frameOf(t, pcm); n != 3500*speaker.Rate/1000 {
		t.Errorf("frame %d after the seek, want %d", n, 3500*speaker.Rate/1000)
	}
}

// encodeFLAC is a FLAC of seconds at rate, each sample saying which it is as wav's do (verbatim, so the
// decoder hands back exactly what went in), with a seek table to every frame if table, behind a large
// block so the table is past the first bytes fetched.
func encodeFLAC(t *testing.T, rate uint32, seconds int, table bool) []byte {
	t.Helper()
	const block = 4096
	n := int(rate) * seconds
	frames := (n + block - 1) / block
	enc := func(points []meta.SeekPoint) ([]byte, []uint64) {
		var buf bytes.Buffer
		info := &meta.StreamInfo{BlockSizeMin: block, BlockSizeMax: block, SampleRate: rate, NChannels: 2, BitsPerSample: 16, NSamples: uint64(n)}
		var blocks []*meta.Block
		if table {
			blocks = append(blocks, &meta.Block{Header: meta.Header{Type: meta.TypePadding, Length: 100 << 10}},
				&meta.Block{Header: meta.Header{Type: meta.TypeSeekTable, Length: int64(18 * len(points))}, Body: &meta.SeekTable{Points: points}})
		}
		e, err := flac.NewEncoder(&buf, info, blocks...)
		if err != nil {
			t.Fatal(err)
		}
		first := buf.Len()
		var offs []uint64
		for off := 0; off < n; off += block {
			offs = append(offs, uint64(buf.Len()-first))
			size := min(block, n-off)
			f := &flacframe.Frame{Header: flacframe.Header{HasFixedBlockSize: true, BlockSize: uint16(size), SampleRate: rate,
				Channels: flacframe.ChannelsLR, BitsPerSample: 16}}
			for ch := range 2 {
				s := make([]int32, size)
				for i := range s {
					v := off + i
					if ch == 1 {
						v >>= 16
					}
					s[i] = int32(int16(uint16(v)))
				}
				f.Subframes = append(f.Subframes, &flacframe.Subframe{SubHeader: flacframe.SubHeader{Pred: flacframe.PredVerbatim}, Samples: s, NSamples: size})
			}
			if err := e.WriteFrame(f); err != nil {
				t.Fatal(err)
			}
		}
		if err := e.Close(); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes(), offs
	}
	if !table {
		b, _ := enc(nil)
		return b
	}
	// Once with the table's room, for where the frames are; again with the table filled in, which
	// moves none of them (its offsets count from the first frame).
	_, offs := enc(make([]meta.SeekPoint, frames))
	points := make([]meta.SeekPoint, frames)
	for i := range points {
		points[i] = meta.SeekPoint{SampleNum: uint64(i * block), Offset: offs[i], NSamples: block}
	}
	b, _ := enc(points)
	return b
}

// A FLAC with a seek table is moved through from the point before the time, and what is between is
// dropped: the samples after a seek are the ones at the time asked for.
func TestAFLACWithASeekTableIsMovedThrough(t *testing.T) {
	stubs(t)
	body := encodeFLAC(t, speaker.Rate, 6, true)
	u := serve(t, "a.flac", body) + "/a.flac"
	m, err := mapSong(context.Background(), fetchClient, u, int64(len(body)), 0)
	if err != nil {
		t.Fatal(err)
	}
	if m.kind != "flac" || m.dur != 6*time.Second || len(m.points) == 0 {
		t.Fatalf("map %s %v %d points", m.kind, m.dur, len(m.points))
	}
	pcm, b, at, err := m.open(context.Background(), fetchClient, 4250*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if at != 4250*time.Millisecond {
		t.Errorf("at %v", at)
	}
	if n, want := frameOf(t, pcm), 4250*speaker.Rate/1000; n != want {
		t.Errorf("sample %d after the seek, want %d", n, want)
	}
}

// mp3Frames is a constant bit rate MP3 of n silent frames (MPEG-1 Layer III, 128 kb/s, 44.1 kHz:
// 417 bytes each), after an ID3 tag, with a Xing header whose table of contents is toc in the first
// frame when toc is given.
func mp3Frames(n int, toc []byte) []byte {
	var b bytes.Buffer
	b.WriteString("ID3\x04\x00\x00\x00\x00\x01\x00") // a tag of 128 bytes
	b.Write(make([]byte, 128))
	frame := func() []byte {
		f := make([]byte, 417)
		copy(f, []byte{0xFF, 0xFB, 0x90, 0x00})
		return f
	}
	if toc != nil {
		f := frame()
		copy(f[36:], "Xing")
		binary.BigEndian.PutUint32(f[40:], 7)
		binary.BigEndian.PutUint32(f[44:], uint32(n))
		binary.BigEndian.PutUint32(f[48:], uint32(n*417))
		copy(f[52:], toc)
		b.Write(f)
		n--
	}
	for range n {
		b.Write(frame())
	}
	return b.Bytes()
}

// An MP3 is moved through in proportion to its length, or by the table of contents a Xing header
// carries, and is fetched from a frame's start.
func TestAnMP3IsMovedThroughFromAFrame(t *testing.T) {
	stubs(t)
	const frames = 1000 // 26.1 seconds
	body := mp3Frames(frames, nil)
	u := serve(t, "a.mp3", body) + "/a.mp3"
	m, err := mapSong(context.Background(), fetchClient, u, int64(len(body)), 0)
	if err != nil {
		t.Fatal(err)
	}
	if m.kind != "mp3" || m.audioStart != 138 {
		t.Fatalf("map %s from %d", m.kind, m.audioStart)
	}
	// 417 bytes at 128 kb/s is 26.06 ms a frame: the length from the bit rate.
	if want := time.Duration(frames * 417 * 8 * int64(time.Second) / 128000); m.dur != want {
		t.Errorf("length %v, want %v", m.dur, want)
	}
	if off, _ := m.place(m.dur / 2); off != 138+frames*417/2 {
		t.Errorf("half way at byte %d, want %d", off, 138+frames*417/2)
	}
	pcm, b, at, err := m.open(context.Background(), fetchClient, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if at != 10*time.Second {
		t.Errorf("at %v", at)
	}
	if n, err := io.ReadFull(pcm, make([]byte, 4096)); n != 4096 {
		t.Errorf("decoded %d bytes after the seek: %v", n, err)
	}
	b.Close()

	// With a table of contents: the first half of the time in the first quarter of the bytes.
	toc := make([]byte, 100)
	for i := range toc {
		if i < 50 {
			toc[i] = byte(i * 64 / 50)
		} else {
			toc[i] = byte(64 + (i-50)*192/50)
		}
	}
	body = mp3Frames(frames, toc)
	u = serve(t, "b.mp3", body) + "/b.mp3"
	if m, err = mapSong(context.Background(), fetchClient, u, int64(len(body)), 0); err != nil {
		t.Fatal(err)
	}
	if len(m.toc) != 100 || m.dur != time.Duration(frames*1152*int64(time.Second)/44100) {
		t.Fatalf("toc %d, length %v", len(m.toc), m.dur)
	}
	if off, _ := m.place(m.dur / 2); off != 138+frames*417/4 {
		t.Errorf("half way at byte %d, want %d", off, 138+frames*417/4)
	}
	if pcm, b, _, err = m.open(context.Background(), fetchClient, m.dur/2); err != nil {
		t.Fatal(err)
	}
	if n, err := io.ReadFull(pcm, make([]byte, 4096)); n != 4096 {
		t.Errorf("decoded %d bytes after the seek: %v", n, err)
	}
	b.Close()
}

// The frame found after a byte part way into a frame is the next real one, not a stray 0xFF.
func TestAnMP3SeekFindsTheNextFrame(t *testing.T) {
	b := mp3Frames(10, nil)[138:]
	b[100], b[101] = 0xFF, 0xFB // a false header in the middle of the first frame
	if i, ok := mp3Sync(b[50:]); !ok || i != 417-50 {
		t.Errorf("synced at %d %v, want %d", i, ok, 417-50)
	}
}

// One of go-mp3's own samples, a real MP3, is moved through and decodes from there.
func TestARealMP3IsMovedThrough(t *testing.T) {
	stubs(t)
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/hajimehoshi/go-mp3").Output()
	if err != nil {
		t.Skip("go-mp3's module is not at hand:", err)
	}
	body, err := os.ReadFile(filepath.Join(strings.TrimSpace(string(out)), "example", "mpeg2.mp3"))
	if err != nil {
		t.Skip("go-mp3's sample is not at hand:", err)
	}
	u := serve(t, "c.mp3", body) + "/c.mp3"
	m, err := mapSong(context.Background(), fetchClient, u, int64(len(body)), 0)
	if err != nil {
		t.Fatal(err)
	}
	pcm, b, _, err := m.open(context.Background(), fetchClient, m.dur/2)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if n, err := io.ReadFull(pcm, make([]byte, 48000*4)); n != 48000*4 {
		t.Errorf("decoded %d bytes a second after the seek: %v", n, err)
	}
}

// A seek in a song that plays fetches it again from the place, replaces it on the speaker with the
// samples from there, and reads its position from there; while paused, it waits for Play.
func TestASongIsFetchedAgainFromTheSeek(t *testing.T) {
	stubs(t)
	body := wav(20 * speaker.Rate)
	u := serve(t, "a.wav", body) + "/a.wav"
	played := make(chan int, 4)
	playOnSpeaker = func(src *source, _ song) {
		var s [4]byte
		if _, err := io.ReadFull(src, s[:]); err == nil {
			played <- int(binary.LittleEndian.Uint16(s[:2])) | int(binary.LittleEndian.Uint16(s[2:]))<<16
		}
		_ = src.Close()
	}
	f := &Feature{}
	f.r.f = f
	r := &f.r
	r.cur = song{uri: u, title: "A test tone"}
	if err := r.play(); err != nil {
		t.Fatal(err)
	}
	select {
	case n := <-played:
		if n != 0 {
			t.Fatalf("started at frame %d", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("the song never started: %s", r.sync())
	}
	// The song's map is found out once it has started.
	deadline := time.Now().Add(5 * time.Second)
	for {
		r.mu.Lock()
		mapped := r.smap != nil
		r.mu.Unlock()
		if mapped {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the song was never mapped")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, b := call(t, f, "AVTransport", "GetCurrentTransportActions", `<InstanceID>0</InstanceID>`); !strings.Contains(b, "Seek") {
		t.Errorf("actions once mapped: %s", b)
	}
	if code, b := call(t, f, "AVTransport", "Seek", seekArgs("REL_TIME", "0:00:12.5")); code != http.StatusOK {
		t.Fatalf("Seek: %d %s", code, b)
	}
	select {
	case n := <-played:
		if n != 12*speaker.Rate+speaker.Rate/2 {
			t.Errorf("after the seek the speaker started at frame %d, want %d", n, 12*speaker.Rate+speaker.Rate/2)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the seek was never carried out")
	}
	deadline = time.Now().Add(2 * time.Second)
	for r.sync() != stPlaying && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	r.mu.Lock()
	pos, st := r.positionLocked(), r.state
	r.mu.Unlock()
	if st != stPlaying || pos < 12500*time.Millisecond || pos > 13500*time.Millisecond {
		t.Errorf("after the seek: %s at %v", st, pos)
	}

	// Paused, the seek waits for Play, and the position reads as the new place meanwhile.
	r.mu.Lock()
	r.state, r.pausedAt = stPaused, time.Now()
	r.mu.Unlock()
	speakerState = func() (bool, bool) { return true, true }
	if code, b := call(t, f, "AVTransport", "Seek", seekArgs("REL_TIME", "0:00:03")); code != http.StatusOK {
		t.Fatalf("Seek while paused: %d %s", code, b)
	}
	time.Sleep(seekSettle + 100*time.Millisecond)
	select {
	case n := <-played:
		t.Errorf("fetched again while paused, at frame %d", n)
	default:
	}
	if _, b := call(t, f, "AVTransport", "GetPositionInfo", `<InstanceID>0</InstanceID>`); !strings.Contains(b, "<RelTime>0:00:03</RelTime>") {
		t.Errorf("paused after a seek: %s", b)
	}
	speakerState = func() (bool, bool) { return true, false }
	if err := r.play(); err != nil {
		t.Fatal(err)
	}
	select {
	case n := <-played:
		if n != 3*speaker.Rate {
			t.Errorf("on Play the speaker started at frame %d, want %d", n, 3*speaker.Rate)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Play after a seek while paused fetched nothing")
	}
	r.stop()
}
