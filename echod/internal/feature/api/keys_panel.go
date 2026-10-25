//go:build panel

package api

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"
)

// firstStart opens the adoption window on a panel's first start, so the zero key it serves with
// until Home Assistant sets one does not stay open: the app has just been installed, and somebody is
// at the tablet adding it. If no Home Assistant adds it in time, the window shuts with a random key
// as an Echo's does, and the setup page opens another.
func (a *API) firstStart(psk *esphome.PSK) *esphome.PSK {
	if !psk.IsZero() {
		return psk
	}
	if _, err := os.Stat(keyFile); !errors.Is(err, fs.ErrNotExist) {
		return psk
	}
	if _, ok := readAdopt(); ok {
		return psk // resumeAdoption has it
	}
	until := time.Now().Add(adoptWindow).Truncate(time.Second)
	if err := os.WriteFile(adoptPath, []byte(until.UTC().Format(time.RFC3339)+"\n"), 0o600); err != nil {
		slog.Error("api: opening the first window a Home Assistant could add this device in", "err", err)
		return psk
	}
	slog.Warn("api: new panel, a Home Assistant may add this device now", "until", until)
	a.shutAdoptionAt(until)
	return psk
}
