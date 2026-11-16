package media

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// fakeOut is a speaker that keeps what it is given. queued is what Queued reports, so a test decides
// whether the queue has run dry.
type fakeOut struct {
	mu     sync.Mutex
	plays  [][]int16
	queued int
	first  chan struct{}
}

func newFakeOut(queued int) *fakeOut { return &fakeOut{queued: queued, first: make(chan struct{})} }

func (f *fakeOut) PlayVoice(mono []int16) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.plays) == 0 {
		close(f.first)
	}
	f.plays = append(f.plays, append([]int16(nil), mono...))
}

func (f *fakeOut) Queued() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.queued
}

func (f *fakeOut) setQueued(n int) {
	f.mu.Lock()
	f.queued = n
	f.mu.Unlock()
}

func (f *fakeOut) all() (out []int16, plays int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.plays {
		out = append(out, p...)
	}
	return out, len(f.plays)
}

// speech is ms of a 300 Hz tone at amplitude, as the bytes a WAVE body carries at the voice rate.
func speech(ms int, amplitude float64) []byte {
	n := ms * speaker.VoiceRate / 1000
	out := make([]byte, 2*n)
	for i := range n {
		v := amplitude * math.Sin(2*math.Pi*300*float64(i)/speaker.VoiceRate)
		binary.LittleEndian.PutUint16(out[2*i:], uint16(int16(v)))
	}
	return out
}

func tailSamples() int { return speaker.VoiceRate * Tail / 1000 }

// An announcement whose engine makes it slower than it plays starts playing once the cushion has
// filled, long before the body ends, and all of it is heard, at the level it arrived at.
func TestAnAnnouncementPlaysBeforeItHasAllArrived(t *testing.T) {
	out := newFakeOut(1 << 20)
	ended := make(chan struct{})
	body := speech(2000, 8000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(ended)
		_, _ = w.Write(wavHeader(1, speaker.VoiceRate))
		// The first second, 100 ms at a time with a pause between: an engine a little faster than speech.
		half := len(body) / 2
		for at := 0; at < half; at += 3200 {
			_, _ = w.Write(body[at:min(at+3200, half)])
			w.(http.Flusher).Flush()
			time.Sleep(20 * time.Millisecond)
		}
		// Then a long pause before the next sentence: what played must not have waited for it.
		select {
		case <-out.first:
		case <-time.After(5 * time.Second):
			t.Error("nothing played before the rest of the announcement arrived")
		}
		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write(body[half:])
	}))
	defer srv.Close()

	if err := PlaySpeech(context.Background(), srv.URL, &SpeechStream{Buffer: AnnounceBuffer}, out); err != nil {
		t.Fatal(err)
	}
	<-ended
	got, _ := out.all()
	if want := len(body)/2 + tailSamples(); len(got) != want {
		t.Fatalf("played %d samples, want %d (the speech and the tail)", len(got), want)
	}
	for i := range len(body) / 2 {
		if v := int16(binary.LittleEndian.Uint16(body[2*i:])); got[i] != v {
			t.Fatalf("sample %d = %d, want %d as it arrived", i, got[i], v)
		}
	}
}

// Nothing plays until the cushion has filled, and once the queue has run dry only a little is held
// before playing again, rather than the whole cushion.
func TestTheCushionFillsThenRefillsAfterADip(t *testing.T) {
	out := newFakeOut(0)
	s := &SpeechStream{Buffer: 500 * time.Millisecond}
	if s.Take(speech(400, 1000), out) {
		t.Fatal("played 400 ms with a 500 ms cushion")
	}
	if !s.Take(speech(200, 1000), out) {
		t.Fatal("did not play once the cushion had filled")
	}
	// The queue has drained: a dip. 50 ms is not enough to play again; 100 ms is.
	if s.Take(speech(50, 1000), out) {
		t.Fatal("played 50 ms after a dip")
	}
	if !s.Take(speech(50, 1000), out) {
		t.Fatal("held the whole cushion again after a dip")
	}
	// Still playing: what arrives goes straight through.
	out.setQueued(1000)
	if !s.Take(speech(10, 1000), out) {
		t.Fatal("held audio while the queue was still playing")
	}
	if got, plays := out.all(); len(got) != (400+200+50+50+10)*speaker.VoiceRate/1000 || plays != 3 {
		t.Fatalf("played %d samples in %d pieces", len(got), plays)
	}
}

// A body that stops arriving part way ends the announcement with what came, rather than holding the
// speaker until the fetch times out; one that has sent only its header is still waiting for its first
// sentence, which is not a stall.
func TestAStalledAnnouncementEnds(t *testing.T) {
	was := speechStall
	speechStall = 200 * time.Millisecond
	defer func() { speechStall = was }()

	for _, tc := range []struct {
		name  string
		audio []byte
	}{
		{"part way", speech(300, 4000)},
		{"before any audio", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write(wavHeader(1, speaker.VoiceRate))
				_, _ = w.Write(tc.audio)
				w.(http.Flusher).Flush()
				select {
				case <-r.Context().Done():
				case <-time.After(10 * time.Second):
				}
			}))
			defer srv.Close()

			out := newFakeOut(0)
			ctx := context.Background()
			if tc.audio == nil {
				// Only the header, then a wait longer than speechStall: an engine still making its first
				// sentence. That is not a stall; the wait is bounded by voiceTimeout, cut short here.
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 4*speechStall)
				defer cancel()
			}
			start := time.Now()
			err := PlaySpeech(ctx, srv.URL, &SpeechStream{Buffer: AnnounceBuffer}, out)
			if took := time.Since(start); took > 3*time.Second {
				t.Fatalf("took %v to give up on a stalled body", took)
			}
			got, _ := out.all()
			if tc.audio == nil {
				if errors.Is(err, errStalled) {
					t.Fatalf("a header and a wait for the first sentence was taken for a stall")
				}
				if took := time.Since(start); took < 3*speechStall {
					t.Fatalf("gave up after %v, before the wait it was given", took)
				}
				if len(got) != 0 {
					t.Fatalf("played %d samples of nothing", len(got))
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v, want what came played and no failure", err)
			}
			if want := len(tc.audio)/2 + tailSamples(); len(got) != want {
				t.Fatalf("played %d samples, want %d: what came, shorter than the cushion, and the tail", len(got), want)
			}
		})
	}
}

