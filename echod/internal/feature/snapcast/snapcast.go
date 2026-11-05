// Package snapcast plays the room as a client of a Snapcast server, in step with the house's other
// Snapcast clients.
//
// The wire format is Snapcast's; everything past it is Sendspin's: its clock filter, its decoders and its
// timestamped output, which holds the room to the server's clock against the speaker's own.
package snapcast

import (
	"context"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/sendspin"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

func init() {
	component.Register(component.Device, Get(), component.Order(27))
}

// DefaultPort is Snapserver's stream port.
const DefaultPort = "1704"

// Reconnecting: soon after a connection that lasted, backing off to a minute for a server that is not
// there.
var (
	retryFirst = 2 * time.Second
	retryMost  = time.Minute
	heldLong   = 30 * time.Second
)

// What the state sensor says.
const (
	stateOff        = "off"
	stateNoServer   = "no server set"
	stateConnecting = "connecting"
	stateConnected  = "connected"
	statePlaying    = "playing"
)

// Client is the switch, the server's address and the connection they ask for.
type Client struct {
	enabled *esphome.Switch
	server  *esphome.Text
	state   *esphome.TextSensor

	out *sendspin.Output

	mu      sync.Mutex
	running context.CancelFunc
	wake    chan struct{}
}

var (
	once   sync.Once
	shared *Client
)

func Get() *Client {
	once.Do(func() { shared = build() })
	return shared
}

func build() *Client {
	c := &Client{wake: make(chan struct{}, 1)}

	c.enabled = &esphome.Switch{
		Base: esphome.Base{
			ObjectID: "snapcast",
			Name:     "Snapcast",
			Icon:     "mdi:speaker-multiple",
			Category: esphome.CategoryConfig,
			DeviceID: component.DevicePlayback,
		},
		OnCommand: func(on bool) {
			c.enabled.Set(on)
			if err := config.Set().Snapcast().Enabled(on); err != nil {
				slog.Error("saving a setting failed", "setting", c.enabled.ObjectID, "err", err)
			}
			c.rethink()
		},
	}

	c.server = &esphome.Text{
		Base: esphome.Base{
			ObjectID: "snapcast_server",
			Name:     "Snapcast server",
			Icon:     "mdi:server-network",
			Category: esphome.CategoryConfig,
			DeviceID: component.DevicePlayback,
		},
		MaxLength: 255,
		OnCommand: func(v string) {
			v = strings.TrimSpace(v)
			c.server.Set(v)
			if err := config.Set().Snapcast().Server(v); err != nil {
				slog.Error("saving a setting failed", "setting", c.server.ObjectID, "err", err)
			}
			// A new address is a new connection.
			c.stop()
			c.rethink()
		},
	}

	c.state = &esphome.TextSensor{
		Base: esphome.Base{
			ObjectID: "snapcast_state",
			Name:     "Snapcast state",
			Icon:     "mdi:lan-connect",
			Category: esphome.CategoryDiagnostic,
			DeviceID: component.DevicePlayback,
		},
	}
	c.state.Set(stateOff)
	return c
}

func (c *Client) Name() string { return "snapcast" }

func (c *Client) Entities() []esphome.Entity {
	return []esphome.Entity{c.enabled, c.server, c.state}
}

func (c *Client) Restore(cfg config.Config) {
	c.enabled.Set(cfg.Snapcast.Enabled)
	c.server.Set(cfg.Snapcast.Server)
}

// Run keeps a connection while the switch is on and there is a server to connect to.
func (c *Client) Run(ctx context.Context) error {
	defer c.stop()
	for {
		c.settle(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-c.wake:
		}
	}
}

func (c *Client) rethink() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func (c *Client) settle(parent context.Context) {
	cfg := config.Get().Snapcast
	c.mu.Lock()
	already := c.running != nil
	c.mu.Unlock()

	switch {
	case !cfg.Enabled:
		c.stop()
		c.state.Set(stateOff)
		return
	case cfg.Server == "":
		c.stop()
		c.state.Set(stateNoServer)
		return
	case already:
		return
	}

	ctx, cancel := context.WithCancel(parent)
	c.mu.Lock()
	c.running = cancel
	c.mu.Unlock()

	addr := withPort(cfg.Server)
	if c.out == nil {
		c.out = sendspin.NewOutput()
	}
	safe.Go("snapcast client", func() { c.keep(ctx, addr) })
}

func (c *Client) stop() {
	c.mu.Lock()
	cancel := c.running
	c.running = nil
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// keep connects, and connects again when the connection ends, until ctx does.
func (c *Client) keep(ctx context.Context, addr string) {
	wait := retryFirst
	name := config.Get().Device.Name
	mac := macAddress()
	for ctx.Err() == nil {
		c.state.Set(stateConnecting)
		start := time.Now()
		s := newSession(addr, name, mac, c.out, speaker.Sound().Backgrounds(), c.state.Set)
		err := s.run(ctx)
		if ctx.Err() != nil {
			return
		}
		slog.Warn("snapcast connection ended", "server", addr, "after", time.Since(start).Round(time.Second), "err", err)
		c.state.Set(stateConnecting)

		if time.Since(start) > heldLong {
			wait = retryFirst
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, retryMost)
	}
}

// withPort is the address with Snapserver's port when it was given without one.
func withPort(server string) string {
	if _, _, err := net.SplitHostPort(server); err == nil {
		return server
	}
	return net.JoinHostPort(strings.Trim(server, "[]"), DefaultPort)
}

// macAddress is the Wi-Fi interface's, which is what Snapserver keeps the client under. A device without
// one reads as its name, which is stable too.
func macAddress() string {
	for _, i := range []string{"wlan0", "eth0"} {
		if b, err := os.ReadFile("/sys/class/net/" + i + "/address"); err == nil {
			if mac := strings.TrimSpace(string(b)); mac != "" && mac != "00:00:00:00:00:00" {
				return mac
			}
		}
	}
	name := config.Get().Device.Name
	if name == "" {
		name = "techo5"
	}
	return name
}
