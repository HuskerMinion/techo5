package led

import (
	"math"
	"slices"
	"testing"
	"time"
)

var partyEffects = []string{EffectPartySpin, EffectPartyPulse}

// run is n frames of f at the driver's rate from start, the last of them returned.
func run(f Frame, start time.Duration, n int) ([]Color, time.Duration) {
	var out []Color
	for i := range n {
		out = f(start + time.Duration(i)*FrameInterval)
	}
	return out, start + time.Duration(n)*FrameInterval
}

func TestPartyEffectsAreChoicesForTheRoomSelectThatHearMusic(t *testing.T) {
	for _, name := range partyEffects {
		if !slices.Contains(Names(KindRoom), name) {
			t.Errorf("%q is not offered with the room reactions", name)
		}
		if slices.Contains(EffectNames(), name) {
			t.Errorf("%q must not be in the light's own list", name)
		}
		if !HearsMusic(name) {
			t.Errorf("%q does not ask for the music to be analyzed", name)
		}
	}
	if HearsMusic(EffectRoomSpin) || HearsMusic("") {
		t.Error("a reaction to the room asks for the music")
	}
}

func TestPartyEffectsAreDarkWhenNothingPlays(t *testing.T) {
	for _, name := range partyEffects {
		m := Music{Level: 1, Bass: 1, Beat: 1, Beats: 5}
		f, err := effect(name, Color{}, musicRoom(&m))
		if err != nil {
			t.Fatal(err)
		}
		if out, _ := run(f, 0, 10); anyLit(out) {
			t.Errorf("%s: lit with nothing playing", name)
		}

		m.Playing = true
		if out, _ := run(f, 0, 10); !anyLit(out) {
			t.Errorf("%s: dark while music plays", name)
		}
		m.Playing = false
		if out, _ := run(f, 0, 1); anyLit(out) {
			t.Errorf("%s: lit after the music stopped", name)
		}
	}
}

func TestPartyEffectsNeverBeatTheBrightnessCeiling(t *testing.T) {
	for _, name := range partyEffects {
		m := Music{Playing: true, Level: 1, Bass: 1, Beat: 1}
		r := musicRoom(&m)
		f, _ := effect(name, Color{}, r)
		full, _ := run(f, 0, 40)

		r.Brightness = func() float64 { return 0.25 }
		g, _ := effect(name, Color{}, r)
		dim, _ := run(g, 0, 40)
		if b := brightest(dim); b == 0 || b > brightest(full)/4+1 {
			t.Errorf("%s: at a quarter brightness got %d against %d", name, b, brightest(full))
		}
	}
}

func TestPartySpinTurnsAndChangesColorOnTheBeat(t *testing.T) {
	m := Music{Playing: true, Level: 0.5}
	f, _ := effect(EffectPartySpin, Color{}, musicRoom(&m))
	a, at := run(f, 0, 20)
	b, at := run(f, at, 5)
	if !slices.Equal(a, b) {
		t.Fatal("the arms move with no beat to throw them")
	}
	m.Beats++
	_, at = run(f, at, 1)
	a, at = run(f, at, 1)
	b, at = run(f, at, 3)
	if slices.Equal(a, b) {
		t.Fatal("a beat did not throw the arms round")
	}
	_, at = run(f, at, 60)

	// A beat with the arms where they are: the same frame time twice, so only the beat moves anything.
	before := f(at)
	m.Beats++
	after := f(at)
	if slices.Equal(before, after) {
		t.Fatal("a beat changed nothing")
	}
	if slices.Contains(before, after[0]) && slices.Contains(before, after[6]) && slices.Contains(before, after[11]) {
		t.Fatal("the colors did not move on with the beat")
	}
}

// way says which way the ring turned between two frames: +1 if b is a shifted one segment up the ring,
// -1 if down, whichever matches its brightness better.
func way(a, b []Color) int {
	miss := func(shift int) int {
		n := 0
		for i := range a {
			d := brightest(a[i:i+1]) - brightest(b[(i+shift+Segments)%Segments:][:1])
			n += max(d, -d)
		}
		return n
	}
	if miss(1) < miss(-1) {
		return 1
	}
	return -1
}

