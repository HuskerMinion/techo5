//go:build !dot

// Package dashboard puts a Home Assistant dashboard on the screen, in one of two ways. Drawn, the
// device reads the dashboard's cards and draws them itself, in its own style: fast, and nothing else
// to run, but only the cards it knows. Streamed, a dashcast server runs a browser for it and sends the
// dashboard as pictures, and the device sends back where it was touched: every card looks exactly as
// it does in Home Assistant, at the cost of a server and a moment's wait on every tap.
//
// This package holds the settings and the stream; the display draws the page.
package dashboard

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hook"
)

// automatic is the choice of no particular dashboard: the Rooms dashboard when drawn here, Home
// Assistant's default one when streamed.
const automatic = "Automatic"

// boardsEvery is how often Home Assistant is asked what dashboards there are: often enough that one
// just made is there to pick without a restart. An unchanged list, the usual answer, costs nothing.
const boardsEvery = 5 * time.Minute

// wantsList is whether Home Assistant is asked what dashboards there are. While a dashboard is on the
// list is kept current, so one just made is there to pick without a restart; while it is off it is asked
// once, and only while there is nothing cached. The picker is built from that list, and it is reached
// for before a dashboard is on - the only state a device that has never shown one is in - so a cache
// nobody has filled has to be asked for, or the picker has nothing to offer and never will.
func wantsList(mode config.DashboardMode, known []config.DashboardChoice) bool {
	return mode != config.DashboardOff || len(known) == 0
}

func init() {
	component.Register(component.Device, Get(), component.Order(37))
}

// Feature is the dashboard's settings and, while it is up, its stream.
type Feature struct {
	// Changed fires when there is something new to draw; listeners must not block.
	Changed hook.Hook[struct{}]

	// Asked fires when Home Assistant asks for the dashboard: true to put it up (dashboard_show),
	// false to take it down (dashboard_hide). The screen does the rest.
	Asked hook.Hook[bool]

	mode  *esphome.Select
	idle  *esphome.Switch
	kiosk *esphome.Switch
	board *esphome.Select
	// boardOpts keeps board's options.
	boardOpts *component.Options

	// back and tiles are how long a dashboard opened by hand stays up, and on the Show how big a drawn
	// one's tiles are (more.go).
	back  *esphome.Select
	tiles *esphome.Select

	mu        sync.Mutex
	stream    *stream  // while the page is up in streamed mode
	drawn     *session // while the page is up drawn, for drawnPath
	drawnPath string
	// navTo is the path a tap on the page is going to, until the page is asked for it there: the one
	// change of path that holds the page it left (session.hold).
	navTo string
	// recent is the last few pictures the drawn page showed, kept between its views.
	recent recent

	// When the page last asked for each: a session nobody has asked for in a while is closed, however
	// the page went away - a turn, the screen going dark, the night.
	streamUsed, drawnUsed time.Time

	// look asks Run to list Home Assistant's dashboards now; relist is a changed list whose reconnect
	// is waiting for the device to be idle, and relisted when the last one was asked for (relistDue).
	look     chan struct{}
	relist   bool
	relisted time.Time
}

// relistGap is the least time between two reconnects for a changed list: each drops the link to Home
// Assistant for a moment, and a run of taps between views must not cost one each.
const relistGap = 3 * time.Minute

// relistDue is whether a changed list's reconnect is to be asked for now: once the device is not in a
// turn, and not within relistGap of the last. One that has to wait stays pending.
func (f *Feature) relistDue(now time.Time, busy bool) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.relist || busy || (!f.relisted.IsZero() && now.Sub(f.relisted) < relistGap) {
		return false
	}
	f.relist, f.relisted = false, now
	return true
}

// unused is how long a session stays open with nothing asking for it: long enough to outlast a turn,
// so the dashboard is still there when the answer is done.
const unused = time.Minute

var (
	once   sync.Once
	shared *Feature
)

