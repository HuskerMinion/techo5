package led

import (
	"slices"
	"testing"
)

func musicRoom(m *Music) Room {
	return Room{
		Level:      func() float64 { return 0 },
		Music:      func() Music { return *m },
		Brightness: func() float64 { return 1 },
	}
}

func anyLit(f []Color) bool {
	return slices.ContainsFunc(f, func(c Color) bool { return c != (Color{}) })
}

func brightest(f []Color) int {
	top := 0
	for _, c := range f {
		top = max(top, int(c.R), int(c.G), int(c.B))
	}
	return top
}

func TestMusicEffectIsAChoiceForTheRoomSelectOnly(t *testing.T) {
	if !slices.Contains(Names(KindRoom), EffectMusic) {
		t.Fatalf("%q is not offered with the room reactions: %v", EffectMusic, Names(KindRoom))
	}
	if slices.Contains(EffectNames(), EffectMusic) {
		t.Fatalf("%q must not be in the light's own list", EffectMusic)
	}
}

func TestMusicEffectIsDarkWhenNothingPlays(t *testing.T) {
	// A dark frame is what lets the ring show whatever it would otherwise.
	m := Music{Level: 1, Bass: 1, Beat: 1}
	f, err := effect(EffectMusic, Color{}, musicRoom(&m))
	if err != nil {
		t.Fatal(err)
	}
	if anyLit(f(0)) {
		t.Fatal("lit with nothing playing")
	}
	if anyLit(effectFrame(t, Room{Level: func() float64 { return 1 }})) {
		t.Fatal("lit with no music source at all")
	}
}

func effectFrame(t *testing.T, r Room) []Color {
	t.Helper()
	f, err := effect(EffectMusic, Color{}, r)
	if err != nil {
		t.Fatal(err)
	}
	return f(0)
}

func settled(f Frame) []Color {
	var out []Color
	for i := 0; i < 40; i++ {
		out = f(0)
	}
	return out
}

func TestMusicEffectFollowsLevelAndBeat(t *testing.T) {
	m := Music{Playing: true, Level: 0.2, Bass: 0.2, Mid: 0.2, Treble: 0.2}
	f, _ := effect(EffectMusic, Color{}, musicRoom(&m))
	quiet := brightest(settled(f))
	if quiet == 0 {
		t.Fatal("dark while music plays")
	}

	m.Level, m.Bass, m.Mid, m.Treble = 0.9, 0.9, 0.9, 0.9
	loud := brightest(settled(f))
	if loud <= quiet {
		t.Fatalf("loud %d is not brighter than quiet %d", loud, quiet)
	}

	m.Level, m.Bass, m.Mid, m.Treble = 0.5, 0.5, 0.5, 0.5
	steady := brightest(settled(f))
	m.Beat = 1
	m.Beats++
	swell := brightest(settled(f))
	if swell <= steady {
		t.Fatalf("a beat did not brighten the ring: %d against %d", swell, steady)
	}
}

func TestMusicEffectNeverBeatsTheBrightnessCeiling(t *testing.T) {
	m := Music{Playing: true, Level: 1, Bass: 1, Mid: 1, Treble: 1, Beat: 1}
	r := musicRoom(&m)
	f, _ := effect(EffectMusic, Color{}, r)
	full := brightest(settled(f))

	r.Brightness = func() float64 { return 0.25 }
	g, _ := effect(EffectMusic, Color{}, r)
	dim := brightest(settled(g))
	if dim == 0 || dim > full/4+1 {
		t.Fatalf("at a quarter brightness got %d against %d", dim, full)
	}
}

func TestMusicEffectBeatsMoveTheColors(t *testing.T) {
	m := Music{Playing: true, Level: 0.6, Bass: 0.6, Mid: 0.6, Treble: 0.6}
	f, _ := effect(EffectMusic, Color{}, musicRoom(&m))
	before := settled(f)
	for i := 0; i < 8; i++ {
		m.Beats++
	}
	after := settled(f)
	if slices.Equal(before, after) {
		t.Fatal("colors did not move on the beats")
	}
}

func TestMusicEffectComesBackFromNothing(t *testing.T) {
	m := Music{Playing: true, Level: 1, Bass: 1, Mid: 1, Treble: 1}
	f, _ := effect(EffectMusic, Color{}, musicRoom(&m))
	settled(f)
	m.Playing = false
	if anyLit(f(0)) {
		t.Fatal("lit after the music stopped")
	}
	m.Playing = true
	if first := brightest(f(0)); first >= brightest(settled(f)) {
		t.Fatalf("the first frame back (%d) did not start from dark", first)
	}
}
