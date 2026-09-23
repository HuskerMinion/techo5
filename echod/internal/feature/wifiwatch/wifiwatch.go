// Package wifiwatch brings the device back when its Wi-Fi goes deaf to broadcasts.
//
// On the Echo Dot's MediaTek radio, a WPA2 group-key rekey can leave the station joined, holding its
// lease and able to reach the gateway, while it no longer decrypts anything sent to everyone
// (techo5-dot#3). Other hosts find a device by broadcasting for its address, so once their caches
// expire nothing but the gateway can reach it, Home Assistant included. Reassociating renews every key
// and cures it at once. The driver is Amazon's, so this is the workaround rather than the fix.
//
// It acts only on the one sign that matters: Home Assistant was connected, has been gone for a few
// minutes, and the Wi-Fi is still joined with the gateway still answering. Then it reassociates once
// and backs off, so a Home Assistant that is down for its own reasons costs a few seconds of Wi-Fi an
// hour at most, not a loop.
package wifiwatch

import (
	"context"
	"log/slog"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wifi"
)

func init() {
	component.Register(component.Network, New(), component.Order(95))
}

const (
	// every is how often it looks.
	every = 30 * time.Second

	// gone is how long Home Assistant must have been away. Longer than a restart of the server usually
	// takes, so a Home Assistant update does not cost the device its Wi-Fi.
	gone = 3 * time.Minute

	// firstWait is how long after a reassociation before another is tried, doubling to mostWait while
	// Home Assistant stays away.
	firstWait = 10 * time.Minute
	mostWait  = time.Hour
)

// Watch is the watcher, with what it asks the world for as functions so a test can be the world.
type Watch struct {
	now         func() time.Time
	haConnected func() bool
	joined      func(context.Context) bool
	gatewayUp   func(context.Context) bool
	reassociate func(context.Context) error

	sawHA   bool      // Home Assistant has been connected at some point this boot
	lostAt  time.Time // when it went away; zero while it is here
	notTill time.Time // no reassociation before this
	wait    time.Duration
}

func New() *Watch {
	return &Watch{
		now:         time.Now,
		haConnected: func() bool { return voice.Get().Ready() },
		joined:      func(ctx context.Context) bool { return wifi.Current(ctx).Connected },
		gatewayUp: func(ctx context.Context) bool {
			gw := wifi.Gateway()
			return gw != nil && wifi.Answers(ctx, gw)
		},
		reassociate: wifi.Reassociate,
		wait:        firstWait,
	}
}

func (w *Watch) Name() string { return "wifiwatch" }

// Run looks every little while, on a device that has a supplicant to ask.
func (w *Watch) Run(ctx context.Context) error {
	if !wifi.Available() {
		<-ctx.Done()
		return nil
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			w.look(ctx)
		}
	}
}

// look is one round: it decides, and reassociates when every sign says the radio has gone deaf.
func (w *Watch) look(ctx context.Context) {
	now := w.now()
	if w.haConnected() {
		w.sawHA, w.lostAt, w.wait = true, time.Time{}, firstWait
		return
	}
	if !w.sawHA {
		return // never connected this boot: a device without Home Assistant, or one still starting
	}
	if w.lostAt.IsZero() {
		w.lostAt = now
		return
	}
	if now.Sub(w.lostAt) < gone || now.Before(w.notTill) {
		return
	}
	// Not joined is the supplicant's own business, and a gateway that does not answer is a network
	// that is down: neither is what a reassociation cures.
	if !w.joined(ctx) || !w.gatewayUp(ctx) {
		return
	}
	slog.Warn("wifi: Home Assistant has been gone while the gateway still answers; reassociating",
		"gone", now.Sub(w.lostAt).Round(time.Second))
	if err := w.reassociate(ctx); err != nil {
		slog.Warn("wifi: reassociating failed", "err", err)
	}
	w.notTill = now.Add(w.wait)
	w.wait = min(w.wait*2, mostWait)
}
