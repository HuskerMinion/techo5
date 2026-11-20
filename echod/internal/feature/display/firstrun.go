//go:build !dot

package display

import (
	"fmt"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/firstrun"
	"github.com/HuskerMinion/techo5/echod/internal/lib/fonts"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wifi"
)

// First run on a screen (feature/firstrun has the steps, where they are saved and what they do): a
// Show's (firstrun_show.go) and a Spot's (firstrun_spot.go). Each step shows what it is about, and one
// already done (the network joined, Home Assistant connected) still shows, with Next, so a device
// installed the usual way gets through in a few taps.
const (
	frHello    = firstrun.Hello
	frLanguage = firstrun.Language
	frWifi     = firstrun.Wifi
	frZone     = firstrun.Zone
	frName     = firstrun.Name
	frHome     = firstrun.Home
	frUpdates  = firstrun.Updates
	frSound    = firstrun.Sound
	frDone     = firstrun.Done
)

// frLangs are the languages offered on the language step.
var frLangs = firstrun.Languages

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

	// The Spot's language step goes round the languages on one button, so what it picks is held here
	// and saved on Next (picking): a language passed on the way is neither saved nor fetched a font for.
	picking     bool
	pickLang    string
	pickClock24 bool
	pickedClock bool // the clock was chosen, so a language picked after it keeps it
}

// firstRunView is what one frame of first run shows.
type firstRunView struct {
	step     string
	lang     string // the language step's language chosen (firstrun.LanguageNow)
	clock24  bool   // and its clock
	number   int    // the step's place, 1 for the first counted step; 0 for the hello and done cards
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
	if v.step == frLanguage {
		v.lang, v.clock24 = firstrun.LanguageNow(), clock24.Load()
	}
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
	switch {
	case v.step == frLanguage && fr.picking:
		v.lang, v.clock24 = fr.pickLang, fr.pickClock24
	case v.step != frLanguage:
		// The step was left some other way (the setup page's first run): the pick is let go, so the
		// step shows what was saved if it comes back.
		fr.picking, fr.pickedClock = false, false
	}
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
	if v.step != frHome {
		// Away from the step, what it saw is forgotten: coming back to it (Back, from the screen or the
		// setup page) shows Home Assistant as it is, and does not go on again by itself at once.
		fr.waited, fr.homeAt = false, time.Time{}
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

// firstRunBack goes back to the step before this one, shown afresh: the Home Assistant step's key put
// away.
func (d *Display) firstRunBack(step string) {
	if !firstrun.HasBack(step) {
		return
	}
	d.mu.Lock()
	d.firstRun.showKey = false
	d.mu.Unlock()
	d.firstRunGo(firstrun.Before(step))
}

// frLangLabel is a language's name on its key, in its own words: Japanese in its own script once a
// font here draws it, and romanized until then. That font is fetched over the network only once Japanese
// is the screen's language (feature/cjkfont), and first run asks for the language before the Wi-Fi.
func frLangLabel(l firstrun.Lang) string {
	if l.Code == "ja" && !fonts.OwnDraws('日', "") {
		return "Nihongo"
	}
	return l.Name
}

// frClockLine is the language step's line with the time as the clock chosen (on: 24 hours) shows it.
func frClockLine(now time.Time, on bool) string {
	at := now.Format("3:04 PM")
	if on {
		at = now.Format("15:04")
	}
	return fmt.Sprintf(tr("firstrun_spot_clock"), at)
}

// frClockButton is the language step's button to the other clock than the one chosen (on: 24 hours).
func frClockButton(on bool) frButton {
	if on {
		return frButton{tr("firstrun_use_12_hours"), "clock"}
	}
	return frButton{tr("firstrun_use_24_hours"), "clock"}
}

// firstRunFinish puts first run away for good.
func (d *Display) firstRunFinish() {
	firstrun.Finish()
	d.wake()
}
