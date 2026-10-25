package setup

import (
	"log/slog"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	"github.com/HuskerMinion/techo5/echod/internal/feature/ring"
	"github.com/HuskerMinion/techo5/echod/internal/feature/talkback"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// Home Assistant asks what wake words the device has only as it connects, so a wake word added or
// removed on the page reaches its list by dropping the connection and letting it come back. Dropped
// in the middle of something, that cuts it off: a request being answered, a call, a talk through a
// camera, an alarm ringing or an announcement being read. So the reconnect waits until the device
// has been idle for a moment, and a few wake words sent in a row are one reconnect, not one each.
//
// It does not wait forever for a moment that is quiet enough. After reconnectPatience the first idle
// look will do, settled or not; a device kept busy with no idle moment at all simply keeps waiting,
// since there is nothing then that a reconnect would not cut off.

const (
	// reconnectLook is how often the device is asked whether it is idle.
	reconnectLook = time.Second

	// reconnectSettle is how long it has to have stayed idle: the gap between a reply and the
	// follow-up it listens for, or between two rounds of an alarm, is not a quiet moment.
	reconnectSettle = 3 * time.Second

	// reconnectPatience is how long a settled moment is waited for before any idle look will do.
	reconnectPatience = 5 * time.Minute

	// reconnectGap is the least time between two of these reconnects. Each one drops Home Assistant's
	// connection, and somebody sending wake words one after another would otherwise have it dropped
	// every few seconds; the later ones wait and go as one.
	reconnectGap = 30 * time.Second
)

// deviceBusy is whether reconnecting now would cut something off. Music playing is not counted: it
// does not come over the connection that is dropped, and it can play for hours.
func deviceBusy() bool {
	return voice.Get().Busy() || phone.Get().Busy() || talkback.Get().Busy() ||
		ring.IsSounding() || speaker.Sound().Busy()
}

// idleReconnect is one reconnect waiting for the device to be idle.
type idleReconnect struct {
	busy                       func() bool
	emit                       func()
	look, settle, patient, gap time.Duration

	mu      sync.Mutex
	pending bool
	last    time.Time // when the last reconnect went
}

// whenIdle is the device's; a test has one of its own.
var whenIdle = &idleReconnect{
	busy:    deviceBusy,
	emit:    func() { component.Reconnect.Emit(struct{}{}) },
	look:    reconnectLook,
	settle:  reconnectSettle,
	patient: reconnectPatience,
	gap:     reconnectGap,
}

// ReconnectWhenIdle has Home Assistant reconnect once the device is idle, for something else it reads only
// as it connects: the screen's light standing apart from the room's lights (display, screen_room.go). It
// joins a reconnect already waiting for a wake word, and keeps the same gap between them.
func ReconnectWhenIdle() { whenIdle.ask() }

// ask has Home Assistant reconnect once the device is idle. Asking while a reconnect is already
// waiting joins it.
func (r *idleReconnect) ask() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pending {
		return
	}
	r.pending = true
	safe.Go("setup page reconnect", r.wait)
}

func (r *idleReconnect) wait() {
	start := time.Now()
	r.mu.Lock()
	earliest := r.last.Add(r.gap)
	r.mu.Unlock()
	var quietSince time.Time
	for {
		time.Sleep(r.look)
		now := time.Now()
		if now.Before(earliest) {
			continue
		}
		if r.busy() {
			quietSince = time.Time{}
			continue
		}
		if quietSince.IsZero() {
			quietSince = now
		}
		if now.Sub(quietSince) >= r.settle || now.Sub(start) >= r.patient {
			break
		}
	}
	// Cleared before the reconnect, so a wake word sent from here on gets one of its own: this one
	// may already be too late for it.
	r.mu.Lock()
	r.pending = false
	r.last = time.Now()
	r.mu.Unlock()
	slog.Info("Home Assistant reconnects to see what changed", "waited", time.Since(start).Round(time.Second))
	r.emit()
}
