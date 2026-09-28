package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

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

// overFor is a player whose sounds over the music are heard by a speaker without hardware, and the
// driver that arbitrates them: what a claim does to other claims is the speaker's business and is tested
// there, and what is tested here is which request a sound belongs to and what becomes of it.
func overFor(t *testing.T) (*Player, *speaker.Driver) {
	t.Helper()
	d := speaker.NewDriver(speaker.New())
	prev := overClaims
	overClaims = d.ClaimOver
	t.Cleanup(func() { overClaims = prev })
	return &Player{stream: &Stream{}, mp: &esphome.MediaPlayer{}}, d
}

// overStream serves a live stream: a second of WAV and then nothing until the reader goes away, which is
// what a camera's converted stream is — read for as long as somebody is watching.
func overStream(t *testing.T) string {
	t.Helper()
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(testWAV(speaker.Rate))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case <-done:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() {
		close(done) // the handler returns, so the server has nothing left to wait for
		srv.Close()
	})
	return srv.URL
}

// neverMuted is what a reading is given when the test is not about muting: a sound that is heard.
var neverMuted = func() bool { return false }

// waitUntil waits for what happens off the reading's own goroutine: a sound starts, ends and is taken from
// in the claim, so the state it leaves behind arrives a moment later.
func waitUntil(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("%s never happened", what)
}

// Every ask has a token of its own, and a request that nothing has answered says so: the control on the
// screen is drawn from that, so a sound still on its way is one there is something to silence.
func TestAnAskHasATokenOfItsOwnAndWaits(t *testing.T) {
	p := &Player{}
	first := p.OverNext()
	if first == 0 {
		t.Fatal("the first ask had no token of its own")
	}
	if second := p.OverNext(); second == first {
		t.Fatal("two asks were given the same token")
	}
	if got := p.OverState(first); got != OverComing {
		t.Fatalf("an ask nothing has answered is %v, want coming", got)
	}
	if got := p.OverState(OverToken(9999)); got != OverGone {
		t.Fatalf("a token nothing was ever asked with is %v, want gone", got)
	}
}

// An ask that nothing answers stops being one. A url arriving after that is not the one it was waiting
// for, and playing it over the music would be a camera heard long after its view closed.
func TestAnAskNothingAnswersStopsBeingOne(t *testing.T) {
	p := &Player{}
	token := p.OverNext()

	// As if the wait had passed: twenty seconds of somebody's afternoon is what expiry is, and this is
	// the same thing without the wait.
	p.overMu.Lock()
	p.overAsks[0].until = time.Now().Add(-time.Second)
	p.overMu.Unlock()

	if p.overURL("http://ha/late.wav") {
		t.Fatal("a url arriving after the ask expired was taken for the sound that was asked for")
	}
	if got := p.OverState(token); got != OverGone {
		t.Fatalf("an expired ask is %v, want gone", got)
	}
}

// Giving a request up is not the same as forgetting it: a stream already on its way has to be dropped
// rather than played as a track, which would replace the room's music with a camera's sound and leave
// nothing able to stop it. This is the tap that silences a view while its stream is still starting.
func TestARequestGivenUpOnDropsTheURLItWasFor(t *testing.T) {
	p, _ := overFor(t)
	token := p.OverNext()
	p.ForgetOverNext(token)

	if !p.overURL(overStream(t)) {
		t.Fatal("a url for a request that was given up on was played as a track")
	}

	p.overMu.Lock()
	playing, asks := p.over, len(p.overAsks)
	p.overMu.Unlock()
	if playing != nil {
		t.Fatal("a sound whose request was given up on played anyway")
	}
	if asks != 0 {
		t.Fatalf("%d asks left after one was answered", asks)
	}
	if got := p.OverState(token); got != OverGone {
		t.Fatalf("a request that was given up on is %v, want gone", got)
	}
}

