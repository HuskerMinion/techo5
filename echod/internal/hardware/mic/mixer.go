package mic

import (
	"log/slog"
	"sync"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/subband"
)

// Mixer reduces the array's channels to the one channel everything downstream reads. Wake detection
// and what Home Assistant transcribes come from the same mix, so they never disagree about what was
// heard.
//
// Which mix is best is a property of the room rather than of the code — an array this small gains
// against diffuse noise but cannot null a directional interferer — so it is a setting.
type Mixer interface {
	// Mix takes one frame per microphone, oldest sample first, and returns the combined frame. It is
	// called from the reader goroutine and may keep state between calls.
	Mix(mics [][]int16) []int16
}

// Center is the middle microphone alone.
type Center struct{}

// Average is every microphone summed and divided: no steering, so nothing to get wrong, and a
// little gain against whatever only one of them hears.
type Average struct{}

func (Average) Mix(mics [][]int16) []int16 {
	if len(mics) == 0 || len(mics[0]) == 0 {
		return nil
	}
	out := make([]int16, len(mics[0]))
	for i := range out {
		acc := 0
		for _, m := range mics {
			if i < len(m) {
				acc += int(m[i])
			}
		}
		out[i] = int16(acc / len(mics))
	}
	return out
}

func (Center) Mix(mics [][]int16) []int16 {
	if len(mics) <= CenterMic {
		return nil
	}
	return mics[CenterMic]
}

// fixedPath marks a mix whose acoustic path never moves, which is what an echo canceller can learn.
// Anything that steers is not one.
type fixedPath interface{ fixedPath() }

func (Center) fixedPath()  {}
func (Average) fixedPath() {}

// cancelInput is what the echo canceller reads for this frame. Where CancelOnMix, a fixed mix is
// cancelled as it is, and a steered one falls back to the plain average, the fixed path nearest to it;
// otherwise it is the center microphone, whatever the mix.
func cancelInput(m Mixer, mics [][]int16, mixed []int16) []int16 {
	if !CancelOnMix {
		return mics[CenterMic]
	}
	if _, ok := m.(fixedPath); ok && mixed != nil {
		return mixed
	}
	return Average{}.Mix(mics)
}

type mix struct {
	name config.Mixing
	make func() Mixer
}

// What this build can do, in the order it is offered. The vendor's own beamformer is in the list
// only when its coefficients are there to read — 460 KB of text on the vendor partition. Building
// the list reads them, and it is built once per boot, since the microphone component asks for
// Mixings() as it binds; the once is so that it is read once, not once per caller.
var mixes = sync.OnceValue(func() []mix {
	out := []mix{
		{config.MixCenter, func() Mixer { return Center{} }},
		{config.MixAll, func() Mixer { return Average{} }},
	}
	// Steering needs the ring: the delays are the Dot's geometry, and two microphones have no
	// direction to resolve.
	if Mics >= 3 {
		out = append(out, mix{config.MixDelaySum, func() Mixer { return NewBeamformer() }})
	}

	if !VendorBeamformer {
		return out
	}
	w, err := subband.Load(subband.VendorDir)
	if err != nil {
		slog.Error("vendor beamformer unavailable", "err", err)
		return out
	}
	slog.Info("vendor beamformer available", "tuning", w.Name(),
		"bands", w.Bands(), "beams", w.Beams(), "mics", w.Inputs())
	return append(out, mix{config.MixBeamformer, func() Mixer { return w.New() }})
})

// Mixings lists what the array can be combined with.
func Mixings() []config.Mixing {
	out := make([]config.Mixing, 0, len(mixes()))
	for _, m := range mixes() {
		out = append(out, m.name)
	}
	return out
}

// NewMixer builds one, falling back to the array for anything this build does not have: a setting
// left over from another version, or from a device with the coefficients this one lacks, should not
// leave it deaf.
func NewMixer(m config.Mixing) (Mixer, config.Mixing) {
	for _, have := range mixes() {
		if have.name == m {
			return have.make(), m
		}
	}
	return Center{}, config.DefaultMixing
}