func Get() *Feature {
	once.Do(func() {
		f := &Feature{
			look: make(chan struct{}, 1),
			mode: &esphome.Select{
				Base: esphome.Base{
					ObjectID: "screen_dashboard",
					Name:     "Dashboard",
					Icon:     "mdi:view-dashboard",
					Category: esphome.CategoryConfig,
				},
			},
			idle: &esphome.Switch{
				Base: esphome.Base{
					ObjectID: "screen_dashboard_idle",
					Name:     "Dashboard when idle",
					Icon:     "mdi:view-dashboard-outline",
					Category: esphome.CategoryConfig,
				},
			},
			kiosk: &esphome.Switch{
				Base: esphome.Base{
					ObjectID: "screen_dashboard_kiosk",
					Name:     "Dashboard without its header",
					Icon:     "mdi:fullscreen",
					Category: esphome.CategoryConfig,
				},
			},
			board: &esphome.Select{
				Base: esphome.Base{
					ObjectID: "screen_dashboard_view",
					Name:     "Dashboard to show",
					Icon:     "mdi:view-dashboard-variant",
					Category: esphome.CategoryConfig,
				},
			},
		}
		f.back, f.tiles = backSelect(f), tilesSelect(f)
		f.board.OnCommand = f.chooseBoard
		f.boardOpts = component.NewOptions(f.board)
		component.Bind(f.mode, Modes(), f.setMode, config.Set().Dashboard().Mode)
		f.idle.OnCommand = func(on bool) {
			f.idle.Set(on)
			if err := config.Set().Dashboard().Idle(on); err != nil {
				slog.Error("saving the dashboard idle setting failed", "err", err)
			}
			f.Changed.Emit(struct{}{})
		}
		// Kiosk is asked for when the screen connects to dashcast, so a change reconnects.
		f.kiosk.OnCommand = func(on bool) {
			f.kiosk.Set(on)
			if err := config.Set().Dashboard().Kiosk(on); err != nil {
				slog.Error("saving the dashboard kiosk setting failed", "err", err)
			}
			slog.Info("dashboard: header", "hidden", on)
			f.setMode(f.Mode())
		}
		shared = f
	})
	return shared
}

func (f *Feature) Name() string { return "dashboard" }

func (f *Feature) Entities() []esphome.Entity {
	es := []esphome.Entity{f.mode, f.idle, f.kiosk, f.board, f.back}
	if hasTiles {
		es = append(es, f.tiles)
	}
	return es
}

func (f *Feature) Restore(c config.Config) {
	component.Restore(f.mode, c.Dashboard.Mode, f.setMode)
	f.idle.Set(c.Dashboard.Idle)
	slog.Info("restored", "what", f.idle.ObjectID, "using", c.Dashboard.Idle)
	f.kiosk.Set(c.Dashboard.Kiosk)
	opts, chosen := boardOptions(c.Dashboard)
	f.boardOpts.Restore(opts)
	f.board.Set(chosen)
	f.back.Set(backLabel(c.Dashboard.ReturnAfter))
	f.tiles.Set(tilesLabel(c.Dashboard.Tiles))
}

// boardsMax and boardBytes cap the dashboard list (listBoards).
const (
	boardsMax  = 100
	boardBytes = 8 << 10
)

// listBoards puts the known dashboards in the list, with the one chosen selected. Home Assistant is
// offered a changed list from its next connection on, which Run asks for (relist): a path chosen
// that it does not list, from dashboard_path or a navigate tap, is a new option, and until then Home
// Assistant would show the select as unknown.
func (f *Feature) listBoards(d config.Dashboard) {
	opts, chosen := boardOptions(d)
	if f.boardOpts.Set(opts) {
		f.mu.Lock()
		f.relist = true
		f.mu.Unlock()
	}
	f.board.Set(chosen)
}

// boardOptions is the list of dashboards to offer, and the one chosen. A chosen path Home Assistant
// no longer lists is kept as a choice of its own, so the list never claims a different one is
// showing.
func boardOptions(d config.Dashboard) (opts []string, chosen string) {
	opts, chosen = []string{automatic}, automatic
	size := len(automatic)
	path := listedPath(d.Path, d.Known)
	for _, b := range d.Known {
		mine := b.Path == path
		// Capped as home's selects are (home.roomFor, #136): all the options go in one message, and one
		// over 64 KiB breaks the device's connection to Home Assistant. The chosen one always stays.
		if !mine && (len(opts) >= boardsMax || size+len(b.Label) > boardBytes) {
			continue
		}
		opts = append(opts, b.Label)
		size += len(b.Label)
		if mine {
			chosen = b.Label
		}
	}
	if d.Path != "" && chosen == automatic {
		opts = append(opts, d.Path)
		chosen = d.Path
	}
	return opts, chosen
}

// listedPath is the path the list has for a view reached by another name, so that going to it adds
// no option: a dashboard of one view is listed by its own path alone ("dash" for "dash/0" or
// "dash/lights"), a view by number is the view of that place in the list ("dash/1" is "dash/lights"
// when that is its second), and a dashboard of several views by its path alone is its first. A path
// the list does not have in any form is itself.
func listedPath(p string, known []config.DashboardChoice) string {
	p = navPath(p)
	if p == "" {
		return p
	}
	base, view, _ := strings.Cut(p, "/")
	var views []string // the dashboard's views as listed, in order
	for _, b := range known {
		switch {
		case b.Path == p:
			return p
		case b.Path == base && view != "":
			return base
		case strings.HasPrefix(b.Path, base+"/"):
			views = append(views, b.Path)
		}
	}
	if len(views) == 0 {
		return p
	}
	if view == "" {
		return views[0]
	}
	if n, err := strconv.Atoi(view); err == nil && n >= 0 && n < len(views) {
		return views[n]
	}
	return p
}

