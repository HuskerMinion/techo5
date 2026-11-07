package firstrun

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/buttons"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/led"
)

// The component: Home Assistant's action to reset the device for someone else, and, on a device with
// an action button and a volume down button (a Dot, an Echo 2nd gen), the same by holding both.

func init() { component.Register(component.Device, Get()) }

type Feature struct{}

var shared = &Feature{}

func Get() *Feature { return shared }

func (*Feature) Name() string { return "firstrun" }

// resetWord is what the action's confirm argument has to say: one tap in Home Assistant's action list
// must not wipe a device.
const resetWord = "reset"

func (*Feature) Actions() []*esphome.Action {
	if !CanReset() {
		return nil
	}
	return []*esphome.Action{{
		Name: "reset_for_someone_else",
		Args: []esphome.Arg{{Name: "confirm", Type: esphome.ArgString}},
		Run: func(c esphome.Call) (any, error) {
			if strings.ToLower(strings.TrimSpace(c.String("confirm"))) != resetWord {
				return nil, errors.New(`nothing was changed: set confirm to "reset" to clear this device for someone else`)
			}
			return nil, Reset()
		},
	}}
}

// comboHold is how long the action button and volume down are held together to reset the device, and
// comboLook how often the buttons are looked at while the daemon runs.
const (
	comboHold = 10 * time.Second
	comboLook = 250 * time.Millisecond
)

// pressed is the buttons' state (a variable for the tests).
var pressed = func(n buttons.Name) (bool, time.Time) { return buttons.Get().Pressed(n) }

// homeLook is how often a device with no screen looks whether Home Assistant has set it up.
const homeLook = 5 * time.Second

// Run finishes first run on a device with no screen once Home Assistant has it (doneByHome), and
// watches for the two buttons held together. The buttons report no combination of their own, so this
// looks at both being down: the hold each one is also reporting (the setup page, Bluetooth pairing,
// the volume going down) happens on the way, as it would for either alone.
func (f *Feature) Run(ctx context.Context) error {
	combo := CanReset() && buttons.Has(buttons.Action) && buttons.Has(buttons.VolumeDown)
	t := time.NewTicker(comboLook)
	defer t.Stop()
	var looked time.Time
	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-t.C:
			if now.Sub(looked) >= homeLook {
				looked = now
				doneByHome()
			}
			if !combo || !comboHeld(now) {
				continue
			}
			slog.Warn("action and volume down held for ten seconds: resetting for someone else")
			// The ring says it was taken: orange, the color first run is set up in, until the restart.
			claim := led.Get().Claim(led.PriorityBoot)
			claim.Show(led.Content{Frame: led.Solid(led.Setup)})
			if err := Reset(); err != nil {
				slog.Error("reset for someone else failed", "err", err)
				claim.Release()
			}
			// Held on, the same press must not reset it again before the restart.
			select {
			case <-ctx.Done():
			case <-time.After(time.Minute):
			}
		}
	}
}

// comboHeld is whether the action button and volume down have both been down for comboHold.
func comboHeld(now time.Time) bool {
	a, aAt := pressed(buttons.Action)
	v, vAt := pressed(buttons.VolumeDown)
	if !a || !v {
		return false
	}
	since := aAt
	if vAt.After(since) {
		since = vAt
	}
	return now.Sub(since) >= comboHold
}
