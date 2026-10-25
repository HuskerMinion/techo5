//go:build !dot && !spot

package tessera

import (
	"log/slog"
	"path/filepath"
	"sync"
	"sync/atomic"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/update"
)

func init() {
	component.Register(component.Device, Get(), component.Order(90))
}

// firmwareClaim is the Tessera firmware version the screen reports. The add-on turns some of what it
// offers on by this number rather than by the hello, and speaks the second protocol only to 0.3.0 and
// later, so a screen that says nothing, or something low, is sent nothing it can use. It is the release
// whose protocol this was written from; what it says it takes beyond that is in the hello (capabilities).
const firmwareClaim = "0.57.0"

// featuresClaim is what the screen says it can do, in the add-on's Screen features words: its backlight dims and
// goes dark, as the add-on takes a screen of no board of its own to, and it draws a camera's picture on the
// camera's tile itself (own_pictures), from Home Assistant with the device's token (display/tessera_camera.go),
// so the add-on lets a layout have camera tiles while it sends the screen no pictures.
const featuresClaim = "dimmable standby own_pictures"

// path is where the layout in force is kept. A variable so a test keeps it somewhere harmless.
var path = filepath.Join(layout.StateDir, "tessera-layout.json")

// restart replaces the daemon with a new one of itself; replaced in tests.
var restart = update.Restart

// Tessera is the switch, the screen's entities and the receiver behind them.
type Tessera struct {
	on *esphome.Switch

	// What the add-on looks for, on a sub-device of their own (component.DeviceTessera): an entity named
	// Tile settings is what makes a device a screen to it, and the sensors say what kind.
	inbox    *esphome.Text
	firmware *esphome.TextSensor
	node     *esphome.TextSensor
	shape    *esphome.TextSensor
	features *esphome.TextSensor
	// gridSel is the grid the screen tells the add-on: the screen's own, or one chosen (choice.go).
	gridSel *esphome.Select

	recv *Receiver

	// enabled is whether the switch was on when the daemon started, which is what the entities, the action
	// and the sub-device follow: Home Assistant reads them once a connection, and a change of switch
	// restarts the daemon to change them.
	enabled atomic.Bool
}

var (
	once   sync.Once
	shared *Tessera
)

// Get is the one.
func Get() *Tessera {
	once.Do(func() { shared = build() })
	return shared
}

func build() *Tessera {
	onDevice := func(objectID, name, icon string) esphome.Base {
		return esphome.Base{
			ObjectID: objectID, Name: name, Icon: icon,
			Category: esphome.CategoryDiagnostic, DeviceID: component.DeviceTessera,
		}
	}
	t := &Tessera{
		on: &esphome.Switch{Base: esphome.Base{
			ObjectID: "tessera_screen", Name: "Tessera screen", Icon: "mdi:view-dashboard-variant",
			Category: esphome.CategoryConfig,
		}},
		// No category: the inbox is where the add-on reads the screen's status, as a screen of its own has it.
		inbox: &esphome.Text{
			Base: esphome.Base{
				ObjectID: "tessera_tile_settings", Name: "Tile settings", Icon: "mdi:inbox",
				DeviceID: component.DeviceTessera,
			},
			MaxLength: 255,
			Mode:      esphome.TextPlain,
		},
		firmware: &esphome.TextSensor{Base: onDevice("tessera_screen_firmware", "Screen firmware", "mdi:chip")},
		node:     &esphome.TextSensor{Base: onDevice("tessera_device_name", "Device name", "mdi:label-outline")},
		shape:    &esphome.TextSensor{Base: onDevice("tessera_screen_layout", "Screen layout", "mdi:grid")},
		features: &esphome.TextSensor{Base: onDevice("tessera_screen_features", "Screen features", "mdi:format-list-checks")},
	}
	// The inbox is the screen's to set: the add-on reads its status there. A value written to it from Home
	// Assistant (the oldest firmware took layouts that way, in pieces) is not taken; the status goes back.
	t.inbox.OnCommand = func(v string) {
		slog.Debug("tessera: a value written to the inbox is not taken", "bytes", len(v))
		t.inbox.Set(t.recv.Status())
	}

	t.recv = NewReceiver(capabilities(gridFor(false, false)), func(l *Layout) error { return Save(path, l) })
	t.recv.changed = func(status string) {
		if t.inbox.Get() != status {
			t.inbox.Set(status)
		}
	}
	t.recv.drawn = func() { Changed.Emit(struct{}{}) }

	t.on.OnCommand = t.set
	t.gridSel = &esphome.Select{Base: esphome.Base{
		ObjectID: "tessera_grid", Name: "Tessera grid", Icon: "mdi:grid-large",
		Category: esphome.CategoryConfig,
	}}
	t.gridSel.OnCommand = t.chooseGrid
	component.Offer(component.Offered{ID: component.DeviceTessera, Suffix: " tessera", Wanted: t.enabled.Load})
	return t
}

