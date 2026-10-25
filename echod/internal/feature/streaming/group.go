package streaming

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// avahiReadyWithin is how long avahi gets to come up before it is started again.
var avahiReadyWithin = 15 * time.Second

// Seams for the tests: what runs avahi until ctx ends or it stops, whether it is up, and the receivers.
var (
	runAvahi   = runAvahiOnce
	leftovers  = killLeftovers
	avahiUp    = avahiRunning
	runAirPlay = runAirPlayReceiver
	runSpotify = runSpotifyReceiver
)

// runGroup runs avahi and the receivers wanted until ctx ends. The receivers register with avahi once,
// as they start, and neither registers again when avahi comes back: so they start only once avahi
// says it is running, and when avahi stops they are stopped with it and everything starts again. So
// does an avahi that runs on but no longer answers for AirPlay (watchAnswers).
func runGroup(ctx context.Context, name string, airplay, spotify bool) {
	wait := restartFirst
	for ctx.Err() == nil {
		leftovers()
		start := time.Now()
		gctx, cancel := context.WithCancel(ctx)
		avahiDone := make(chan struct{})
		go func() {
			defer close(avahiDone)
			runAvahi(gctx)
		}()
		var wg sync.WaitGroup
		if waitForAvahi(gctx, avahiDone) {
			if airplay {
				wg.Add(1)
				go func() { defer wg.Done(); runAirPlay(gctx, name) }()
			}
			if spotify {
				wg.Add(1)
				go func() { defer wg.Done(); runSpotify(gctx, name) }()
			}
			unanswered := make(chan struct{})
			if airplay {
				wg.Add(1)
				go func() { defer wg.Done(); watchAnswers(gctx, unanswered) }()
			}
			select {
			case <-avahiDone:
				if ctx.Err() == nil {
					slog.Warn("streaming: avahi stopped; starting the receivers again with it")
				}
			case <-unanswered:
			case <-ctx.Done():
			}
		} else if ctx.Err() == nil {
			slog.Warn("streaming: avahi did not come up; trying again")
		}
		cancel()
		wg.Wait()
		<-avahiDone
		if ctx.Err() != nil {
			return
		}
		if time.Since(start) > ranLong {
			wait = restartFirst
		} else {
			wait = min(wait*2, restartMost)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// waitForAvahi is whether avahi came up: it says it is running before it stops, ctx ends, or
// avahiReadyWithin passes.
func waitForAvahi(ctx context.Context, done <-chan struct{}) bool {
	end := time.Now().Add(avahiReadyWithin)
	for time.Now().Before(end) {
		select {
		case <-ctx.Done():
			return false
		case <-done:
			return false
		case <-time.After(250 * time.Millisecond):
		}
		if avahiUp(ctx) {
			return true
		}
	}
	return false
}
