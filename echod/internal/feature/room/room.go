// Package room is the ring following the room: chosen once and then simply on, which is why it is
// not in the light's effect list. An effect there is an appearance Home Assistant set and can set
// again; this is a standing instruction to show what the microphone hears until told otherwise.
//
// It holds its own claim above the light's resting color, so choosing None reveals whatever the
// light was set to without this having to remember or restore anything. A conversation, a volume
// change or a failure covers it and gives it back on its own.
package room

import (
	"sync"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/light"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/led"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/mic"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// After the light, whose color it inherits.
func init() {
	component.Register(component.Device, Get(), component.Order(20))
}

type Reaction struct {
	sel   *esphome.Select
	claim *led.Claim

	// room is what the effect may ask about the room, and base what color to show it in. Both are
	// read at the moment a frame is drawn rather than captured, so leveling and the light's color
	// stay live.
	room led.Room
	base func() led.Color
}

var (
	once   sync.Once
	shared *Reaction
)

func Get() *Reaction {
	once.Do(func() { shared = build() })
	return shared
}

func build() *Reaction {
	r := &Reaction{
		sel: &esphome.Select{
			Base: esphome.Base{
				ObjectID: "room_reaction",
				DeviceID: component.DeviceRing,
				Name:     "Ring follows the room",
				Icon:     "mdi:waveform",
				Category: esphome.CategoryConfig,
			},
		},
		base:  light.Get().Base,
		claim: led.Get().Claim(led.PriorityRoom),
	}

	source := mic.Get()
	r.room = led.Room{
		Level:  source.Level,
		Facing: source.Facing,
		Music:  music,
		// The light's brightness is the ceiling for the music effect, which brings colors of its own.
		// A light that is off has none to give, and leaves the effect at full.
		Brightness: r.brightness,
	}

	component.BindEffect(r.sel, led.Names(led.KindRoom), r.show, config.Set().Ring().Reaction)

	// The light's color is inherited, and the claim holds the color it was given rather than
	// looking it up, so a change has to be handed over.
	light.Get().OnColor(r.recolor)
	return r
}

// music is what the speaker's tap reads, in the ring's own terms.
func music() led.Music {
	m := speaker.Get().Music()
	return led.Music{Playing: m.Playing, Level: m.Level, Bass: m.Bass, Mid: m.Mid, Treble: m.Treble,
		Beat: m.Beat, Beats: m.Beats}
}

// brightness is the light's brightest channel, 0 to 1, or 1 when the light is off.
func (r *Reaction) brightness() float64 {
	c := r.base()
	top := max(c.R, c.G, c.B)
	if top == 0 {
		return 1
	}
	return float64(top) / 255
}

func (r *Reaction) Name() string { return "room reaction" }

func (r *Reaction) Entities() []esphome.Entity {
	if !led.HasRing {
		return nil
	}
	return []esphome.Entity{r.sel}
}

// Restore starts following the room again, if that is what was chosen.
func (r *Reaction) Restore(c config.Config) {
	if !led.HasRing {
		// A choice saved before the control was taken away from a device with no ring would follow
		// the room for nothing, every frame, with no way left to turn it off.
		return
	}
	component.RestoreEffect(r.sel, c.Ring.Reaction, r.show, config.Set().Ring().Reaction)
}

// show puts the chosen reaction on the ring, or takes it off. Empty is None: the claim is cleared
// rather than released, so whatever the light was set to comes back on its own.
func (r *Reaction) show(name string) {
	// Listening to what is played costs a little on every period, so it is only on while it is wanted.
	speaker.Get().MusicTap(wantsMusic(name))
	if name == "" {
		r.claim.Clear()
		return
	}
	r.claim.React(name, r.base(), r.room)
}

// wantsMusic is whether a choice follows the music, and so needs the speaker to analyze it.
func wantsMusic(name string) bool { return name == led.EffectMusic }

func (r *Reaction) recolor() { r.show(config.Get().Ring.Reaction) }