// capabilities is what this screen tells the add-on it takes on a grid of g: every size of tile the grid
// has room for, eight pages, and none of the newer options, until something here draws them.
func capabilities(g grid) Capabilities {
	return Capabilities{
		MaxTiles:    64,
		MaxPages:    8,
		MaxBarItems: 6,
		TileSizes:   sizes(g),
		FreePages:   true,
	}
}

func (t *Tessera) Name() string { return "tessera screen" }

// Close writes a change of titles still waiting its turn, so a clean stop keeps the last one.
func (t *Tessera) Close() error {
	t.recv.Flush()
	return nil
}

// Entities is the switch, and the screen's own while it is on.
func (t *Tessera) Entities() []esphome.Entity {
	if !t.enabled.Load() {
		return []esphome.Entity{t.on}
	}
	return []esphome.Entity{t.on, t.gridSel, t.inbox, t.firmware, t.node, t.shape, t.features}
}

// Actions is screen_message while it is on: the add-on hands every message to it, as
// esphome.<node>_screen_message, and reads the answer.
func (t *Tessera) Actions() []*esphome.Action {
	if !t.enabled.Load() {
		return nil
	}
	return []*esphome.Action{{
		Name:    "screen_message",
		Args:    []esphome.Arg{{Name: "message", Type: esphome.ArgString}},
		Answers: true,
		Run: func(c esphome.Call) (any, error) {
			return t.recv.Receive(c.String("message")), nil
		},
	}}
}

func (t *Tessera) Restore(cfg config.Config) {
	on := cfg.Tessera.On
	t.on.Set(on)
	t.enabled.Store(on)
	slog.Info("restored", "what", t.on.ObjectID, "using", on)
	if !on {
		return
	}
	g := chosen(screenGrid(), cfg.Tessera.Grid)
	t.showGrid(screenGrid(), cfg.Tessera.Grid)
	t.recv.takes(capabilities(g))
	t.firmware.Set(firmwareClaim)
	t.features.Set(featuresClaim)
	t.node.Set(layout.NodeName(cfg.Device.Name, mac()))
	t.shape.Set(g.String())
	l, err := Load(path)
	if err != nil {
		slog.Warn("tessera: the kept layout could not be read", "err", err)
	}
	t.recv.Restore(l)
	t.inbox.Set(t.recv.Status())
	current.Store(newScreen(t.recv, g))
	Changed.Emit(struct{}{})
}

// mac is the factory address, which the node name falls back on for a name with nothing a slug keeps.
func mac() string {
	m, err := layout.FactoryMAC()
	if err != nil {
		return ""
	}
	return m
}

// set saves the switch and restarts the daemon, since what the device offers Home Assistant is read only
// when the server starts. A rare change, as the time zone's is, which takes the same way. A switch moved
// to where it already is, or a save that failed, changes nothing.
func (t *Tessera) set(on bool) {
	if on == config.Get().Tessera.On {
		t.on.Set(on)
		return
	}
	if err := config.Set().Tessera().On(on); err != nil {
		slog.Error("saving a setting failed", "setting", t.on.ObjectID, "err", err)
		t.on.Set(config.Get().Tessera.On)
		return
	}
	t.on.Set(on)
	slog.Info("setting changed, restarting", "setting", t.on.ObjectID, "using", on)
	restart("tessera screen")
}
