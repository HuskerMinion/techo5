package wifiwatch

import (
	"context"
	"time"
)

// After a reassociation the Echo Dot's MediaTek driver leaves most IPv4 multicast (mDNS, router adverts)
// out until its receive filter is sent to the firmware again; toggling promiscuous mode does that
// (techo5-dot#45). So on the Dots every reassociation these watchers make is followed, once the link is
// back up, by a refresh of that filter. Elsewhere it would be harmless (a promiscuous toggle of a
// moment that a driver without the fault ignores) but also pointless, so it is left out.

const (
	// rejoinGrace is how long after asking for a reassociation before the link is looked at: the
	// supplicant answers at once, and the old association is still shown for a moment.
	rejoinGrace = 3 * time.Second
	// rejoinPoll and rejoinMost are how often, and for how long, to wait for the link to be back.
	rejoinPoll = time.Second
	rejoinMost = 30 * time.Second
)

// refreshing wraps a reassociation: when it has been made and the link is back, refresh runs. It
// reports what it did to say. sleep is time.Sleep unless a test is the clock.
func refreshing(reassociate func(context.Context) error, joined func(context.Context) bool,
	refresh func(context.Context) error, say func(string, ...any), sleep func(time.Duration)) func(context.Context) error {
	return func(ctx context.Context) error {
		if err := reassociate(ctx); err != nil {
			return err
		}
		sleep(rejoinGrace)
		for waited := time.Duration(0); !joined(ctx); waited += rejoinPoll {
			if waited >= rejoinMost || ctx.Err() != nil {
				return nil // never came back in time: nothing to refresh, the watchers see to the link
			}
			sleep(rejoinPoll)
		}
		if err := refresh(ctx); err != nil {
			say("wifi: refreshing the receive filter after reassociating failed", "err", err)
			return nil
		}
		say("wifi: link is back after reassociating; refreshed the receive filter (multicast)")
		return nil
	}
}

// rejoiner is wifi.Reassociate, followed on the Dots by the refresh.
func rejoiner(reassociate func(context.Context) error, joined func(context.Context) bool,
	refresh func(context.Context) error, say func(string, ...any)) func(context.Context) error {
	if !refreshAfterReassociate {
		return reassociate
	}
	return refreshing(reassociate, joined, refresh, say, time.Sleep)
}
