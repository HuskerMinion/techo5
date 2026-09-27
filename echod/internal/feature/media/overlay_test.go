package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// testWAV is what Home Assistant serves for a stream it has converted: a WAVE header whose sizes were
// written before the length was known, and n frames of silence after it.
func testWAV(n int) []byte {
	var b bytes.Buffer
	var sz [4]byte
	b.WriteString("RIFF")
	b.Write(sz[:]) // the length, which a live stream does not know
	b.WriteString("WAVE")

	b.WriteString("fmt ")
	binary.LittleEndian.PutUint32(sz[:], 16)
	b.Write(sz[:])
	var fmtChunk [16]byte
	binary.LittleEndian.PutUint16(fmtChunk[0:], 1) // PCM
	binary.LittleEndian.PutUint16(fmtChunk[2:], uint16(speaker.Channels))
	binary.LittleEndian.PutUint32(fmtChunk[4:], uint32(speaker.Rate))
	binary.LittleEndian.PutUint32(fmtChunk[8:], uint32(speaker.Rate*speaker.Channels*2))
	binary.LittleEndian.PutUint16(fmtChunk[12:], uint16(speaker.Channels*2))
	binary.LittleEndian.PutUint16(fmtChunk[14:], 16)
	b.Write(fmtChunk[:])

	b.WriteString("data")
	binary.LittleEndian.PutUint32(sz[:], uint32(n*speaker.Channels*2))
	b.Write(sz[:])
	b.Write(make([]byte, n*speaker.Channels*2))
	return b.Bytes()
}

// The ask is for one URL and no more. Left standing it would take the next track of somebody's music
// for a camera's sound, and never standing it is a sound that replaces the music after all.
func TestTheAskToPlayOverTheMusicIsForOneURL(t *testing.T) {
	p := &Player{}
	if p.takeOverNext() {
		t.Fatal("a URL was taken for a sound over the music without anybody asking")
	}

	p.OverNext()
	if !p.takeOverNext() {
		t.Fatal("the URL the sound was asked for was played as a track instead")
	}
	if p.takeOverNext() {
		t.Fatal("the ask was still standing for the URL after it")
	}

	p.OverNext()
	p.ForgetOverNext()
	if p.takeOverNext() {
		t.Fatal("the ask was left standing after the request for it failed")
	}
}

// Stopping a sound over the music is what lets the music back up, and a sound that is not playing is
// nothing to stop — the watcher calls this on the way out of every camera view, whether or not there
// was ever a sound.
func TestStoppingASoundOverTheMusicLetsTheMusicBackUp(t *testing.T) {
	p := &Player{}
	if p.Overing() {
		t.Fatal("a player with nothing over it said something was playing over the music")
	}
	p.StopOver() // nothing to stop: not a panic, and not a crash

	stopped := make(chan struct{})
	p.overMu.Lock()
	p.overStop = func() { close(stopped) }
	p.overMu.Unlock()

	if !p.Overing() {
		t.Fatal("a sound playing over the music was not reported")
	}
	p.StopOver()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("stopping the sound did not stop it")
	}

	// What clears the report is the sound's own end, which the claim says; a stopped sound is not a
	// sound.
	p.overMu.Lock()
	p.overStop = nil
	p.overMu.Unlock()
	if p.Overing() {
		t.Fatal("a stopped sound was still reported as playing over the music")
	}
}

// A stream that ends is not a failure: a camera's stream ends when the camera stops sending, and that
// is the end of the sound rather than something to complain about.
func TestReadOverEndsWithTheStream(t *testing.T) {
	wav := testWAV(64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(wav)
	}))
	defer srv.Close()

	if err := readOver(context.Background(), srv.URL, speaker.New()); err != nil {
		t.Fatalf("a stream that ended was reported as a failure: %v", err)
	}
}

// Silencing is not a failure either. It is the reader's own context going away, and a warning in the
// log every time somebody taps the control would be noise about the feature working.
func TestReadOverGivesUpWhenSilenced(t *testing.T) {
	wav := testWAV(speaker.Rate * 4) // four seconds of it, dribbled out slowly
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
		flusher, _ := w.(http.Flusher)
		for i := 0; i < len(wav); i += 4096 {
			end := min(i+4096, len(wav))
			if _, err := w.Write(wav[i:end]); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(2 * time.Millisecond)
		}
	}))
	defer srv.Close()

	stop, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	if err := readOver(stop, srv.URL, speaker.New()); err != nil {
		t.Fatalf("silencing was reported as a failure: %v", err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("the reading went on for %v after being silenced", took)
	}
}

// What cannot be played is refused rather than played as noise: a camera Home Assistant would not
// answer for, and a body that is not the WAV this speaker takes.
func TestReadOverRefusesWhatItCannotPlay(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   []byte
	}{
		{"a refusal", http.StatusInternalServerError, nil},
		{"something that is not audio", http.StatusOK, []byte("this is not a stream of samples at all")},
	}
	for _, c := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(c.status)
			_, _ = w.Write(c.body)
		}))
		err := readOver(context.Background(), srv.URL, speaker.New())
		srv.Close()
		if err == nil {
			t.Errorf("%s: readOver played it anyway", c.name)
		}
	}
}