// An MP3 is not played as it arrives: it is read whole and decoded, as it always was.
func TestAnMP3AnnouncementIsStillReadWhole(t *testing.T) {
	mp3 := sampleMP3(t)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write(mp3[:len(mp3)/2])
		w.(http.Flusher).Flush()
		<-release
		_, _ = w.Write(mp3[len(mp3)/2:])
	}))
	defer srv.Close()

	out := newFakeOut(0)
	done := make(chan error, 1)
	go func() { done <- PlaySpeech(context.Background(), srv.URL, &SpeechStream{Buffer: AnnounceBuffer}, out) }()

	select {
	case <-out.first:
		t.Fatal("half an MP3 played")
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	whole, err := decode(mp3, "audio/mpeg", "test")
	if err != nil {
		t.Fatal(err)
	}
	got, plays := out.all()
	if len(got) != len(whole)+tailSamples() || plays != 2 {
		t.Fatalf("played %d samples in %d pieces, want %d in two (the clip, then the tail)", len(got), plays, len(whole)+tailSamples())
	}
}

// Silencing an announcement part way abandons the download: nothing more is read or played, and the
// error says it was canceled, so a second url is not started after it.
func TestACanceledAnnouncementStopsReading(t *testing.T) {
	gone := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(gone)
		_, _ = w.Write(wavHeader(1, speaker.VoiceRate))
		for range 500 {
			if _, err := w.Write(speech(100, 4000)); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(20 * time.Millisecond):
			}
		}
	}))
	defer srv.Close()

	out := newFakeOut(1 << 20)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- PlaySpeech(ctx, srv.URL, &SpeechStream{Buffer: AnnounceBuffer}, out) }()

	<-out.first
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("still playing after being canceled")
	}
	_, plays := out.all()
	select {
	case <-gone:
	case <-time.After(3 * time.Second):
		t.Fatal("the server is still being read from")
	}
	if _, after := out.all(); after != plays {
		t.Fatalf("%d more pieces played after the cancel", after-plays)
	}
	if got, _ := out.all(); len(got) >= 50*speaker.VoiceRate {
		t.Fatalf("played %d samples: the whole body", len(got))
	}
}

// What goes into a call is each piece as the room hears it, with the gain, and nothing else.
func TestAStreamedAnnouncementGoesIntoTheCallPieceByPiece(t *testing.T) {
	var said [][]int16
	out := newFakeOut(1 << 20)
	s := &SpeechStream{Buffer: 100 * time.Millisecond, Gain: true, Say: func(p []int16) { said = append(said, p) }}
	for range 10 {
		s.Take(speech(50, 1000), out)
	}
	s.Finish(out)

	got, plays := out.all()
	if len(said) != plays || plays < 5 {
		t.Fatalf("said %d pieces for %d played; want one each, several", len(said), plays)
	}
	var into []int16
	for _, p := range said {
		into = append(into, p...)
	}
	if len(into) != len(got) {
		t.Fatalf("said %d samples, played %d", len(into), len(got))
	}
	for i := range into {
		if into[i] != got[i] {
			t.Fatalf("sample %d into the call %d, to the room %d", i, into[i], got[i])
		}
	}
}

// Quiet speech is lifted toward the speech level as it streams, and a loud syllable among it is
// compressed rather than clipped or wrapped around.
func TestTheRunningGainLiftsSpeechWithoutClipping(t *testing.T) {
	out := newFakeOut(1 << 20)
	s := &SpeechStream{Buffer: AnnounceBuffer, Gain: true}
	quiet := speech(1500, 1500)
	loud := speech(100, 30000)
	var in []byte
	for _, part := range [][]byte{quiet, loud, quiet} {
		for at := 0; at < len(part); at += 1024 {
			chunk := part[at:min(at+1024, len(part))]
			in = append(in, chunk...)
			s.Take(chunk, out)
		}
	}
	s.Finish(out)

	got, _ := out.all()
	if len(got) != len(in)/2 {
		t.Fatalf("played %d samples of %d", len(got), len(in)/2)
	}
	for i, v := range got {
		was := int16(binary.LittleEndian.Uint16(in[2*i:]))
		if (was > 0 && v < 0) || (was < 0 && v > 0) {
			t.Fatalf("sample %d wrapped: %d became %d", i, was, v)
		}
		if math.Abs(float64(was)) < 20000 && math.Abs(float64(v)) < math.Abs(float64(was)) {
			t.Fatalf("sample %d made quieter: %d became %d", i, was, v)
		}
		if v == math.MaxInt16 || v == math.MinInt16 {
			t.Fatalf("sample %d hit full scale: the limiter did not hold it", i)
		}
	}
	// The quiet tone sits near -30 dBFS; the gain brings it most of the way to the target.
	first := got[:len(quiet)/2]
	if rms := math.Sqrt(SumSquares(first) / float64(len(first))); rms < speechTargetRMS/2 {
		t.Fatalf("quiet speech at %.3f RMS, want it lifted toward %.3f", rms, speechTargetRMS)
	}
}
