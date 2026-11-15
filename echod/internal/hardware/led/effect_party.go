package led

import (
	"math"
	"time"
)

// The party effects are what a party speaker's ring does: loud colors that move with the music, where
// Follows the Music is meant to be lived with. They follow the same rules as it does: dark when nothing
// plays, never brighter than the ring light's brightness, and lit at no less than musicFloor while
// music plays.

// party is every hue at full scale. The wheel the other effects use sits at 0.6 so as not to glare;
// glaring is the point here, and the ring light's brightness turns it down for anyone who disagrees.
var party = func() Palette {
	p := make(Palette, 6)
	for i := range p {
		p[i] = hue(float64(i) / float64(len(p)))
	}
	return p
}()

const (
	// partyArms is how many colored arms turn round the ring. A beat stretches them to fill it between
	// them, and they shrink back to partyShort of that, a dot each, by the next.
	partyArms  = 3
	partyShort = 0.25

	// How fast the arms turn, in revolutions a second. Every beat throws them round at partyKick, and
	// they slow over partyCoast to a stop, so they surge on the beat, a few segments, and stand still
	// until the next. The level would not do for this: it is measured against the music's own loudest,
	// so once a song is under way it hardly moves, and a speed following it is a steady one.
	partyKick  = 1.2
	partyCoast = 200 * time.Millisecond

	// partyTurn is how many beats the arms turn one way before they turn back, a phrase of most dance
	// music.
	partyTurn = 32

	// partyFlash is how long a beat's flash takes to fade to a third, and partyStep how far round the
	// colors each beat moves the pulse: the golden ratio's fraction, so that no two beats in a row are
	// close in color and no short run comes round to the same one.
	partyFlash = 220 * time.Millisecond
	partyStep  = 0.381966
)

// flashed is how bright a beat's flash leaves the ring, from the flash fading 1 to 0. Squared, because
// the eye sees light by its ratios: a ring at two thirds looks nearly as bright as a full one, and a
// flash has to fall most of the way to the floor to be seen to fall at all.
func flashed(flash float64) float64 {
	return musicFloor + (1-musicFloor)*flash*flash
}

// beats counts the beats since the last frame, at most a handful, which a long gap between frames would
// otherwise turn into a jump. It keeps seen up to date, including over a reset of the analysis.
func beats(m Music, seen *uint64) int {
	if m.Beats < *seen {
		*seen = m.Beats
	}
	n := int(min(m.Beats-*seen, 3))
	*seen = m.Beats
	return n
}

// step is the time since the last frame, nothing for the first or after a long pause.
func step(elapsed time.Duration, last *time.Duration) float64 {
	dt := elapsed - *last
	*last = elapsed
	if dt < 0 || dt > time.Second {
		return 0
	}
	return dt.Seconds()
}

// ceilingOf is the ring light's brightness, as the most an effect with colors of its own may show.
func ceilingOf(r Room) float64 {
	if r.Brightness == nil {
		return 1
	}
	return math.Max(0, math.Min(1, r.Brightness()))
}

// partySpin turns colored arms round the ring, each a bright head fading back to its tail. Every beat
// throws them round, stretches and flashes them, and moves their colors on a step; between beats they
// slow to a stop and shrink to dots. Every partyTurn beats they turn back the other way.
func partySpin(p Palette, r Room) Frame {
	var (
		seen    uint64
		count   int
		last    time.Duration
		phase   float64
		speed   float64
		hues    int
		flash   float64
		reverse bool
	)

	return func(elapsed time.Duration) []Color {
		out := make([]Color, Segments)
		dt := step(elapsed, &last)
		if r.Music == nil {
			return out
		}
		m := r.Music()
		if !m.Playing {
			speed, flash, seen = 0, 0, m.Beats
			return out
		}

		speed *= math.Exp(-dt / partyCoast.Seconds())
		flash *= math.Exp(-dt / partyFlash.Seconds())

		n := beats(m, &seen)
		for range n {
			count++
			if count%partyTurn == 0 {
				reverse = !reverse
			}
		}
		hues += n
		if n > 0 {
			speed, flash = partyKick, 1
		}

		if reverse {
			phase -= speed * dt
		} else {
			phase += speed * dt
		}

		// Never as dark as the pulse falls: the arms are mostly dark already.
		light := flashed(0.5+0.5*flash) * ceilingOf(r)
		length := partyShort + (1-partyShort)*flash
		for i := range out {
			// Which arm is over this segment, and how far along it: the head is the end that leads the
			// way the arms turn, and the light fades back from it to the tail, as far as the arm reaches.
			x := math.Mod(float64(i)/Segments-phase, 1)
			if x < 0 {
				x++
			}
			x *= partyArms
			along := x - math.Floor(x)
			if reverse {
				along = 1 - along
			}
			tail := (along - (1 - length)) / length
			if tail < 0 {
				continue
			}
			c := p.Nth(hues + int(x)*len(p)/partyArms)
			out[i] = scale(c, light*(0.15+0.85*tail))
		}
		return out
	}
}

// partyPulse flashes the whole ring on every beat, each in a color far round the wheel from the last,
// and fades it nearly out until the next. It never falls below a third of the bass, so music without a
// beat to find still moves it.
func partyPulse(p Palette, r Room) Frame {
	var (
		seen  uint64
		last  time.Duration
		color float64
		flash float64
	)

	return func(elapsed time.Duration) []Color {
		out := make([]Color, Segments)
		dt := step(elapsed, &last)
		if r.Music == nil {
			return out
		}
		m := r.Music()
		if !m.Playing {
			flash, seen = 0, m.Beats
			return out
		}

		flash *= math.Exp(-dt / partyFlash.Seconds())
		if n := beats(m, &seen); n > 0 {
			color += partyStep * float64(n)
			flash = 1
		}

		light := flashed(math.Max(flash, m.Bass/3)) * ceilingOf(r)
		c := scale(p.At(color), light)
		for i := range out {
			out[i] = c
		}
		return out
	}
}
