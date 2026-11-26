// Package addonhost is the device's side of the add-on API (docs/addon-api.md): the server for each
// running add-on's socket (pkg/addonapi/serve), and what each request does here. Commands reach an
// add-on from Home Assistant (the addon_command action and a button per command); its words are said
// by Home Assistant's announce on this device, as a reminder's are; its timers are the device's own;
// its keep-awake holds keep the screen in use; its settings are drawn on its card on the setup page;
// its entities are made in Home Assistant under its id; and it can play a video or audio here.
//
// Nothing here is a device setting. What an add-on adds comes with it and goes with it.
package addonhost

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/addons"
	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
	"github.com/HuskerMinion/techo5/echod/pkg/addonapi"
	"github.com/HuskerMinion/techo5/echod/pkg/addonapi/serve"
)

func init() {
	if !addons.Here {
		return
	}
	// After the add-ons' update entities (Order 98): the entity list is read in this order, so should an
	// add-on's entity have the key of another add-on's update entity, the add-on's entity is the one
	// left out (api.addEach), never the update entity the owner relies on.
	component.Register(component.Network, Get(), component.Order(99))
}

// Host is the add-on API on this device. There is one (Get); tests make their own.
type Host struct {
	srv      *serve.Server
	settings *settingsStore
	dev      *device

	// grants is the installed add-ons that speak the API (addons.Manager.APIGrants), and names their
	// names, for the entities: variables, so tests need no supervisor.
	grants func() []addons.APIGrant
	names  func() map[string]string

	mu       sync.Mutex
	entities map[string]*addonEntities // by add-on id
}

var (
	once   sync.Once
	shared *Host
)

// Get is the device's add-on API.
func Get() *Host {
	once.Do(func() {
		f := addons.Get()
		shared = newHost(serve.DefaultDir, func() string { return addons.Root }, f.InstalledUID, &device{})
		shared.grants = f.APIGrants
		shared.names = func() map[string]string {
			out := map[string]string{}
			for _, s := range f.Installed() {
				out[s.ID] = s.Name
			}
			return out
		}
		f.SetAPI(shared)
	})
	return shared
}

func newHost(dir string, root func() string, allowed func(uint32) bool, d *device) *Host {
	h := &Host{settings: &settingsStore{root: root}, dev: d, entities: map[string]*addonEntities{},
		grants: func() []addons.APIGrant { return nil }, names: func() map[string]string { return nil }}
	d.host = h
	h.srv = serve.New(dir, d, allowed)
	timer.Get().Owned.Listen(h.timerEvent)
	return h
}

func (h *Host) Name() string { return "add-on API" }

// The supervisor's half (addons.API).

// Open makes an add-on's socket, granting it what its manifest uses and the owner agreed to.
func (h *Host) Open(g addons.APIGrant) (string, error) {
	return h.srv.Open(serve.Grant{ID: g.ID, UID: g.UID, Caps: g.Capabilities(), Commands: g.Man.Commands,
		Entities: g.Man.Entities, Settings: g.Man.Settings, Files: g.Man.Files, BinDir: g.BinDir})
}

// Close takes an add-on's socket away as it stops: its connections and keep-awake holds go with it.
func (h *Host) Close(id string) { h.srv.Close(id) }

// Removed is an add-on gone from the device: its timers are canceled, and what it played stopped, and
// its entities are not made again (Home Assistant reads the list again on the reconnect the removal
// asks for).
func (h *Host) Removed(id string) {
	h.srv.Close(id)
	if n := timer.Get().CancelAllOwned(id); n > 0 {
		slog.Info("addonhost: a removed add-on's timers were canceled", "addon", id, "count", n)
	}
	h.dev.forget(id)
	h.mu.Lock()
	delete(h.entities, id)
	h.mu.Unlock()
}

// timerEvent tells an add-on what became of its timer.
func (h *Host) timerEvent(e timer.OwnedEvent) {
	h.srv.Emit(e.Owner, addonapi.EventTimer, addonapi.TimerEvent{ID: e.ID, Name: e.Name, State: e.State, Quiet: e.Quiet})
}

// Banned is until when an add-on is disconnected for going past its limits, for its card.
func (h *Host) Banned(id string) (time.Time, bool) { return h.srv.Banned(id) }

