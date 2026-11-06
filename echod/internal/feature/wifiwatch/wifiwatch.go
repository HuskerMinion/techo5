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
// once and backs off, and the backoff starts over only once the sign has stayed for an hour, so a sign
// that comes and goes for its own reasons costs a few seconds of Wi-Fi an hour at most, not a loop.
//
// The sign is Home Assistant's connection wherever Home Assistant uses the device. A device it has not
// connected to this boot (one with a direct brain, say) goes by the traffic its network sends to
// everyone instead: mDNS, router adverts, DHCP and the like arrive every few seconds on a busy network,
// router adverts and membership queries every few minutes on a quiet one, and all of it stops when the
// radio has gone deaf. (ARP would be the surest of all, but it is not IP, and these are the IP stack's
// counters.) The counters go by where a packet was addressed, not how it came: a router that sends
// group traffic on to each device as its own (multicast to unicast), or a host on the network that
// does, keeps the network heard through a broken group key, and the watcher then leaves it be, as it
// did before.
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
	// The sign then still has to stay gone for gone, so a deaf radio is reassociated after about 23
	// minutes. A busy network is not quiet for even 30 seconds, and a router sends adverts or
	// membership queries every few minutes; long, because a network that is merely quiet must not be
	// taken for a deaf radio, and before this the device was never brought back at all.
	quiet = 20 * time.Minute

	// firstWait is how long after a reassociation before another is tried, doubling to mostWait while
	// Home Assistant stays away.
	firstWait = 10 * time.Minute
	mostWait  = time.Hour

	// steady is how long the sign has to stay before the wait starts over from firstWait. A sign that
	// comes back for a moment between losses keeps the wait it had, since the watcher cannot tell a
	// sign its reassociation brought back from one that came back by itself. Home Assistant earns it
	// sooner: a server that stays a quarter of an hour is back, and a router that rekeys more often than
	// hourly must not leave the device out of reach for an hour each time. Group traffic, which on a
	// quiet network is heard only now and then, has to stay the hour.
	steady   = time.Hour
	steadyHA = 15 * time.Minute
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
	groupPackets func() (wifi.Group, bool)
	joined       func(context.Context) bool
	gatewayUp    func(context.Context) bool
	reassociate  func(context.Context) error
	evidence     func(context.Context) []string
	say          func(msg string, args ...any)
	network      func(context.Context) (ssid string, joined bool)
	renew        func()

	sign    string     // signHA once Home Assistant has connected this boot, else signGroup once group traffic was heard
	heardAt time.Time  // when traffic sent to everyone was last heard
	group   wifi.Group // the group counters at the last look
	counted bool       // group holds a reading to compare the next one with
	lostAt  time.Time  // when the sign went away; zero while it is here
	hereAt  time.Time  // when the sign came back; zero while it is gone
	notTill time.Time  // no reassociation before this
	wait    time.Duration

	// tried is when the last reassociation was, until what came of it has been said.
	tried time.Time

	ssid string // the network joined at the last look, empty before one was

	mc *multicast // multicast that stops while the link is up (multicast.go)
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
		network: func(ctx context.Context) (string, bool) {
			st := wifi.Current(ctx)
			return st.SSID, st.Connected
		},
		renew: wifi.RenewLease,
		wait:  firstWait,
		mc:    newMulticast(),
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
			w.moved(ctx)
			w.look(ctx)
			if w.mc != nil {
				w.mc.look(ctx)
			}
		}
	}
}

// reached reports whether the sign the watcher goes by says the device can still be reached, and
// known whether it can tell. Once Home Assistant has connected this boot it is the sign, as before.
// Until then the sign is group traffic, heard when a router advert or membership query came in, or
// when more group packets came in than the device sent itself (its own loop back); and that has to
// have happened within quiet.
//
// Group traffic is judged between two looks only. The first reading, and one after the counters could
// not be read, came from other files, or went down, is a new start rather than evidence: totals since
// boot say the network was heard once, not that it still is.
func (w *Watch) reached(now time.Time) (yes, known bool) {
	if w.haConnected() {
		w.sign = signHA
		return true, true
	}
	if w.sign == signHA {
		return false, true
	}
	g, ok := w.groupPackets()
	if !ok {
		w.counted = false
		return false, false
	}
	last := w.group
	w.group = g
	if !w.counted || g.From != last.From || g.In < last.In || g.Out < last.Out || g.Router < last.Router {
		w.counted = true
		return false, false
	}
	if g.Router > last.Router || g.In-last.In > g.Out-last.Out {
		w.heardAt, w.sign = now, signGroup
	}
	return w.sign == signGroup && now.Sub(w.heardAt) < quiet, true
}

// look is one round: it decides, and reassociates when every sign says the radio has gone deaf.
func (w *Watch) look(ctx context.Context) {
	now := w.now()
	yes, known := w.reached(now)
	if !known {
		return // what the counters say cannot be told this round: not a sign that anything is gone
	}
	if yes {
		if !w.tried.IsZero() {
			w.say("wifi: recovered: "+w.sign+" is back after reassociating",
				"after", now.Sub(w.tried).Round(time.Second))
			w.tried = time.Time{}
		}
		w.lostAt = time.Time{}
		if w.hereAt.IsZero() {
			w.hereAt = now
		}
		stay := steady
		if w.sign == signHA {
			stay = steadyHA
		}
		if now.Sub(w.hereAt) >= stay {
			w.wait = firstWait
		}
		return
	}
	w.hereAt = time.Time{}
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
