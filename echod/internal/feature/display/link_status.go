//go:build !dot

package display

import (
	"context"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/firstrun"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wifi"
)

// The screen says when the device has lost Home Assistant, or its Wi-Fi: nothing else on the clock did.
// A tile tapped then flipped and went back by itself, a voice request chimed and came to nothing, and
// the screen looked as it does on a good day. The Show says it in its header's pill (link_header.go),
// the Spot over the time on its clock faces (statusWord).
//
// It is said only once it has lasted (linkDownAfter), so a Home Assistant restarting for an update, or
// a router changing channels, is not announced; and only of a Home Assistant this device has had: one
// that connected since start, or that it holds an access to.

const (
	linkCheckEvery = 10 * time.Second
	linkDownAfter  = 25 * time.Second
)

// What the header says is down: nothing, the Wi-Fi, or Home Assistant alone.
const (
	linkUp     = ""
	linkNoWifi = "wifi"
	linkNoHome = "home"
)

// linkWatch follows whether Home Assistant is connected to the device, and the Wi-Fi while it is not.
type linkWatch struct {
	mu        sync.Mutex
	homeSeen  bool      // Home Assistant has been connected since start
	downSince time.Time // when it was first found not connected; zero while it is
	noWifi    bool      // the Wi-Fi has no address, as of the last look while Home Assistant was away
	shown     string    // what the header said at the last look, to know when it has changed
}

// The looks, variables for the tests. The Wi-Fi is asked only while Home Assistant is away (a connected
// Home Assistant is a working Wi-Fi), and only where the device runs its own (not a tablet's).
var (
	linkHome = func() bool { return firstrun.HomeConnected() }
	linkWifi = func() bool {
		return !wifi.Available() || wifi.Current(context.Background()).Address != ""
	}
	linkHomeKnown = func() bool { return hass.Get().Ready() }
)

// look takes one reading at now, and says whether what the header says has changed since the last one.
func (l *linkWatch) look(now time.Time) bool {
	home := linkHome()
	wifiUp := true
	if !home {
		wifiUp = linkWifi()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if home {
		l.homeSeen, l.downSince, l.noWifi = true, time.Time{}, false
	} else {
		if l.downSince.IsZero() {
			l.downSince = now
		}
		l.noWifi = !wifiUp
	}
	was := l.shown
	l.shown = l.downLocked(now)
	return l.shown != was
}

// down is what the header says is down at now.
func (l *linkWatch) down(now time.Time) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.downLocked(now)
}

func (l *linkWatch) downLocked(now time.Time) string {
	switch {
	case l.downSince.IsZero() || now.Sub(l.downSince) < linkDownAfter:
		return linkUp
	case l.noWifi:
		return linkNoWifi
	case l.homeSeen || linkHomeKnown():
		return linkNoHome
	}
	return linkUp
}

// watchLink keeps the reading fresh, and redraws when what the header says changes.
func (d *Display) watchLink() {
	for {
		time.Sleep(linkCheckEvery)
		if d.link.look(time.Now()) {
			d.wake()
		}
	}
}
