// Package wifiband is the Prefer 5 GHz switch: whether the device goes on looking for a stronger 5 GHz
// radio of its network and moving to it, or stays on the band it joined.
//
// The looking and the moving are the boot scripts' network keeper's (t5_wifi_prefer5): every few minutes
// it scans, and moves the link to a strong 5 GHz radio it finds, falling back after thirty seconds, and
// again half an hour later, if the device cannot join it. On a mesh that steers its devices between its
// own units that can cost the link for most of a minute each time, which a stream or a song does not
// ride out. With the switch off the keeper leaves the link where it is; the supplicant still chooses a
// band when it joins, and the network is still free to move a device itself.
//
// The switch is the daemon's and the keeper is a script, so it passes the answer through a file in /run
// (wifi.KeepBandFlag), put back as the daemon starts since /run does not outlast a reboot.
package wifiband

import (
	"log/slog"
	"sync"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wifi"
)

func init() {
	if layout.OnAndroid() || !hasKeeper {
		return // a tablet's Wi-Fi is Android's, and a Dot has no keeper to tell (keeper_dot.go)
	}
	component.Register(component.Network, New(), component.Order(97))
}

// Band is the component, with what it does to the world as functions so a test can be the world.
type Band struct {
	prefer *esphome.Switch

	mu   sync.Mutex
	set  func(keep bool) error
	save func(stay bool) error
}

func New() *Band {
	b := &Band{
		prefer: &esphome.Switch{Base: esphome.Base{
			ObjectID: "wifi_prefer_5ghz", Name: "Prefer 5 GHz", Icon: "mdi:wifi-strength-4",
			Category: esphome.CategoryConfig,
		}},
		set:  wifi.SetKeepBand,
		save: func(stay bool) error { return config.Set().WifiBand().Stay(stay) },
	}
	b.prefer.OnCommand = b.change
	return b
}

func (b *Band) Name() string { return "wifiband" }

func (b *Band) Entities() []esphome.Entity { return []esphome.Entity{b.prefer} }

// Restore puts the saved choice on the switch and the flag.
func (b *Band) Restore(c config.Config) {
	b.apply(c.WifiBand.Stay)
	slog.Info("restored", "what", b.prefer.ObjectID, "on", !c.WifiBand.Stay)
}

// change is the switch moved in Home Assistant: on prefers 5 GHz, off stays on the band joined.
func (b *Band) change(prefer bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.save(!prefer); err != nil {
		slog.Error("saving the 5 GHz preference failed", "err", err)
	}
	b.applyLocked(!prefer)
	slog.Info("setting changed", "setting", b.prefer.ObjectID, "using", prefer)
}

func (b *Band) apply(stay bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.applyLocked(stay)
}

// applyLocked puts the flag where the keeper reads it and shows the switch for what is in force: a flag
// that would not go up leaves the switch on, the keeper's behavior being what it is.
func (b *Band) applyLocked(stay bool) {
	if err := b.set(stay); err != nil {
		slog.Error("setting the 5 GHz flag failed", "stay", stay, "err", err)
		b.prefer.Set(true)
		return
	}
	b.prefer.Set(!stay)
}
