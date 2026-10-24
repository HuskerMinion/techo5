package phone

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/emiago/diago"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// fakeProgress stands in for the tone and the network's audio, and records what played, and when it
// stopped.
type fakeProgress struct {
	mu     sync.Mutex
	played []string
	ends   []string
}

func (f *fakeProgress) play(name string) func(context.Context) {
	return func(ctx context.Context) {
		f.mu.Lock()
		f.played = append(f.played, name)
		f.mu.Unlock()
		<-ctx.Done()
		f.mu.Lock()
		f.ends = append(f.ends, name)
		f.mu.Unlock()
	}
}

func (f *fakeProgress) seen() (played, ends []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.played...), append([]string(nil), f.ends...)
}

func useFakeProgress(t *testing.T) *fakeProgress {
	f := &fakeProgress{}
	wasTone, wasEarly := playRingback, playEarly
	playRingback = f.play("tone")
	playEarly = func(ctx context.Context, _ *diago.DialogMedia) { f.play("early")(ctx) }
	t.Cleanup(func() { playRingback, playEarly = wasTone, wasEarly })
	return f
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("never: %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// The far end ringing plays the tone, once however often it is said; the network's audio takes over
// from the tone, and a ringing said after it changes nothing; stop ends what plays.
func TestProgressPlaysTheToneThenTheNetwork(t *testing.T) {
	f := useFakeProgress(t)
	g := newProgress(context.Background())

	g.ringing()
	g.ringing()
	eventually(t, "the tone", func() bool { p, _ := f.seen(); return len(p) == 1 })
	if p, _ := f.seen(); p[0] != "tone" {
		t.Fatalf("played %v, want the tone", p)
	}

	g.earlyMedia(&diago.DialogMedia{})
	eventually(t, "the network's audio", func() bool { p, _ := f.seen(); return len(p) == 2 })
	if p, e := f.seen(); p[1] != "early" || len(e) != 1 || e[0] != "tone" {
		t.Fatalf("played %v, ended %v: want the tone ended and the network playing", p, e)
	}

	g.ringing()
	g.earlyMedia(&diago.DialogMedia{})
	time.Sleep(20 * time.Millisecond)
	if p, _ := f.seen(); len(p) != 2 {
		t.Fatalf("played %v: a ringing or more audio after the network's started something new", p)
	}

	g.stop()
	if _, e := f.seen(); len(e) != 2 || e[1] != "early" {
		t.Fatalf("ended %v: stop did not end the network's audio before it returned", e)
	}
	g.stop() // twice is fine
}

// A call answered with no ringing said, as some networks do, plays nothing.
func TestProgressSilentUntilTheNetworkSaysSomething(t *testing.T) {
	f := useFakeProgress(t)
	g := newProgress(context.Background())
	g.stop()
	if p, _ := f.seen(); len(p) != 0 {
		t.Errorf("played %v with nothing said", p)
	}
}

// The ringing tone is a second of 425 Hz and four seconds of quiet, its edges soft.
func TestRingbackTone(t *testing.T) {
	tone := ringbackTone()
	rate := speaker.VoiceRate
	if len(tone) != 5*rate {
		t.Fatalf("a cycle is %d samples, want %d (five seconds)", len(tone), 5*rate)
	}
	crossings := 0
	for i := 1; i < rate; i++ {
		if (tone[i-1] < 0) != (tone[i] < 0) {
			crossings++
		}
	}
	if crossings < 2*425-10 || crossings > 2*425+10 {
		t.Errorf("%d zero crossings in the first second, want about %d (425 Hz)", crossings, 2*425)
	}
	for i := rate; i < len(tone); i++ {
		if tone[i] != 0 {
			t.Fatalf("sound at %d, in the four seconds that should be quiet", i)
		}
	}
	if abs := tone[0]; abs > 100 || abs < -100 {
		t.Errorf("the tone starts at %d: it should rise from nothing, not click", abs)
	}
}

// countingWriter counts what is written to it, and how much each time.
type countingWriter struct {
	mu     sync.Mutex
	writes []int
}

func (w *countingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.writes = append(w.writes, len(p))
	return len(p), nil
}

// Silence goes out in 20 ms frames, which opens the way back through the firewall for the network's
// audio, and stops with its context.
func TestSendSilence(t *testing.T) {
	w := &countingWriter{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { sendSilence(ctx, w, 8000); close(done) }()
	time.Sleep(110 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("sendSilence went on after its context ended")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.writes) < 3 {
		t.Fatalf("%d frames in 110 ms, want about five", len(w.writes))
	}
	for _, n := range w.writes {
		if n != 320 {
			t.Fatalf("a frame of %d bytes, want 320 (20 ms of 16-bit samples at 8 kHz)", n)
		}
	}
}
