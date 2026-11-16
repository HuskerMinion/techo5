//go:build !dot

package display

import (
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
	// The address from the interface, not through the supplicant (wifi.Current): a supplicant slow to
	// answer is not a Wi-Fi gone.
	linkWifi      = func() bool { return !wifi.Available() || wifi.HasAddress() }
	linkHomeKnown = func() bool { return hass.Get().Ready() }

	// linkLookMu guards the three looks above and is held while one is taken: the display's watch
	// takes them every few seconds, while a test may be swapping them (setLinkLooks) or what they read
	// (firstrun's, in the first run tests).
	linkLookMu sync.Mutex
)

// linkLook takes one of the three looks, under their lock. Each is quick: nothing waits on the network.
func linkLook(which *func() bool) bool {
	linkLookMu.Lock()
	defer linkLookMu.Unlock()
	return (*which)()
}

// setLinkLooks puts in the three looks (nil keeps one) and gives a func that puts the old ones back.
func setLinkLooks(home, wifiUp, homeKnown func() bool) (restore func()) {
	linkLookMu.Lock()
	defer linkLookMu.Unlock()
	h, w, k := linkHome, linkWifi, linkHomeKnown
	if home != nil {
		linkHome = home
	}
	if wifiUp != nil {
		linkWifi = wifiUp
	}
	if homeKnown != nil {
		linkHomeKnown = homeKnown
	}
	return func() {
		linkLookMu.Lock()
		defer linkLookMu.Unlock()
		linkHome, linkWifi, linkHomeKnown = h, w, k
	}
}

// look takes one reading at now, and says whether what the header says has changed since the last one.
func (l *linkWatch) look(now time.Time) bool {
	home := linkLook(&linkHome)
	wifiUp := true
	if !home {
		wifiUp = linkLook(&linkWifi)
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
	case l.homeSeen || homeKnownNow():
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

// homeKnownNow is whether the device holds a Home Assistant it can reach, by the look of the moment.
func homeKnownNow() bool { return linkLook(&linkHomeKnown) }