// Connected is whether the add-on is connected to the API now, for its card.
func (h *Host) Connected(id string) bool { return h.srv.Connected(id) > 0 }

// grant is an installed add-on's grant, when it speaks the API.
func (h *Host) grant(id string) (addons.APIGrant, bool) {
	for _, g := range h.grants() {
		if g.ID == id {
			return g, true
		}
	}
	return addons.APIGrant{}, false
}

// Settings is an add-on's settings schema and its values as saved, when it was granted settings. A
// secret's value is in it: the caller that draws a form must never put one back in the page.
func (h *Host) Settings(id string) (*addonapi.Settings, map[string]any, bool) {
	g, ok := h.grant(id)
	if !ok || g.Man.Settings == nil || !has(g.Capabilities(), addonapi.CapSettings) {
		return nil, nil, false
	}
	return g.Man.Settings, h.settings.load(id, g.Man.Settings), true
}

func has(caps []string, c string) bool {
	for _, x := range caps {
		if x == c {
			return true
		}
	}
	return false
}

// SaveSettings saves the settings in a setup page form, writing only when they changed, and tells the
// add-on the new values when they did.
func (h *Host) SaveSettings(id string, form func(string) string) (bool, error) {
	schema, saved, ok := h.Settings(id)
	if !ok {
		return false, fmt.Errorf("%s has no settings", id)
	}
	values, err := fromForm(schema, form, saved)
	if err != nil {
		return false, err
	}
	changed, err := h.settings.save(id, values)
	if err != nil || !changed {
		return changed, err
	}
	slog.Info("addonhost: an add-on's settings were changed on the setup page", "addon", id)
	h.srv.Emit(id, addonapi.EventSettings, addonapi.Values{Values: values})
	return true, nil
}

// testWait is how long Test waits for the add-on's answer.
var testWait = 10 * time.Second

// TestSettings asks the add-on whether the settings in the form work, without saving them: ok, or its
// words for why not.
func (h *Host) TestSettings(ctx context.Context, id string, form func(string) string) (bool, string, error) {
	schema, saved, ok := h.Settings(id)
	if !ok {
		return false, "", fmt.Errorf("%s has no settings", id)
	}
	values, err := fromForm(schema, form, saved)
	if err != nil {
		return false, "", err
	}
	ctx, cancel := context.WithTimeout(ctx, testWait)
	defer cancel()
	return h.srv.Test(ctx, id, values)
}

// commandWait is how long the addon_command action waits for an add-on's answer. It runs on Home
// Assistant's connection, which waits with it, so an add-on is held to a few seconds: what it says
// back is meant to be said at once ("Step two: stir"), not fetched slowly.
var commandWait = 4 * time.Second

// Command calls an add-on's command from Home Assistant and waits for its answer.
func (h *Host) Command(id, name, args string) (string, error) {
	var a map[string]any
	if args != "" {
		if err := addonapi.Unmarshal([]byte(args), &a); err != nil {
			return "", fmt.Errorf("addon_command: args is a JSON object of the command's arguments: %v", err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), commandWait)
	defer cancel()
	return h.srv.Command(ctx, id, name, a, "action", true)
}

// SetScreenSize is the screen's size, which hello tells an add-on; the display sets it as it opens.
func SetScreenSize(w, h int) {
	screenMu.Lock()
	screenW, screenH = w, h
	screenMu.Unlock()
}

var (
	screenMu         sync.Mutex
	screenW, screenH int
)

// language is the screen's, as hello says it.
func language() string {
	if l := config.Get().Screen.Language; l != "" {
		return l
	}
	return "en"
}

// asAPIError keeps an error the device returned for a request as one of the API's codes, or makes it
// one: words for the add-on's log, never more of the device than that.
func asAPIError(code string, err error) error {
	if err == nil {
		return nil
	}
	var e *addonapi.Error
	if errors.As(err, &e) {
		return e
	}
	return addonapi.Errorf(code, "%v", err)
}

// logCommand says a command from Home Assistant was not carried out.
func logCommand(id, cmd string, err error) {
	slog.Info("addonhost: a command from Home Assistant was not carried out", "addon", id, "command", cmd, "err", err)
}
