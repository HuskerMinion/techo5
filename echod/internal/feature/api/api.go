// Package api presents the Dot to Home Assistant over the ESPHome native API.
//
// It owns no entities. What it serves is whatever the components registered, collected at start-up:
// their entities, and the handlers that answer without one.
package api

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"
	"github.com/ygelfand/go-esphome-device/api"

	"github.com/HuskerMinion/techo5/echod/internal/android/firewall"
	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/bluetooth"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
	"github.com/HuskerMinion/techo5/echod/internal/feature/wakeword"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

func init() {
	// Last: it serves the registry, so nothing should still be coming up when it starts listening.
	//
	// Required, because this is the whole of what the device is to Home Assistant: without it the
	// screen and the ring still work, but the device is offline and says nothing about why. A start
	// that cannot be made to work therefore takes the process down and inittab brings it straight
	// back, which is a boot that tries again rather than a device that has written the API off until
	// somebody pulls the power. See loadPSK for the error this is really about.
	component.Register(component.Network, Get(), component.Order(99))
}

// One of Home Assistant's keepalive intervals; it gives up on us at 4.5 of them.
const writeTimeout = 20 * time.Second

type API struct {
	srv  *esphome.Server
	name string

	reconnect chan struct{}
	announced sync.Once
}

var (
	once   sync.Once
	shared *API
)

func Get() *API {
	once.Do(func() {
		shared = &API{reconnect: make(chan struct{}, 1)}
		component.Reconnect.Listen(func(struct{}) { shared.Reconnect() })
		component.Fire.Listen(func(e component.Event) { shared.fire(e) })
		component.CallService.Listen(func(c component.Call) { shared.call(c) })
	})
	return shared
}

// call asks Home Assistant to run an action — a script, a service — with data. Like fire, nothing
// happens before the server is up; Home Assistant also has to allow it for this device.
func (a *API) call(c component.Call) {
	if a.srv == nil {
		return
	}
	fields := make([]*api.HomeassistantServiceMap, 0, len(c.Data))
	for k, v := range c.Data {
		fields = append(fields, &api.HomeassistantServiceMap{Key: k, Value: v})
	}
	if err := a.srv.Broadcast(&api.HomeassistantActionRequest{Service: c.Service, Data: fields}); err != nil {
		slog.Warn("calling a home assistant action failed", "service", c.Service, "err", err)
	} else {
		slog.Info("home assistant action called", "service", c.Service, "data", c.Data)
	}
}

func (a *API) Name() string { return "api" }

// Start builds the server. Not the constructor, because what the server serves is the registry, and
// the registry is only complete once every package's init has run.
func (a *API) Start(ctx context.Context) error {
	psk, err := waitForPSK(ctx, layout.KeyPath)
	if err != nil {
		return err
	}
	mac, err := layout.FactoryMAC()
	if err != nil {
		return err
	}

	device := config.Get().Device
	ents := esphome.NewEntities()
	if err := ents.Add(component.Default().Entities()...); err != nil {
		return err
	}
	if err := ents.AddActions(component.Default().Actions()...); err != nil {
		return err
	}

	a.name = layout.Slug(device.Name)
	a.srv = &esphome.Server{
		Addr:         device.Addr,
		WriteTimeout: writeTimeout,
		Info: esphome.Info{
			Name:         a.name,
			FriendlyName: device.Name,
			MACAddress:   mac,
			Manufacturer: layout.Manufacturer,
			// The model carries the daemon's own release, since the version field is Home Assistant's
			// ESPHome version: given this daemon's release number there, it reads an ancient ESPHome and
			// raises a repair to update firmware the device does not run (compat.go).
			Model:             layout.Model + " · TECHO5 " + layout.Version,
			Version:           ESPHomeCompat,
			VoiceFeatures:     voice.Features,
			BluetoothFeatures: bluetooth.Get().Features(),

			Devices: subDevices(device.Name),
		},
		PSK:    psk,
		Logger: slog.Default(),
		// Persist a key Home Assistant pushes, or the next connection reverts to the old one.
		OnSetEncryptionKey: func(k esphome.PSK) error { return writePSK(layout.KeyPath, k) },

		OnSubscribed: func() { component.Subscribed.Emit(struct{}{}) },

		// The handlers components answer for themselves rather than through an entity: the voice
		// satellite's pipeline traffic, and the Bluetooth proxy's subscribe and set-mode messages.
		// Describers go first so what they add to the entity list lands before the library's Done.
		Handler: esphome.Chain(append(append(component.Default().Describers(), ents), component.Default().Handlers()...)...),
	}
	return nil
}

// subDevices groups entities onto pages of their own, since Home Assistant puts every entity a device has
// on one page and there are more here than anyone wants to read.
//
// Each name carries the device's own, because Home Assistant shows what it is given verbatim: a bare "Ring"
// or "Assistant 1" is unreadable in a house with several satellites.
func subDevices(name string) []esphome.Device {
	out := []esphome.Device{
		{ID: component.DeviceRing, Name: name + " ring"},
		{ID: component.DeviceMicrophone, Name: name + " microphone"},
		{ID: component.DevicePlayback, Name: name + " playback"},
	}

	for slot := range wakeword.Slots {
		out = append(out, esphome.Device{
			ID:   component.AssistantDevice(slot),
			Name: fmt.Sprintf("%s assistant %d", name, slot+1),
		})
	}
	return out
}