// Asks are answered in the order they were made, which is what tells two of them apart: a second camera
// opened while the first camera's stream is still starting would otherwise play the first camera's sound
// under the second camera's view.
func TestAsksAreAnsweredInTheOrderTheyWereMade(t *testing.T) {
	p, _ := overFor(t)
	first, second := p.OverNext(), p.OverNext()
	url := overStream(t)

	if !p.overURL(url) {
		t.Fatal("the first url was played as a track")
	}
	waitUntil(t, "the first sound playing", func() bool { return p.OverState(first) == OverPlaying })
	if got := p.OverState(second); got != OverComing {
		t.Fatalf("the second ask is %v after the first was answered, want coming", got)
	}

	if !p.overURL(url) {
		t.Fatal("the second url was played as a track")
	}
	waitUntil(t, "the second sound playing", func() bool { return p.OverState(second) == OverPlaying })
	if got := p.OverState(first); got != OverGone {
		t.Fatalf("the first sound is %v once the second replaced it, want gone", got)
	}
}

// A sound over the music is not a track, and is not announced as one: the station that was playing keeps
// its name, and a stream that gets dropped is still the thing to resume. What the screen and the
// last-station memory hear about is the room's music, not a camera.
func TestASoundOverTheMusicIsNotATrack(t *testing.T) {
	p, _ := overFor(t)

	var played []string
	p.OnPlay.Listen(func(url string) { played = append(played, url) })

	token := p.OverNext()
	url := overStream(t)
	if !p.overURL(url) {
		t.Fatal("the url the ask was for was played as a track")
	}
	waitUntil(t, "the sound playing", func() bool { return p.OverState(token) == OverPlaying })

	if len(played) != 0 {
		t.Fatalf("a sound over the music was announced as a track: %v", played)
	}
}

// Muting is not stopping. A sound silenced from the screen keeps its stream: what arrives is read and
// thrown away, so bringing it back is immediate — the same request, the same stream — rather than another
// trip to Home Assistant. Muting one whose url has not arrived yet is the same thing: what answers it is
// still its own url, and it is silent when it starts.
func TestMutingASoundOverTheMusicKeepsItsStream(t *testing.T) {
	p, _ := overFor(t)
	token := p.OverNext()

	// Muted while it was still on its way.
	p.MuteOver(token, true)
	if got := p.OverState(token); got != OverMuted {
		t.Fatalf("a sound muted on its way is %v, want muted", got)
	}
	if p.OverState(token).Live() {
		t.Fatal("a muted sound was reported as one to silence rather than to bring back")
	}

	// The url that answers is still this request's, and the sound is there but silent.
	if !p.overURL(overStream(t)) {
		t.Fatal("the url for a muted request was played as a track instead")
	}
	waitUntil(t, "the muted sound connected", func() bool { return p.OverState(token) == OverMuted })

	// Bringing it back is the same request still playing, which is the whole difference from stopping it
	// and asking again.
	p.MuteOver(token, false)
	waitUntil(t, "the sound heard again", func() bool { return p.OverState(token) == OverPlaying })

	// And silencing a sound that is already playing leaves the stream up too.
	p.MuteOver(token, true)
	if got := p.OverState(token); got != OverMuted {
		t.Fatalf("a muted sound is %v, want muted", got)
	}
	p.MuteOver(token, false)
	waitUntil(t, "the sound heard once more", func() bool { return p.OverState(token) == OverPlaying })
}

// Stopping a sound over the music is what lets the music back up, and it says so at once: whoever drew
// the control that was tapped redraws it against the sound already being over rather than a second of
// queued audio later. What it queued is drained with it (overlay.go), which is what makes that second
// silent — queued audio is only audible on a device, so it is the hardware that shows that half.
func TestStoppingASoundOverTheMusicEndsItAtOnce(t *testing.T) {
	p, d := overFor(t)
	token := p.OverNext()
	if !p.overURL(overStream(t)) {
		t.Fatal("the url the ask was for was played as a track")
	}
	waitUntil(t, "the sound playing", func() bool { return p.OverState(token) == OverPlaying })

	p.StopOver(token)

	if got := p.OverState(token); got != OverGone {
		t.Fatalf("a stopped sound is %v, want gone", got)
	}
	waitUntil(t, "the speaker free again", func() bool { return !d.Busy() })
}

