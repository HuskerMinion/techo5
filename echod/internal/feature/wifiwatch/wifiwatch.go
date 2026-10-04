// Package wifiwatch brings the device back when its Wi-Fi goes deaf to broadcasts.
//
// On the Echo Dot's MediaTek radio, a WPA2 group-key rekey can leave the station joined, holding its
// lease and able to reach the gateway, while it no longer decrypts anything sent to everyone
// (techo5-dot#3). Other hosts find a device by broadcasting for its address, so once their caches
// expire nothing but the gateway can reach it, Home Assistant included. Reassociating renews every key
// and cures it at once. The driver is Amazon's, so this is the workaround rather than the fix.
//
// It acts only on the one sign that matters: what shows the device can be reached has been gone for a
// few minutes, and the Wi-Fi is still joined with the gateway still answering. Then it reassociates
// once and backs off, so a sign that is gone for its own reasons costs a few seconds of Wi-Fi an hour
// at most, not a loop.
//
// The sign is Home Assistant's connection wherever Home Assistant uses the device. A device it has not
// connected to this boot (one with a direct brain, say) goes by the traffic its network sends to
// everyone instead: ARP, mDNS and router adverts arrive every few seconds on a working network, and stop
// altogether when the radio has gone deaf.
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

	// quiet is how long a device that goes by group traffic may hear none before it counts as gone.
	// The sign then still has to stay gone for gone, so a deaf radio is reassociated after about five
	// minutes. A busy network is not quiet for even 30 seconds.
	quiet = 2 * time.Minute

	// firstWait is how long after a reassociation before another is tried, doubling to mostWait while
	// Home Assistant stays away.
	firstWait = 10 * time.Minute
	mostWait  = time.Hour
)

// What the watcher goes by, as it is named in the log.
const (
	signHA    = "Home Assistant"
	signGroup = "traffic sent to everyone"
)

// Watch is the watcher, with what it asks the world for as functions so a test can be the world.
type Watch struct {
	now          func() time.Time
	haConnected  func() bool
	groupPackets func() (in, out uint64, ok bool)
	joined       func(context.Context) bool
	gatewayUp    func(context.Context) bool
	reassociate  func(context.Context) error
	evidence     func(context.Context) []string
	say          func(msg string, args ...any)

	sign    string    // signHA once Home Assistant has connected this boot, else signGroup once group traffic was heard
	heardAt time.Time // when traffic sent to everyone was last heard
	in, out uint64    // the group counters at the last look
	lostAt  time.Time // when the sign went away; zero while it is here
	notTill time.Time // no reassociation before this
	wait    time.Duration

	// tried is when the last reassociation was, until what came of it has been said.
	tried time.Time
}

// settle is how long after a reassociation the sign has to be back before it is said that it is not:
// a rejoin takes seconds, and Home Assistant's own reconnect up to a minute more.
const settle = 2 * time.Minute

func New() *Watch {
	return &Watch{
		now:          time.Now,
		haConnected:  func() bool { return voice.Get().HomeAssistant() },
		groupPackets: wifi.GroupPackets,
		joined:       func(ctx context.Context) bool { return wifi.Current(ctx).Connected },
		gatewayUp: func(ctx context.Context) bool {
			gw := wifi.Gateway()
			return gw != nil && wifi.Answers(ctx, gw)
		},
		reassociate: wifi.Reassociate,
		evidence:    wifi.Evidence,
		say:         func(msg string, args ...any) { slog.Warn(msg, args...) },
		wait:        firstWait,
	}
}

// record puts the link's evidence in the log, which is what a diagnostics download carries: enough
// to show a rekey was the trigger without anybody having to catch it at the console.
func (w *Watch) record(ctx context.Context, when string) {
	for _, line := range w.evidence(ctx) {
		w.say("wifi: evidence", "when", when, "line", line)
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

// reached reports whether the sign the watcher goes by says the device can still be reached. Once Home
// Assistant has connected this boot it is the sign, as before. Until then the sign is group traffic:
// taking in more group packets than the device sent itself (its own loop back) means the network was
// heard, and that has to have happened within quiet.
func (w *Watch) reached(now time.Time) bool {
	if w.haConnected() {
		w.sign = signHA
		return true
	}
	if w.sign == signHA {
		return false
	}
	in, out, ok := w.groupPackets()
	if !ok {
		return false
	}
	if in-w.in > out-w.out {
		w.heardAt, w.sign = now, signGroup
	}
	w.in, w.out = in, out
	return w.sign == signGroup && now.Sub(w.heardAt) < quiet
}

// look is one round: it decides, and reassociates when every sign says the radio has gone deaf.
func (w *Watch) look(ctx context.Context) {
	now := w.now()
	if w.reached(now) {
		if !w.tried.IsZero() {
			w.say("wifi: recovered: "+w.sign+" is back after reassociating",
				"after", now.Sub(w.tried).Round(time.Second))
			w.tried = time.Time{}
		}
		w.lostAt, w.wait = time.Time{}, firstWait
		return
	}
	if w.sign == "" {
		return // nothing to go by yet this boot: a device still starting, or one with no counters
	}
	if !w.tried.IsZero() && now.Sub(w.tried) >= settle {
		w.say("wifi: still unreachable after reassociating", "after", now.Sub(w.tried).Round(time.Second))
		w.record(ctx, "after")
		w.tried = time.Time{}
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
	w.say("wifi: "+w.sign+" has been gone while the gateway still answers; reassociating",
		"gone", now.Sub(w.lostAt).Round(time.Second))
	w.record(ctx, "before")
	if err := w.reassociate(ctx); err != nil {
		w.say("wifi: reassociating failed", "err", err)
	}
	w.tried = now
	w.notTill = now.Add(w.wait)
	w.wait = min(w.wait*2, mostWait)
}
