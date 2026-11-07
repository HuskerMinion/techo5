//go:build !dot

package display

import (
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/firstrun"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wifi"
)

// First run on a screen (feature/firstrun has the steps, where they are saved and what they do): a
// Show's (firstrun_show.go) and a Spot's (firstrun_spot.go). Each step shows what it is about, and one
// already done (the network joined, Home Assistant connected) still shows, with Next, so a device
// installed the usual way gets through in a few taps.
const (
	frHello   = firstrun.Hello
	frWifi    = firstrun.Wifi
	frZone    = firstrun.Zone
	frName    = firstrun.Name
	frHome    = firstrun.Home
	frUpdates = firstrun.Updates
	frSound   = firstrun.Sound
	frDone    = firstrun.Done
)

// frRooms are the rooms offered on the name step.
var frRooms = firstrun.Rooms

// frHomeLinger is how long "Home Assistant is connected" stays up, when it connected while the step
// waited, before first run goes on by itself.
const frHomeLinger = 3 * time.Second

// frRecheck is how often the Wi-Fi status is read again while first run shows it.
const frRecheck = 3 * time.Second

// frNameLimit is the setup page's limit on a name (setup/rename.go), and a room's.
const frNameLimit = firstrun.NameLimit

// firstRunState is what first run keeps between frames, with d.mu held.
type firstRunState struct {
	wifi     wifi.Status
	wifiAt   time.Time // when wifi was last read; zero is never
	reading  bool      // a read is running
	waited   bool      // the Home Assistant step was seen with nothing connected
	homeAt   time.Time // when Home Assistant connected while the step waited
	chimedOn string    // the step the sound check last played on, so it plays once on arriving
	showKey  bool      // the Home Assistant step shows the key and its QR code

	// The keyboard, typing a name ("name") or a room ("room"); typing is empty while it is away.
	typing           string
	text, typeErr    string
	shifted, symbols bool
	renaming         bool // the name was saved and the daemon is restarting to announce it

	phone bool // the Spot's "On a phone" opened the setup page: its face says where it is
}

// firstRunView is what one frame of first run shows.
type firstRunView struct {
	step     string
	number   int // the step's place, 1 for the first counted step; 0 for the hello and done cards
	of       int
	wifi     wifi.Status
	home     bool // Home Assistant is connected
	homeJust bool // and it connected while the step waited: it says so and goes on
	key      string
	showKey  bool
	adopt    time.Time // when the window for a Home Assistant to add the device shuts; zero when shut
	unkeyed  bool      // no key at all (a gift install, a reset): any Home Assistant can add it, no window
	name     string
	room     string
	renaming bool
	phone    bool

	typing           string
	text, typeErr    string
	shifted, symbols bool

	checked bool   // the update check has answered
	offered string // the version it found, if any
	auto    bool   // updates install by themselves
}

// frButton is one of a step's buttons; the last is the one to press.
type frButton struct {
	label, act string
}

func firstRunStep() string      { return firstrun.Step() }
func stepAfter(s string) string { return firstrun.After(s) }

// firstRunView reads what this frame of first run shows, does what arriving at a step does (the sound,
// the update check) and moves on from Home Assistant by itself once it has connected. The key is only
// ever read here, so it is shown in first run and nowhere else.
func (d *Display) firstRunView(now time.Time) firstRunView {
	v := firstRunView{step: firstRunStep(), name: config.Get().Device.Name, room: config.Get().Home.Area}
	v.number, v.of = firstrun.Number(v.step)
	if v.name == "" {
		v.name = "TECHO5"
	}
	if !firstrun.Pending() {
		return v
	}
	v.home = firstrun.HomeConnected()
	read := false
	d.mu.Lock()
	fr := &d.firstRun
	if !fr.reading && (fr.wifiAt.IsZero() || now.Sub(fr.wifiAt) >= frRecheck) {
		fr.reading, read = true, true
	}
	v.wifi, v.showKey, v.renaming, v.phone = fr.wifi, fr.showKey, fr.renaming, fr.phone
	v.typing, v.text, v.typeErr, v.shifted, v.symbols = fr.typing, fr.text, fr.typeErr, fr.shifted, fr.symbols
	advance, chime := false, false
	switch v.step {
	case frHome:
		switch {
		case !v.home:
			fr.waited, fr.homeAt = true, time.Time{}
		case fr.waited && fr.homeAt.IsZero():
			fr.homeAt = now
		}
		v.homeJust = v.home && fr.waited
		advance = v.homeJust && now.Sub(fr.homeAt) >= frHomeLinger
	case frSound:
		chime = fr.chimedOn != frSound
		fr.chimedOn = frSound
	}
	if v.step != frSound {
		fr.chimedOn = ""
	}
	d.mu.Unlock()
	if v.step == frHome && !v.home {
		v.key, v.adopt = firstrun.Key(), firstrun.AdoptUntil()
		v.unkeyed = v.key == "" && v.adopt.IsZero()
	}
	if v.step == frUpdates {
		firstrun.ArriveAtUpdates(d.wake)
		v.checked, v.offered, v.auto = firstrun.UpdatesChecked(), firstrun.Offered(), firstrun.AutoInstall()
	}
	if read {
		frBackground.Add(1)
		go func() { defer frBackground.Done(); d.firstRunReadWifi() }()
	}
	if chime {
		frBackground.Add(1)
		go func() { defer frBackground.Done(); firstrun.Chime() }()
	}
	if advance {
		d.firstRunGo(stepAfter(frHome))
	}
	return v
}

// frBackground is first run's work off the frame's goroutine, which a test waits for before it puts
// back what that work calls.
var frBackground sync.WaitGroup

// firstRunReadWifi reads the network status off the frame's goroutine: wpa_cli can take a moment.
func (d *Display) firstRunReadWifi() {
	st := firstrun.WifiStatus()
	d.mu.Lock()
	d.firstRun.wifi, d.firstRun.wifiAt, d.firstRun.reading = st, time.Now(), false
	d.mu.Unlock()
	d.wake()
}

// firstRunGo saves step as the one first run is on.
func (d *Display) firstRunGo(step string) {
	firstrun.Go(step)
	d.wake()
}

// firstRunFinish puts first run away for good.
func (d *Display) firstRunFinish() {
	firstrun.Finish()
	d.wake()
}
