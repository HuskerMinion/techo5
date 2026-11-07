package api

import (
	"log/slog"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Connected is whether anything is connected to the API now: Home Assistant, almost always. First run
// (display/firstrun.go) goes on by itself once it is.
func (a *API) Connected() bool { return a.clients.Load() > 0 }

// Key is the encryption key Home Assistant asks for when it adds the device, for first run to show;
// empty when the device serves with none, or with the zero key a Home Assistant replaces (adopt.go).
func (a *API) Key() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if keyKind(a.key) != keyReal {
		return ""
	}
	return a.key.String()
}

// SetArea keeps the room first run chose and serves afresh, so the Home Assistant that adds the
// device next is told it as the suggested area. One that has added it already keeps its own.
func (a *API) SetArea(room string) error {
	if err := config.Set().Home().Area(room); err != nil {
		return err
	}
	slog.Info("api: suggested area", "area", room)
	a.Reconnect()
	return nil
}