// navPath is a dashboard path as it is kept: no slashes at its ends, and no query or hash, which a
// navigation path may carry ("/dash/lights?edit=1", "dash/lights#popup") and which name no other view.
func navPath(p string) string {
	p, _, _ = strings.Cut(strings.TrimSpace(p), "#")
	p, _, _ = strings.Cut(p, "?")
	return strings.Trim(p, "/")
}

// chooseBoard is a dashboard picked from the list.
func (f *Feature) chooseBoard(label string) {
	d := config.Get().Dashboard
	path, ok := "", label == automatic
	for _, b := range d.Known {
		if b.Label == label {
			path, ok = b.Path, true
		}
	}
	if !ok && label == d.Path {
		path, ok = d.Path, true
	}
	if !ok {
		slog.Warn("unknown dashboard", "value", label)
		return
	}
	if err := config.Set().Dashboard().Path(path); err != nil {
		slog.Error("saving the dashboard failed", "err", err)
		return
	}
	f.board.Set(label)
	slog.Info("dashboard chosen", "path", path)
	f.setMode(f.Mode())
}

// Run keeps two things: sessions nobody is using are closed, and the list of Home Assistant's
// dashboards is kept current, while the dashboard is on, and asked for once while it is off and there
// is nothing cached (wantsList). Home Assistant reads a list's choices once per connection, so a
// changed list is a reconnect, which drops the link to Home Assistant for a moment - so it waits for a
// moment when the device is not in a turn, and comes at most once in relistGap. An unchanged list, the
// usual answer, costs nothing.
func (f *Feature) Run(ctx context.Context) error {
	lists := time.NewTimer(20 * time.Second) // Home Assistant is usually not reachable the moment this starts
	defer lists.Stop()
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	waiting := false // for Home Assistant, said in the log
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			f.closeUnused()
			if f.relistDue(time.Now(), voice.Get().Busy()) {
				component.Reconnect.Emit(struct{}{})
			}
			continue
		case <-f.look:
		case <-lists.C:
		}
		lists.Reset(boardsEvery)
		if !wantsList(f.Mode(), config.Get().Dashboard.Known) {
			continue
		}
		if !hass.Get().Ready() {
			// Said once, not every minute: a device that is never given a token would say it forever.
			if !waiting {
				slog.Info("dashboard: waiting for Home Assistant before listing its dashboards")
				waiting = true
			}
			lists.Reset(time.Minute) // soon, rather than at the next regular look
			continue
		}
		waiting = false
		f.listOnce(ctx)
	}
}

// closeUnused closes a stream or a drawn session the page has not asked for in unused.
//
// Taken out under the lock, so a session the page asked for again in the meantime is a new one and
// is left alone.
func (f *Feature) closeUnused() {
	f.mu.Lock()
	var s *stream
	var d *session
	if f.stream != nil && time.Since(f.streamUsed) > unused {
		s, f.stream = f.stream, nil
	}
	if f.drawn != nil && time.Since(f.drawnUsed) > unused {
		d, f.drawn = f.drawn, nil
		f.recent.empty() // nobody is looking: the memory is the device's again
	}
	f.mu.Unlock()
	if s != nil {
		s.close()
	}
	if d != nil {
		d.close()
	}
}

// listOnce asks Home Assistant for its dashboards, and when the list has changed keeps it and asks
// for the reconnect that shows it.
func (f *Feature) listOnce(ctx context.Context) {
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	boards, err := hass.Get().Boards(cctx)
	cancel()
	if err != nil {
		slog.Info("dashboard: listing Home Assistant's dashboards failed", "err", err)
		return
	}
	known := make([]config.DashboardChoice, 0, len(boards))
	for _, b := range boards {
		known = append(known, config.DashboardChoice{Label: b.Label, Path: b.Path, Streamed: b.Streamed})
	}
	known = append(known, webChoices(config.Get().Dashboard)...)
	if slices.Equal(known, config.Get().Dashboard.Known) {
		return
	}
	if err := config.Set().Dashboard().Known(known); err != nil {
		slog.Error("saving the dashboards failed", "err", err)
		return
	}
	slog.Info("dashboard: Home Assistant's dashboards", "count", len(known))
	f.listBoards(config.Get().Dashboard)
	f.mu.Lock()
	f.relist = true
	f.mu.Unlock()
}

