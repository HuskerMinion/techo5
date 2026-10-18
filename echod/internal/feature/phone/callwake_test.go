package phone

import (
	"math"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
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
