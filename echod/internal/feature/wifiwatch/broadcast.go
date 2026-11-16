package wifiwatch

import (
	"context"
	"log/slog"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/wifi"
)

// A radio can also stop taking in what is sent to everyone while Home Assistant stays connected
// (techo5-dot#45). On the Echo Dot's MediaTek radio the firmware keeps a key slot's replay counter
// across a group-key rekey: the router starts the new key's counter at zero, and every broadcast is
// dropped as a replay until the router's count passes the old one, which on a busy network often takes
// longer than the router waits before the next rekey. Home Assistant already has the device's address
// and the device talks to it, so the watcher in wifiwatch.go sees nothing wrong, while everything else
// that has to find the device by broadcasting for its address (SSH, the setup page, the Remote API,
// AirPlay and Sendspin) loses it. A router that hands multicast to each device as its own keeps mDNS
// and its own queries coming, so the multicast watcher in multicast.go is no help either.
//
// IPv4 broadcasts have no such way round, so they are what this watches. It acts only on a network
// heard broadcasting steadily since the last start, with no gap between broadcasts longer than
// bcQuiet, and only once the quiet has gone on longer than bcQuiet and bcGapTimes the longest gap seen.
// Then, with the link joined and the gateway answering, it reassociates, which installs the keys afresh
// and brings the broadcasts back until the next rekey. A reassociation that brought them back leaves
// the wait at bcFirstWait, so a router that rekeys every ten minutes is answered every time; one that
// did not doubles the wait, so a network that has merely gone quiet costs a few seconds of Wi-Fi now
// and then, not a loop. The driver is Amazon's: this is the workaround rather than the fix.

const (
	// bcQuiet is the shortest quiet that counts as broadcasts stopped, and the longest gap between two
	// broadcasts a network may leave and still be watched. Ordinary devices (a TV, a smart plug, the
	// router itself) broadcast for discovery every few seconds to a minute.
	bcQuiet = 3 * time.Minute

	// bcGapTimes is how many times the longest gap seen between broadcasts the quiet has to last.
	bcGapTimes = 4

	// bcHeardLooks is how many looks have to have heard broadcasts, each within bcQuiet of the one
	// before, before a quiet is taken for a deaf radio: a network that broadcasts only now and then is
	// left alone.
	bcHeardLooks = 10

	// bcFirstWait is the least time between two reassociations, doubling to bcMostWait while they do
	// not bring the broadcasts back.
	bcFirstWait = 5 * time.Minute
	bcMostWait  = 6 * time.Hour
)

// broadcast watches for broadcasts that stop while the link is up, with what it asks the world for as
// functions so a test can be the world.
type broadcast struct {
	now          func() time.Time
	groupPackets func() (wifi.Group, bool)
	joined       func(context.Context) bool
	gatewayUp    func(context.Context) bool
	reassociate  func(context.Context) error
	record       func(ctx context.Context, when string)
	say          func(msg string, args ...any)
	// held is whether the other watcher has just reassociated, so this one does not as well.
	held func() bool

	last    wifi.Group    // the counters at the last look
	counted bool          // last holds a reading to compare the next one with
	heardAt time.Time     // when a broadcast from the network was last heard; zero before it ever was
	heard   int           // looks that heard broadcasts within bcQuiet of the one before
	longest time.Duration // the longest of those gaps
	tried   time.Time     // the last reassociation, until what came of it has been said
	notTill time.Time     // no reassociation before this
	wait    time.Duration
}

func newBroadcast() *broadcast {
	return &broadcast{
		now:          time.Now,
		groupPackets: wifi.GroupPackets,
		joined:       func(ctx context.Context) bool { return wifi.Current(ctx).Connected },
		gatewayUp: func(ctx context.Context) bool {
			gw := wifi.Gateway()
			return gw != nil && wifi.Answers(ctx, gw)
		},
		reassociate: rejoiner(wifi.Reassociate, func(ctx context.Context) bool { return wifi.Current(ctx).Connected }, wifi.RefreshRxFilter, func(msg string, args ...any) { slog.Warn(msg, args...) }),
		record:      func(context.Context, string) {},
		say:         func(msg string, args ...any) { slog.Warn(msg, args...) },
		held:        func() bool { return false },
		wait:        bcFirstWait,
	}
}

// forget starts over: the link went down (quiet hours, a router restarting) or joined another network,
// and the network has to be heard broadcasting steadily again before its quiet counts. Without it the
// first look after the link came back found hours of quiet and reassociated a link just up.
func (b *broadcast) forget() {
	if b == nil {
		return
	}
	b.counted, b.heardAt, b.heard, b.longest = false, time.Time{}, 0, 0
}

// limit is how long the broadcasts have to have been quiet before the radio counts as deaf to them.
func (b *broadcast) limit() time.Duration {
	return max(bcQuiet, bcGapTimes*b.longest)
}

// look is one round. A broadcast counts as heard when more came in than the device sent (its own come
// back to it), and only between two readings of the same files.
func (b *broadcast) look(ctx context.Context) {
	now := b.now()
	g, ok := b.groupPackets()
	last := b.last
	b.last = g
	if !ok || g.From&wifi.FromIPv4 == 0 || !b.counted || g.From != last.From ||
		g.BcastIn < last.BcastIn || g.BcastOut < last.BcastOut {
		b.counted = ok && g.From&wifi.FromIPv4 != 0
		return
	}
	if g.BcastIn-last.BcastIn > g.BcastOut-last.BcastOut {
		b.heardNow(now)
		return
	}
	if !b.tried.IsZero() && now.Sub(b.tried) >= settle {
		b.say("wifi: still no broadcasts after reassociating", "after", now.Sub(b.tried).Round(time.Second))
		b.record(ctx, "after")
		b.tried = time.Time{}
	}
	quiet := now.Sub(b.heardAt)
	if b.heardAt.IsZero() || b.heard < bcHeardLooks || quiet < b.limit() || now.Before(b.notTill) || b.held() {
		return
	}
	if !b.joined(ctx) || !b.gatewayUp(ctx) {
		return // a link that is down is the other watchers' business
	}
	b.say("wifi: no broadcasts heard while the link is up; reassociating",
		"quiet", quiet.Round(time.Second), "longest_gap", b.longest.Round(time.Second))
	b.record(ctx, "before")
	if err := b.reassociate(ctx); err != nil {
		b.say("wifi: reassociating failed", "err", err)
	}
	b.tried = now
	b.notTill = now.Add(b.wait)
	b.wait = min(b.wait*2, bcMostWait)
}

// heardNow is a look that heard broadcasts from the network.
func (b *broadcast) heardNow(now time.Time) {
	if !b.tried.IsZero() {
		after := now.Sub(b.tried)
		b.say("wifi: broadcasts are back after reassociating", "after", after.Round(time.Second))
		if after < settle {
			// It was the reassociation that brought them back: the next loss is answered as soon.
			b.wait = bcFirstWait
		}
		b.tried = time.Time{}
	}
	// A gap longer than bcQuiet is a loss, or a network that broadcasts only now and then: it is not
	// what the network's steady gaps are learned from.
	if gap := now.Sub(b.heardAt); b.heardAt.IsZero() || gap < bcQuiet {
		b.heard++
		if !b.heardAt.IsZero() {
			b.longest = max(b.longest, gap)
		}
	}
	b.heardAt = now
}