// webChoices is the dashcast server's web pages as dashboards to choose from, asked of the server; a
// server that cannot be asked just now leaves the ones last known, so a moment away does not take a
// chosen page out of the list.
func webChoices(d config.Dashboard) []config.DashboardChoice {
	var out []config.DashboardChoice
	if d.Server == "" {
		return nil
	}
	names, err := Pages(d.Server, d.Key, config.Get().Device.Name)
	if err != nil {
		slog.Info("dashboard: asking the dashboard server for its web pages failed", "err", err)
		for _, k := range d.Known {
			if _, ok := WebPage(k.Path); ok {
				out = append(out, k)
			}
		}
		return out
	}
	for _, n := range names {
		out = append(out, config.DashboardChoice{Label: "Web: " + n, Path: webPath + n, Streamed: true})
	}
	return out
}

// setMode applies a mode: a stream open in the old one is closed, and the page asks again.
func (f *Feature) setMode(m config.DashboardMode) config.DashboardMode {
	m = modeOf(m) // a mode this device does not have, saved by another build, is Off
	if m != config.DashboardOff && m != config.DashboardTessera {
		select { // turned on: the list is wanted now, not at the next look
		case f.look <- struct{}{}:
		default:
		}
	}
	f.Close()
	f.CloseDrawn()
	f.Changed.Emit(struct{}{})
	return m
}

// SetServer keeps where the dashcast server is and its key, and connects to it afresh.
// The address is taken as typed, scheme and path and all, and kept as host:port (NormalizeServer);
// one that is not an address is refused rather than kept to fail quietly later.
func (f *Feature) SetServer(addr, key string) error {
	addr, err := NormalizeServer(addr)
	if err != nil {
		return err
	}
	key = strings.TrimSpace(key)
	if err := config.Set().Dashboard().Server(addr, key); err != nil {
		return err
	}
	slog.Info("dashboard: server set", "address", addr)
	f.setMode(f.Mode()) // reconnect to the new one
	return nil
}

// Mode is how the dashboard is shown, off included. A mode this device does not have - Tessera
// restored or imported on a Spot or a panel - is Off, as setMode settles it and Home Assistant shows it.
func (f *Feature) Mode() config.DashboardMode { return modeOf(config.Get().Dashboard.Mode) }

func modeOf(m config.DashboardMode) config.DashboardMode {
	if !slices.Contains(Modes(), m) {
		return config.DashboardOff
	}
	return m
}

// Idle is whether the dashboard stands in for the clock.
func (f *Feature) Idle() bool {
	return config.Get().Dashboard.Idle && f.Mode() != config.DashboardOff
}

func (f *Feature) Actions() []*esphome.Action {
	return []*esphome.Action{
		{
			// Where the dashcast server is, for the streamed mode. Here rather than a text box
			// because the key is a secret, and an action's arguments are not kept as state.
			Name: "dashboard_server",
			Args: []esphome.Arg{{Name: "address", Type: esphome.ArgString}, {Name: "key", Type: esphome.ArgString}},
			Run: func(c esphome.Call) (any, error) {
				return nil, f.SetServer(c.String("address"), c.String("key"))
			},
		},
		{
			// Which dashboard: "lovelace/0", "dashboard-kitchen/lights"; empty for the default.
			Name: "dashboard_path",
			Args: []esphome.Arg{{Name: "path", Type: esphome.ArgString}},
			Run: func(c esphome.Call) (any, error) {
				p := navPath(c.String("path"))
				if err := config.Set().Dashboard().Path(p); err != nil {
					return nil, err
				}
				slog.Info("dashboard: path set", "path", p)
				f.listBoards(config.Get().Dashboard)
				f.setMode(f.Mode())
				return nil, nil
			},
		},
		{
			// The dashboard up, as a swipe in from the left puts it up, until dashboard_hide or a
			// finger takes it away: an automation that put it up says when it goes.
			Name: "dashboard_show",
			Run: func(esphome.Call) (any, error) {
				if f.Mode() == config.DashboardOff {
					return nil, errors.New("the dashboard is off: choose how it is shown in the Dashboard setting first")
				}
				slog.Info("dashboard: shown by Home Assistant")
				f.Asked.Emit(true)
				return nil, nil
			},
		},
		{
			// Back to the clock. When the dashboard stands in for the clock, the clock stays for
			// a while, as it does when the dashboard is swiped away.
			Name: "dashboard_hide",
			Run: func(esphome.Call) (any, error) {
				slog.Info("dashboard: hidden by Home Assistant")
				f.Asked.Emit(false)
				return nil, nil
			},
		},
	}
}
