package phone

import (
	"context"
	"math"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// The far end counts as talking for farEndHold after its last loud frame, and not before one.
func TestFarEndTalking(t *testing.T) {
	defer farEndAt.Store(0)
	now := time.Now()

	farEndAt.Store(0)
	if farEndTalking(now) {
		t.Error("talking before anything was heard")
	}
	farEndAt.Store(now.UnixNano())
	if !farEndTalking(now.Add(farEndHold - time.Millisecond)) {
		t.Error("not talking inside the hold")
	}
	if farEndTalking(now.Add(farEndHold)) {
		t.Error("still talking after the hold")
	}
}

// A wake word opens a turn with no call, and with Wake word during calls on, over one ringing here and
// during one that is up while the far end is quiet. A call being placed never lets it through.
func TestWakeAllowed(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	p := Get()
	defer p.set(func(s *State) { s.Phase = Idle })
	defer farEndAt.Store(0)

	for _, c := range []struct {
		name          string
		phase         Phase
		callWake, far bool
		want          bool
	}{
		{"no call", Idle, false, false, true},
		{"a call, switched off", Talking, false, false, false},
		{"a call, switched on", Talking, true, false, true},
		{"a call, the far end talking", Talking, true, true, false},
		{"ringing, switched on", Ringing, true, false, true},
		{"ringing, switched off", Ringing, false, false, false},
		{"being placed", Dialing, true, false, false},
	} {
		if err := config.Set().Home().CallWake(c.callWake); err != nil {
			t.Fatal(err)
		}
		farEndAt.Store(0)
		if c.far {
			farEndAt.Store(time.Now().UnixNano())
		}
		p.set(func(s *State) { s.Phase = c.phase })
		if got := p.WakeAllowed(); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// The far end under a reply is as far down as music is, and not down at all when ducking is off.
func TestUnderGain(t *testing.T) {
	if g := underGain(0); g != 1 {
		t.Errorf("no ducking: gain %v", g)
	}
	if g := underGain(-20); math.Abs(g-0.1) > 1e-9 {
		t.Errorf("-20 dB: gain %v, want 0.1", g)
	}
}

// What goes up the call: the room with anything to say mixed over it, or while a turn's reply plays
// here, the reply alone, since the room then is mostly the reply again a moment later.
func TestUplink(t *testing.T) {
	room := []int16{100, 100, 100, 100}

	out, rest := uplink(append([]int16(nil), room...), []int16{10, 20}, false)
	if want := []int16{110, 120, 100, 100}; !slices.Equal(out, want) || len(rest) != 0 {
		t.Errorf("mixed: %v rest %v, want %v", out, rest, want)
	}

	out, rest = uplink(append([]int16(nil), room...), []int16{10, 20, 30, 40, 50}, true)
	if want := []int16{10, 20, 30, 40}; !slices.Equal(out, want) || !slices.Equal(rest, []int16{50}) {
		t.Errorf("alone: %v rest %v, want %v and [50]", out, rest, want)
	}

	out, _ = uplink(append([]int16(nil), room...), nil, true)
	if want := []int16{0, 0, 0, 0}; !slices.Equal(out, want) {
		t.Errorf("alone with nothing to say: %v, want silence", out)
	}
}

// Only a reply that starts while a call is up holds the room back from the call: one already playing
// when a call is answered leaves the call's uplink alone.
func TestReplyStartedOnlyCountsDuringACall(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	p := Get()
	defer p.set(func(s *State) { s.Phase = Idle })

	p.set(func(s *State) { s.Phase = Idle })
	done := p.ReplyStarted()
	p.set(func(s *State) { s.Phase = Talking }) // answered while the reply plays
	if p.Replying() {
		t.Error("a reply begun before the call holds the call's uplink")
	}
	done()

	done = p.ReplyStarted()
	if !p.Replying() {
		t.Error("a reply begun during the call does not hold the room back")
	}
	done()
	if p.Replying() {
		t.Error("still replying after the reply was done")
	}
}

// A call's ring takes the speaker back once whatever took it (a turn's chime and reply over a ringing
// call) is done, rather than leaving the call ringing in silence.
func TestRingComesBackAfterAnotherSound(t *testing.T) {
	sound := speaker.Sound()
	ctx, cancel := context.WithCancel(context.Background())
	rang := make(chan struct{})
	go func() { ring(ctx, make([]int16, speaker.VoiceRate/10)); close(rang) }()
	defer func() { cancel(); <-rang }()

	waitFor := func(what string, cond func() bool) {
		t.Helper()
		for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
			if cond() {
				return
			}
		}
		t.Fatalf("timed out waiting for %s", what)
	}
	waitFor("the ring", func() bool { return sound.Holder() == "ringing" })

	other := sound.Claim("turn", func(context.Context, *speaker.Player) error { return nil })
	<-other.Done()
	waitFor("the ring to come back", func() bool { return sound.Holder() == "ringing" })
}

// While a turn listens, the far end is held back from the room, so the caller cannot speak into what the
// device is sending Home Assistant; once listening ends they are heard again.
func TestFarEndHeldWhileATurnListens(t *testing.T) {
	p := Get()
	if !playFarEnd() {
		t.Fatal("the far end held back with no turn listening")
	}
	done := p.TurnListening()
	if playFarEnd() {
		t.Error("the far end plays while a turn listens")
	}
	done()
	done() // twice is once
	if !playFarEnd() {
		t.Error("the far end still held back after the turn stopped listening")
	}
}
