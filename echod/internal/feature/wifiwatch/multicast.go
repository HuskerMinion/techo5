package wifiwatch

import (
	"context"
	"log/slog"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/wifi"
)

// A radio can also lose only its multicast. After days up, a Dot was seen still joined, answering
// ping and keeping Home Assistant's connection, with broadcasts arriving as ever, while nothing sent
// to a group came in: no mDNS, so AirPlay, Sendspin and ESPHome discovery lost it, and no router
// adverts (TECHO5 #458). The driver's multicast filter was lost. Reassociating did not bring it back;
// taking in all multicast for a moment, and then going back, did at once. That costs no Wi-Fi at all,
// so it is done whenever multicast that was heard this boot has gone quiet for mcQuiet with the link
// otherwise up, and a network that is merely quiet costs nothing but a log line now and then.

const (
	// mcQuiet is how long multicast heard before may be silent. Home Assistant's own discovery and
	// router adverts arrive every few minutes on most networks.
	mcQuiet = 10 * time.Minute

	// mcFirstWait is how long after a refresh before another, doubling to mcMostWait while multicast
	// stays quiet. A refresh that brought multicast back starts over from mcFirstWait.
	mcFirstWait = 10 * time.Minute
	mcMostWait  = 6 * time.Hour
)

// multicast watches for multicast that stops while the link is up, with what it asks the world for
// as functions so a test can be the world.
type multicast struct {
	now          func() time.Time
	groupPackets func() (wifi.Group, bool)
	joined       func(context.Context) bool
	gatewayUp    func(context.Context) bool
	refresh      func(context.Context) error
	say          func(msg string, args ...any)

	last    wifi.Group // the counters at the last look
	counted bool       // last holds a reading to compare the next one with
	heardAt time.Time  // when multicast from the network was last heard; zero before it ever was
	tried   time.Time  // the last refresh, until what came of it has been said
	notTill time.Time  // no refresh before this
	wait    time.Duration
}

func newMulticast() *multicast {
	return &multicast{
		now:          time.Now,
		groupPackets: wifi.GroupPackets,
		joined:       func(ctx context.Context) bool { return wifi.Current(ctx).Connected },
		gatewayUp: func(ctx context.Context) bool {
			gw := wifi.Gateway()
			return gw != nil && wifi.Answers(ctx, gw)
		},
		refresh: wifi.RefreshMulticast,
		say:     func(msg string, args ...any) { slog.Warn(msg, args...) },
		wait:    mcFirstWait,
	}
}

// look is one round. Multicast counts as heard when a router advert or membership query came in, or
// more multicast came in than the device sent (its own comes back to it); as with group traffic above,
// only between two readings of the same files.
func (m *multicast) look(ctx context.Context) {
	now := m.now()
	g, ok := m.groupPackets()
	last := m.last
	m.last = g
	if !ok || !m.counted || g.From != last.From || g.McastIn < last.McastIn || g.McastOut < last.McastOut || g.Router < last.Router {
		m.counted = ok
		return
	}
	if g.Router > last.Router || g.McastIn-last.McastIn > g.McastOut-last.McastOut {
		if !m.tried.IsZero() {
			m.say("wifi: multicast is back after refreshing the filter", "after", now.Sub(m.tried).Round(time.Second))
			m.tried = time.Time{}
			m.wait = mcFirstWait
		}
		m.heardAt = now
		return
	}
	if m.heardAt.IsZero() || now.Sub(m.heardAt) < mcQuiet || now.Before(m.notTill) {
		return
	}
	if !m.joined(ctx) || !m.gatewayUp(ctx) {
		return // a link that is down is the other watchers' business
	}
	m.say("wifi: no multicast heard while the link is up; refreshing the multicast filter",
		"quiet", now.Sub(m.heardAt).Round(time.Second))
	if err := m.refresh(ctx); err != nil {
		m.say("wifi: refreshing the multicast filter failed", "err", err)
	}
	m.tried = now
	m.notTill = now.Add(m.wait)
	m.wait = min(m.wait*2, mcMostWait)
}
