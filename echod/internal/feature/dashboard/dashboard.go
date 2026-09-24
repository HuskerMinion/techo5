//go:build !dot && !spot

// Package dashboard puts a Home Assistant dashboard on the screen, in one of two ways. Drawn, the
// device reads the dashboard's cards and draws them itself, in its own style: fast, and nothing else
// to run, but only the cards it knows. Streamed, a dashcast server runs a browser for it and sends the
// dashboard as pictures, and the device sends back where it was touched: every card looks exactly as
// it does in Home Assistant, at the cost of a server and a moment's wait on every tap.
//
// This package holds the settings and the stream; the display draws the page.
package dashboard

import (
	"log/slog"
	"strings"
	"sync"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hook"
)

func init() {
	component.Register(component.Device, Get(), component.Order(37))
}

// Feature is the dashboard's settings and, while it is up, its stream.
type Feature struct {
	// Changed fires when there is something new to draw; listeners must not block.
	Changed hook.Hook[struct{}]

	mode *esphome.Select
	idle *esphome.Switch

	mu     sync.Mutex
	stream *stream // while the page is up in streamed mode
}

var (
	once   sync.Once
	shared *Feature
)

func Get() *Feature {
	once.Do(func() {
		f := &Feature{
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
		}
		component.Bind(f.mode, config.DashboardModes(), f.setMode, config.Set().Dashboard().Mode)
		f.idle.OnCommand = func(on bool) {
			f.idle.Set(on)
			if err := config.Set().Dashboard().Idle(on); err != nil {
				slog.Error("saving the dashboard idle setting failed", "err", err)
			}
			f.Changed.Emit(struct{}{})
		}
		shared = f
	})
	return shared
}

func (f *Feature) Name() string { return "dashboard" }

func (f *Feature) Entities() []esphome.Entity { return []esphome.Entity{f.mode, f.idle} }

func (f *Feature) Restore(c config.Config) {
	component.Restore(f.mode, c.Dashboard.Mode, f.setMode)
	f.idle.Set(c.Dashboard.Idle)
	slog.Info("restored", "what", f.idle.ObjectID, "using", c.Dashboard.Idle)
}

// setMode applies a mode: a stream open in the old one is closed, and the page asks again.
func (f *Feature) setMode(m config.DashboardMode) config.DashboardMode {
	f.mu.Lock()
	s := f.stream
	f.stream = nil
	f.mu.Unlock()
	if s != nil {
		s.close()
	}
	f.Changed.Emit(struct{}{})
	return m
}

// Mode is how the dashboard is shown, off included.
func (f *Feature) Mode() config.DashboardMode { return config.Get().Dashboard.Mode }

// Idle is whether the dashboard stands in for the clock.
func (f *Feature) Idle() bool {
	d := config.Get().Dashboard
	return d.Idle && d.Mode != config.DashboardOff
}

func (f *Feature) Actions() []*esphome.Action {
	return []*esphome.Action{
		{
			// Where the dashcast server is, for the streamed mode. Here rather than a text box
			// because the key is a secret, and an action's arguments are not kept as state.
			Name: "dashboard_server",
			Args: []esphome.Arg{{Name: "address", Type: esphome.ArgString}, {Name: "key", Type: esphome.ArgString}},
			Run: func(c esphome.Call) (any, error) {
				addr, key := strings.TrimSpace(c.String("address")), strings.TrimSpace(c.String("key"))
				if err := config.Set().Dashboard().Server(addr, key); err != nil {
					return nil, err
				}
				slog.Info("dashboard: server set", "address", addr)
				f.setMode(f.Mode()) // reconnect to the new one
				return nil, nil
			},
		},
		{
			// Which dashboard: "lovelace/0", "dashboard-kitchen/lights"; empty for the default.
			Name: "dashboard_path",
			Args: []esphome.Arg{{Name: "path", Type: esphome.ArgString}},
			Run: func(c esphome.Call) (any, error) {
				p := strings.Trim(strings.TrimSpace(c.String("path")), "/")
				if err := config.Set().Dashboard().Path(p); err != nil {
					return nil, err
				}
				slog.Info("dashboard: path set", "path", p)
				f.setMode(f.Mode())
				return nil, nil
			},
		},
	}
}