// Being taken from is not the same as being silenced. A reply or an announcement claims the speaker, and
// what was playing over the music goes with it — but the view that asked for it still wants a sound, so
// the state says it can be asked for again. Silencing it from the screen says the opposite.
func TestASoundTakenByAnAnnouncementIsOneToAskForAgain(t *testing.T) {
	p, d := overFor(t)
	token := p.OverNext()
	if !p.overURL(overStream(t)) {
		t.Fatal("the url the ask was for was played as a track")
	}
	// Playing, and holding the speaker: the state says playing from the moment the url is answered, while
	// the claim itself takes the speaker a moment later — a claim over the music waits its turn rather than
	// displacing what is being said, and only what holds it can be taken from.
	waitUntil(t, "the sound holding the speaker", func() bool { return d.Busy() })

	d.ClaimSpeech("announce", func(context.Context, *speaker.Player) error { return nil })

	waitUntil(t, "the sound taken", func() bool { return p.OverState(token) == OverTaken })
	if p.OverState(token).Live() {
		t.Fatal("a sound that was taken was reported as one to silence rather than to ask for")
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

	if err := readOver(context.Background(), srv.URL, speaker.New(), neverMuted); err != nil {
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
	if err := readOver(stop, srv.URL, speaker.New(), neverMuted); err != nil {
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
		err := readOver(context.Background(), srv.URL, speaker.New(), neverMuted)
		srv.Close()
		if err == nil {
			t.Errorf("%s: readOver played it anyway", c.name)
		}
	}
}

// Silencing before the stream has even started is not a failure either: the control can be tapped while
// the reading is still in the handshake or the header, and that is where it landed on hardware — a
// warning in the log every time, because the reader was only checking between reads.
func TestReadOverGivesUpBeforeTheHeaderArrives(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Nothing at all until the test says so, so the cancellation lands inside the header read.
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = w.Write(testWAV(64))
	}))
	defer srv.Close()
	defer close(release)

	stop, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	if err := readOver(stop, srv.URL, speaker.New(), neverMuted); err != nil {
		t.Fatalf("silencing while the header was being read was reported as a failure: %v", err)
	}
}

// A request that was given up on keeps catching its url until the call that made it has come back: a slow
// call is answered fifteen seconds later, and a url arriving then has to be dropped rather than played as a
// track over the room's music. Once the call is back the catch is short — what was still to come can only
// be just behind it — and then a url is likelier to be somebody's music than the sound nobody wanted.
func TestAGivenUpRequestCatchesItsURLUntilItsCallReturns(t *testing.T) {
	p, _ := overFor(t)

	// The call is still out, and this is a slow one: the url is still its own to drop.
	slow := p.OverNext()
	p.ForgetOverNext(slow)
	if !p.overURL(overStream(t)) {
		t.Fatal("a url for a call still out was played as a track")
	}

	// The call comes back, and the request is forgotten a grace later rather than at once.
	settled := p.OverNext()
	p.ForgetOverNext(settled)
	p.overMu.Lock()
	long := time.Until(p.overAsks[0].until)
	p.overMu.Unlock()
	if long < 10*time.Second {
		t.Fatalf("a given-up request waits %v for its url while its call is still out, want the call's own time", long)
	}

	p.OverSettled(settled)
	p.overMu.Lock()
	short := time.Until(p.overAsks[0].until)
	p.overMu.Unlock()
	if short > overDropFor+time.Second {
		t.Errorf("a given-up request whose call returned waits %v, want about %v", short, overDropFor)
	}
	if !p.overURL(overStream(t)) {
		t.Fatal("a url arriving within the grace was played as a track")
	}

	// A request nobody gave up on is left alone by its call returning: it is still wanted.
	wanted := p.OverNext()
	p.OverSettled(wanted)
	p.overMu.Lock()
	left := time.Until(p.overAsks[0].until)
	p.overMu.Unlock()
	if left < 10*time.Second {
		t.Errorf("a request still wanted was cut short to %v by its call returning", left)
	}
}
