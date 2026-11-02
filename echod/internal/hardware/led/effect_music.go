package led

import (
	"math"
	"time"
)

// Music is a reading of the music the device is playing: the same numbers the analysis publishes, so the
// ring package need not know where they come from.
type Music struct {
	// Playing is whether music is sounding. An effect draws nothing while it is not.
	Playing bool

	// Level and the three bands are 0 to 1, against the music's own recent loudest.
	Level, Bass, Mid, Treble float64

	// Beat is 1 at a beat and fades; Beats counts them.
	Beat  float64
	Beats uint64
}

// calm is the palette for the music: deep blue through teal and violet to a muted magenta and round
// again, all of it dark enough not to glare in a room at night.
var calm = Palette{
	{R: 16, G: 40, B: 130},
	{R: 0, G: 100, B: 140},
	{R: 56, G: 48, B: 140},
	{R: 120, G: 40, B: 110},
}

const (
	// A ring that is lit hums (see roomGlow), so it only rests lit while music plays, and then at no
	// less than musicFloor of what it can do.
	musicFloor = 0.10

	// How much of a segment's light is the music as a whole, a band's share being the rest.
	musicWhole = 0.6

	// How far a frame closes the gap to where the light is going, so a beat is felt as a swell over a few
	// frames rather than a flash, and how fast the colors drift toward where the beats have sent them.
	musicFollow = 0.45
	musicDrift  = 0.12

	// beatShift is how far round the palette each beat moves the colors.
	beatShift = 0.07
)

// followsMusic lights the ring with the music: the level sets how bright, the three bands make thirds of
// the ring a little brighter or dimmer, and each beat swells the light and moves the colors on a step.
//
// It keeps a little state between frames, as room effects may. Nothing playing is a dark ring, and a dark
// ring is what lets whatever is under it show.
func followsMusic(p Palette, r Room) Frame {
	var (
		seen   uint64
		shift  float64
		shown  = make([]float64, Segments)
		target float64
	)

	return func(time.Duration) []Color {
		out := make([]Color, Segments)
		if r.Music == nil {
			return out
		}
		m := r.Music()
		if !m.Playing {
			// Start from nothing when it comes back, not from where the last song left it.
			for i := range shown {
				shown[i] = 0
			}
			seen = m.Beats
			return out
		}

		ceiling := 1.0
		if r.Brightness != nil {
			ceiling = math.Max(0, math.Min(1, r.Brightness()))
		}

		if m.Beats < seen {
			seen = m.Beats // the analysis was reset
		}
		if m.Beats != seen {
			// A beat or several since the last frame: one step each, but never more than a handful
			// at once, which a long gap between frames would otherwise turn into a jump.
			target += beatShift * float64(min(m.Beats-seen, 3))
			seen = m.Beats
		}
		shift += (target - shift) * musicDrift

		// The bass, mid and treble each own a third of the ring, and a segment between two takes both
		// in proportion, so the thirds blend rather than step.
		bands := [3]float64{m.Bass, m.Mid, m.Treble}
		for i := 0; i < Segments; i++ {
			x := (float64(i) + 0.5) / Segments // 0 to 1 round the ring
			band := interpolate(bands, math.Mod(x+1.0/3, 1))

			want := musicFloor + (1-musicFloor)*(musicWhole*m.Level+(1-musicWhole)*band)
			want = math.Min(1, want+0.25*m.Beat)
			shown[i] += (want - shown[i]) * musicFollow

			out[i] = scale(p.At(shift+x*0.25), shown[i]*ceiling)
		}
		return out
	}
}

// interpolate reads three values laid at equal steps round a circle, blending between the nearest two.
func interpolate(v [3]float64, x float64) float64 {
	pos := x * 3
	i := int(pos) % 3
	f := pos - math.Floor(pos)
	return v[i]*(1-f) + v[(i+1)%3]*f
}
