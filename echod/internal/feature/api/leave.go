package api

import (
	"context"
	"crypto/rand"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"strings"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
	"github.com/HuskerMinion/techo5/echod/internal/update"
)

// Leaving the Home Assistant a device has, for a device given to somebody else. The access it uses to
// reach Home Assistant (the address and the token) is forgotten, and the key Home Assistant connects
// with is replaced by a random one nobody holds, so that Home Assistant cannot connect again. Then a
// restart, to serve the new key. Realtime voice's key and its token for Home Assistant go too: both are
// the old owner's. Alarms, radio stations, the Wi-Fi and the rest stay as they are.
//
// Never a key of zeros: that is the library's "not provisioned yet", which lets whichever Home
// Assistant adds the device first set its own key.

// leaveWord is what the action's confirm argument has to say: the action is one tap away in Home
// Assistant's action list, and what it does cannot be undone from Home Assistant.
const leaveWord = "leave"

// leaveDelay lets the action's answer reach Home Assistant before the restart takes the connection.
const leaveDelay = 2 * time.Second

func (a *API) Actions() []*esphome.Action {
	return []*esphome.Action{{
		Name: "leave_home_assistant",
		Args: []esphome.Arg{{Name: "confirm", Type: esphome.ArgString}},
		Run: func(c esphome.Call) (any, error) {
			if strings.ToLower(strings.TrimSpace(c.String("confirm"))) != leaveWord {
				return nil, errors.New(`nothing was changed: set confirm to "leave" to disconnect this device from Home Assistant for good`)
			}
			if err := leave(layout.KeyPath, hass.Path); err != nil {
				return nil, err
			}
			slog.Warn("left Home Assistant: its access forgotten and a new key made; restarting")
			safe.Go("leave home assistant", func() {
				time.Sleep(leaveDelay)
				leaveEntry()
				update.Restart("left Home Assistant")
			})
			return nil, nil
		},
	}}
}

// leaveEntry deletes Home Assistant's entry for this device, which holds a key the device no longer
// has, when the access just forgotten was an administrator's (the client still holds it until the
// restart). Without one it stays, for deleting by hand, as the docs say.
var leaveEntry = func() {
	if !hass.Get().Ready() {
		return
	}
	mac, err := layout.FactoryMAC()
	if err != nil || mac == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if n, err := hass.Get().RemoveOwnEntry(ctx, mac); err != nil {
		slog.Warn("left Home Assistant, but could not delete its entry there; delete it by hand", "err", err)
	} else {
		slog.Info("left Home Assistant and deleted its entry there", "entries", n)
	}
}

// leave forgets the access first and changes the key second: stopped between the two, the device
// still has a key Home Assistant knows, and running it again finishes the job. The other way round
// would leave it calling a Home Assistant that can no longer reach it.
func leave(keyPath, hassPath string) error {
	var k esphome.PSK
	if _, err := rand.Read(k[:]); err != nil {
		return err
	}
	if k == (esphome.PSK{}) {
		return errors.New("api: the new key came out as zeros")
	}
	if err := config.Set().Realtime().SetKey(""); err != nil {
		return err
	}
	if err := config.Set().Realtime().SetMCPToken(""); err != nil {
		return err
	}
	if err := os.Remove(hassPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return writePSK(keyPath, k)
}
