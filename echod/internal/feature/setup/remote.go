package setup

// Remote setup: the setup page reached from away over the tailnet, and a browser let in from Home
// Assistant instead of by a press on the device. For a device nobody can stand at: one in another
// house, run from this Home Assistant (docs/tailscale.md).
//
// Both are Home Assistant's to give, over its encrypted link, and neither outlives the page:
//   - Remote setup (a switch) opens the page and lets its own paths through the tailnet
//     (web.RemoteSetup), asked for by the device's address and not a name, which a site of somebody
//     else's could point at it. Every other page on the port stays this house's. It holds for the one
//     opening of the page it was turned on in: off at every start, off with that page however it
//     closes, and on only while the link has a key of its own.
//   - Let in from Home Assistant (a button) lets in the browser asking now, as a press on the device
//     would, but only one asking over the tailnet: a browser in the device's own home is let in at the
//     device. Setup page asking (a sensor) is where the browser asking is asking from, so that whoever
//     presses knows whom they let in. The screen says so too, so whoever is in the room knows
//     somebody else is on the page.
//   - Turning it off, or the page closing, lets go of every browser it let in over the tailnet.

import (
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/feature/notify"
	"github.com/HuskerMinion/techo5/echod/internal/feature/web"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// remoteSetup is Remote setup's state and its entities. Past on, held with the Feature's mu.
type remoteSetup struct {
	on     atomic.Bool
	sw     *esphome.Switch
	btn    *esphome.Button
	asking *esphome.TextSensor

	// opened is the opening of the page it was turned on in (Feature.openedAt then): any other
	// opening is not it.
	opened time.Time
	// from is where the browser that asked last asked from, and away whether that is over the
	// tailnet; token says which browser that was. Noted as it asks (awaitFrom).
	token, from string
	away        bool
	// sessions are the browsers let in over the tailnet, let go with Remote setup.
	sessions map[string]bool
}

// byHA is admit's by for a browser let in from Home Assistant: only while Remote setup is on.
const byHA = "Home Assistant"

// keyed is whether the Home Assistant link has a key of its own rather than the reserved all-zeros one
// an unprovisioned device answers with: without one, anybody on the network could flip the switch.
var keyed = func() bool {
	b, err := os.ReadFile(layout.KeyPath)
	if err != nil {
		return false
	}
	k, err := esphome.ParsePSK(strings.TrimSpace(string(b)))
	return err == nil && !k.IsZero()
}

// letInNotice puts up on the screen that Home Assistant let a browser in; a variable for tests.
var letInNotice = func(from string) {
	if err := notify.Get().Notify("Home Assistant let a browser at "+from+" into the setup page, over Tailscale.",
		"Setup page", "mdi:tools", "", "", "", 60); err != nil {
		slog.Warn("setup page: the notice of a browser let in was not shown", "err", err)
	}
}

// buildRemote makes Remote setup's entities.
func (f *Feature) buildRemote() {
	f.remote.sw = &esphome.Switch{
		Base: esphome.Base{
			ObjectID: "setup_remote",
			Name:     "Remote setup",
			Icon:     "mdi:tools",
			Category: esphome.CategoryConfig,
		},
		OnCommand: f.setRemote,
	}
	f.remote.btn = &esphome.Button{
		Base: esphome.Base{
			ObjectID: "setup_let_in",
			Name:     "Let in from Home Assistant",
			Icon:     "mdi:tools",
			Category: esphome.CategoryConfig,
		},
		OnPress: func() { f.letInFromHA() },
	}
	f.remote.asking = &esphome.TextSensor{
		Base: esphome.Base{
			ObjectID: "setup_asking",
			Name:     "Setup page asking",
			Icon:     "mdi:tools",
			Category: esphome.CategoryDiagnostic,
		},
	}
}

// setRemote is the Remote setup switch. On opens the page and lets the tailnet reach it, for this
// opening of it; off keeps the page this house's again.
func (f *Feature) setRemote(on bool) {
	if !on {
		f.remoteOff()
		return
	}
	if !keyed() {
		slog.Warn("setup page: remote setup refused; set an API encryption key first")
		f.remote.sw.Set(false)
		return
	}
	f.Open()
	now := time.Now()
	f.mu.Lock()
	ok := f.on(now)
	if ok {
		f.remote.opened = f.openedAt
		// Only on the turn from off: only browsers let in from then on are this turn's to let go of. A
		// second on while it is on keeps the ones already let in, or turning it off would not drop them.
		if !f.remote.on.Load() {
			f.remote.sessions = nil
		}
		f.remote.on.Store(true)
	}
	f.mu.Unlock()
	f.remote.sw.Set(ok)
	if ok {
		slog.Warn("setup page: remote setup on; the page can be reached from away over the tailnet")
	}
}

// remoteOff turns Remote setup off, and lets go of the browsers it let in over the tailnet, and of the
// one still asking over it: nobody can let that one in any more, and the screen should not go on
// saying it asks.
func (f *Feature) remoteOff() {
	was := f.remote.on.Swap(false)
	f.mu.Lock()
	for token := range f.remote.sessions {
		delete(f.live, token)
	}
	gone := len(f.remote.sessions)
	f.remote.sessions = nil
	asking := f.waiting != "" && f.remote.away && f.waiting == f.remote.token
	if asking {
		f.waiting, f.waitingEnd = "", time.Time{}
	}
	f.mu.Unlock()
	if was || gone > 0 || asking {
		slog.Warn("setup page: remote setup off", "let go", gone, "asking dropped", asking)
	}
	if f.remote.sw != nil {
		f.remote.sw.Set(false)
	}
	if asking {
		f.showAsking()
		f.Changed.Emit(struct{}{})
	}
}

// Remote is whether the page may be reached over the tailnet now: Remote setup on, and the page open
// still in the opening it was turned on in. Any other comes to Remote setup off.
func (f *Feature) Remote() bool {
	if !f.remote.on.Load() {
		return false
	}
	now := time.Now()
	f.mu.Lock()
	ok := f.on(now) && f.openedAt.Equal(f.remote.opened)
	f.mu.Unlock()
	if !ok {
		f.remoteOff()
	}
	return ok
}

// showAsking puts where the browser waiting now asked from in Setup page asking: empty with none.
func (f *Feature) showAsking() {
	if f.remote.asking == nil {
		return
	}
	now := time.Now()
	f.mu.Lock()
	from := ""
	if f.waiting != "" && now.Before(f.waitingEnd) && f.waiting == f.remote.token {
		from = f.remote.from
		if !f.remote.away {
			from += " (in the device's home)"
		}
	}
	f.mu.Unlock()
	if f.remote.asking.Get() != from { // each Set is sent, even unchanged: Run calls this every 15 s
		f.remote.asking.Set(from)
	}
}

// letInFromHA lets in the browser asking now, as a press on the device would: only while Remote setup
// is on, the link keyed, and the browser asking over the tailnet.
func (f *Feature) letInFromHA() bool {
	defer f.showAsking()
	if !keyed() {
		slog.Warn("setup page: let in from Home Assistant refused; the link has no key of its own")
		f.remoteOff()
		return false
	}
	if !f.Remote() {
		slog.Warn("setup page: let in from Home Assistant refused; Remote setup is off")
		return false
	}
	now := time.Now()
	f.mu.Lock()
	token, from, away := f.waiting, f.remote.from, f.remote.away
	ok := token != "" && now.Before(f.waitingEnd) && token == f.remote.token
	f.mu.Unlock()
	switch {
	case !ok:
		slog.Info("setup page: let in from Home Assistant, but no browser is asking")
		return false
	case !away:
		slog.Warn("setup page: let in from Home Assistant refused; the browser asking is in the device's home",
			"from", from)
		return false
	}
	if !f.admit(token, byHA) {
		return false
	}
	slog.Warn("setup page: a browser from away let in from Home Assistant", "from", from)
	f.Changed.Emit(struct{}{})
	letInNotice(from)
	return true
}

// keepRemote is admit's: a browser let in that asked over the tailnet is Remote setup's to let go of.
// Held with mu.
func (f *Feature) keepRemote(token string) {
	if token != f.remote.token || !f.remote.away {
		return
	}
	if f.remote.sessions == nil {
		f.remote.sessions = map[string]bool{}
	}
	f.remote.sessions[token] = true
}

// wireRemote hands the web port the question of whether the tailnet may reach the page.
func (f *Feature) wireRemote() { web.RemoteSetup = f.Remote }