func TestPartySpinTurnsBackAfterAPhrase(t *testing.T) {
	m := Music{Playing: true}
	f, _ := effect(EffectPartySpin, Color{}, musicRoom(&m))

	// Which way a beat throws the arms, watched over the frame after it.
	thrown := func(at time.Duration) (int, time.Duration) {
		_, at = run(f, at, 60) // come to a stop from any beat before
		m.Beats++
		a, at := run(f, at, 1)
		b, at := run(f, at, 1)
		return way(a, b), at
	}

	forward, at := thrown(0)
	for range partyTurn - 1 {
		m.Beats++
		f(at)
	}
	if back, _ := thrown(at); forward == back {
		t.Fatalf("the arms turned %+d before a phrase of beats and %+d after", forward, back)
	}
}

func TestPartySpinSurgesOnTheBeat(t *testing.T) {
	// The level stays where a song under way keeps it; only the beats may change the speed.
	m := Music{Playing: true, Level: 0.9}
	f, _ := effect(EffectPartySpin, Color{}, musicRoom(&m))
	_, at := run(f, 0, 60)

	// How far the arms moved over two frames, from their shape alone: each frame's segments against its
	// brightest, so neither the colors a beat moves on nor its flash count as moving.
	shape := func(f []Color) []float64 {
		top := float64(brightest(f))
		out := make([]float64, len(f))
		for i := range f {
			out[i] = float64(brightest(f[i:i+1])) / top
		}
		return out
	}
	moved := func() float64 {
		a, next := run(f, at, 1)
		b, next := run(f, next, 2)
		at = next
		sa, sb := shape(a), shape(b)
		miss := 0.0
		for i := range sa {
			miss += math.Abs(sa[i] - sb[i])
		}
		return miss
	}
	coasting := moved()
	m.Beats++
	if surging := moved(); surging <= coasting {
		t.Fatalf("a beat did not speed the arms up: %.2f of change against %.2f coasting", surging, coasting)
	}
}

func TestPartySpinArmsStretchOnTheBeatAndShrinkBetween(t *testing.T) {
	m := Music{Playing: true}
	f, _ := effect(EffectPartySpin, Color{}, musicRoom(&m))
	lit := func(f []Color) int {
		n := 0
		for _, c := range f {
			if c != (Color{}) {
				n++
			}
		}
		return n
	}

	m.Beats++
	stretched, at := run(f, 0, 1)
	shrunk, _ := run(f, at, 12)
	if lit(stretched) != Segments {
		t.Fatalf("on the beat %d of %d segments are lit, want the whole ring", lit(stretched), Segments)
	}
	if lit(shrunk) > Segments/2 {
		t.Fatalf("half a second after the beat %d of %d segments are still lit", lit(shrunk), Segments)
	}
}

func TestPartyPulseFlashesANewColorOnEveryBeat(t *testing.T) {
	m := Music{Playing: true, Level: 0.5, Bass: 0.2}
	f, _ := effect(EffectPartyPulse, Color{}, musicRoom(&m))
	resting, at := run(f, 0, 30)

	var seen []Color
	for range 4 {
		m.Beats++
		flash := f(at)
		if brightest(flash) <= brightest(resting) {
			t.Fatalf("a beat did not flash: %d against %d", brightest(flash), brightest(resting))
		}
		for _, c := range flash {
			if c != flash[0] {
				t.Fatal("the flash is not the whole ring in one color")
			}
		}
		if slices.Contains(seen, flash[0]) {
			t.Fatalf("beat %d repeats a color: %v", len(seen)+1, flash[0])
		}
		seen = append(seen, flash[0])

		// Half a second on, the next beat of most dance music, it has to have fallen far enough to be
		// seen to: a third of the flash, by the light the LEDs give.
		var faded []Color
		faded, at = run(f, at+FrameInterval, 12)
		if brightest(faded) > brightest(flash)/3 {
			t.Fatalf("the flash only fell from %d to %d", brightest(flash), brightest(faded))
		}
	}
}