// Run listens until ctx is cancelled, advertising over mDNS so Home Assistant finds the device
// without being told an address.
func (a *API) Run(ctx context.Context) error {
	safe.Go("logs", func() { a.pipeLogs(ctx) })

	// A second way in to the port the install's hook already opens. It costs nothing, and it means a
	// hook that is missing, or older than the device, is not a lockout.
	if err := firewall.Open(firewall.API, layout.Port); err != nil {
		slog.Error("opening the api port failed", "port", layout.Port, "err", err)
	}

	for {
		ln, err := net.Listen("tcp", a.srv.Addr)
		if err != nil {
			return fmt.Errorf("api: listen %s: %w", a.srv.Addr, err)
		}

		a.announced.Do(func() {
			safe.Go("mdns", func() { a.advertise(ctx, ln.Addr().(*net.TCPAddr).Port) })
		})

		serving, stop := context.WithCancel(ctx)
		go func() {
			select {
			case <-a.reconnect:
			case <-serving.Done():
			}
			stop()
		}()

		err = a.srv.Serve(serving, ln)
		stop()

		if err != nil || ctx.Err() != nil {
			return err
		}

		// Between serving and listening again nothing reads Info, which is the only moment it can be
		// changed: a client is told what the device is once, when it connects.
		a.srv.Info.BluetoothFeatures = bluetooth.Get().Features()
		slog.Info("serving again", "bluetooth", a.srv.Info.BluetoothFeatures)
	}
}

// fire puts an event on Home Assistant's bus. Nothing happens before the server is up or while no
// client is subscribed: an event nobody is listening for is not a failure, and the component that
// asked for it has nothing useful to do about one.
func (a *API) fire(e component.Event) {
	if a.srv == nil {
		return
	}
	if err := a.srv.FireEvent(e.Name, e.Data); err != nil {
		slog.Debug("firing an event failed", "event", e.Name, "err", err)
	}
}

// Reconnect drops every client and serves afresh, which is how a change to what the device says it is
// reaches Home Assistant.
func (a *API) Reconnect() {
	select {
	case a.reconnect <- struct{}{}:
	default:
	}
}

// keyRetry is how long Start keeps asking for a key it could not read, and keyWait how long it
// leaves between asks. A read that fails because the flash was busy, or because the filesystem was
// still coming up underneath the daemon, succeeds again within a few seconds; anything that has not
// cleared in half a minute is not going to clear by being asked once more in the same boot. Both are
// variables so a test does not have to sit through them.
var (
	keyRetry = 30 * time.Second
	keyWait  = 2 * time.Second
)

// waitForPSK is loadPSK with the patience a boot needs.
//
// The device is paired and its key is briefly unreadable: that is a moment to wait out, not a reason
// to spend the rest of the boot without Home Assistant. Failing outright here used to do exactly
// that, because this service is started once and a start that fails is not tried again — so a read
// error that would have succeeded two seconds later cost the device its connection until somebody
// power-cycled it, with Home Assistant showing it unavailable and nothing saying why.
//
// What it will not do is give up quietly: when the key still cannot be read the error goes back to
// Start, the service is Required, and the process ends so init can begin the boot afresh. Serving on
// the zero key is never one of the outcomes.
func waitForPSK(ctx context.Context, path string) (*esphome.PSK, error) {
	deadline := time.Now().Add(keyRetry)
	for attempt := 1; ; attempt++ {
		psk, err := loadPSK(path)
		if err == nil {
			if attempt > 1 {
				slog.Warn("the device key read after all", "path", path, "attempts", attempt)
			}
			return psk, nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return nil, err
		}
		// Loudly once, then quietly: the same line fifteen times over half a minute buries whatever
		// else the boot had to say, and the failure that matters is the one Start ends on.
		if attempt == 1 {
			slog.Error("the device key could not be read; trying again rather than serving without one",
				"path", path, "in", keyWait, "err", err)
		} else {
			slog.Debug("the device key still cannot be read", "path", path, "attempt", attempt, "err", err)
		}
		select {
		case <-ctx.Done():
			return nil, err
		case <-time.After(keyWait):
		}
	}
}

// loadPSK reads the key echoctl wrote at install. With no key the device runs unprovisioned —
// Noise with the reserved zero key — so Home Assistant can push a real one, which is what
// `echoctl install --zero-psk` leaves behind. echod never invents a key: one that appeared on
// first boot would be unknown to Home Assistant and nobody would be told it changed.
//
// Only a key that is not there means unprovisioned. A key that is there and cannot be read —
// permissions, a bad block on the flash, a directory where the file should be — is a device that has
// been paired, and coming up on the zero key would drop authentication for everyone on the network
// without anyone asking for it or being told. So every other error goes back to the caller, and
// waitForPSK decides how long to keep asking before that error ends the boot. A device that is
// unreachable is the complaint that gets the key looked at. An open one is not.
func loadPSK(path string) (*esphome.PSK, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return esphome.Unprovisioned(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("api: key at %s: %w", path, err)
	}
	k, err := esphome.ParsePSK(strings.TrimSpace(string(b)))
	if err != nil {
		return nil, fmt.Errorf("api: key at %s: %w", path, err)
	}
	return &k, nil
}

func writePSK(path string, k esphome.PSK) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(k.String()+"\n"), 0o600)
}
