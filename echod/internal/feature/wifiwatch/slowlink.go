package wifiwatch

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wifi"
	"github.com/HuskerMinion/techo5/echod/internal/update"
)

// A link can also degrade without going deaf. After a couple of days up, Dots have been seen joined,
// with a good signal, no loss and the LAN still fast, while everything from the internet crawled at
// 20 kB/s; a Wi-Fi reconnect brought back megabytes a second. Nothing in Home Assistant's connection
// shows it, so the watcher above never sees it.
//
// The sign used here costs nothing while the device is idle: the updater's own download, which comes
// from a CDN that serves megabytes a second and so is a fair test of the link. When the updater says
// a download crawled under its slow rate for a whole window, or stalled outright, and the Wi-Fi is
// still joined with the gateway answering, the radio is reassociated once. The download carries on
// by itself afterward, from where it got to. It is done at most once every slowEvery, so a
// connection that is simply slow costs a few seconds of Wi-Fi now and then, not a loop.

// slowEvery is the least time between two reassociations for a slow link.
const slowEvery = 6 * time.Hour

// slowLink acts on the updater's word that a download is crawling, with what it asks the world for
// as functions so a test can be the world.
type slowLink struct {
	available   func() bool
	now         func() time.Time
	joined      func(context.Context) bool
	gatewayUp   func(context.Context) bool
	reassociate func(context.Context) error
	evidence    func(context.Context) []string
	say         func(msg string, args ...any)
	start       func(what string, f func())

	mu   sync.Mutex
	busy bool      // a look is running
	last time.Time // the last reassociation for a slow link
}

func newSlowLink() *slowLink {
	return &slowLink{
		available: wifi.Available,
		now:       time.Now,
		joined:    func(ctx context.Context) bool { return wifi.Current(ctx).Connected },
		gatewayUp: func(ctx context.Context) bool {
			gw := wifi.Gateway()
			return gw != nil && wifi.Answers(ctx, gw)
		},
		reassociate: wifi.Reassociate,
		evidence:    wifi.Evidence,
		say:         func(msg string, args ...any) { slog.Warn(msg, args...) },
		start:       safe.Go,
	}
}

func init() {
	update.OnSlowDownload(newSlowLink().report)
}

// report is the updater saying a download ran at bytesPerSecond (0 for a stall). It is called on the
// download's goroutine, so the looking is done on another.
func (s *slowLink) report(bytesPerSecond int64) {
	if !s.available() {
		return
	}
	s.mu.Lock()
	if s.busy || (!s.last.IsZero() && s.now().Sub(s.last) < slowEvery) {
		s.mu.Unlock()
		return
	}
	s.busy = true
	s.mu.Unlock()
	s.start("wifi slow link", func() { s.look(bytesPerSecond) })
}

// look reassociates when the link is up but the download says it is not working as one.
func (s *slowLink) look(bytesPerSecond int64) {
	defer func() {
		s.mu.Lock()
		s.busy = false
		s.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Not joined, or a gateway that does not answer, is a network that is down, which a
	// reassociation does not cure.
	if !s.joined(ctx) || !s.gatewayUp(ctx) {
		return
	}
	s.say("wifi: a download from the internet is crawling while the Wi-Fi is up; reassociating",
		"bytes_per_second", bytesPerSecond)
	for _, line := range s.evidence(ctx) {
		s.say("wifi: evidence", "when", "slow link", "line", line)
	}
	if err := s.reassociate(ctx); err != nil {
		s.say("wifi: reassociating failed", "err", err)
	}
	s.mu.Lock()
	s.last = s.now()
	s.mu.Unlock()
}
