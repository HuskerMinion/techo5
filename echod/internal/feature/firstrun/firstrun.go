// Package firstrun is what a device asks of whoever plugs it in the first time (docs/first-run-plan.md),
// on every TECHO5 device: the steps, the one it is on, whether it is done, and what each step does to
// the device. The faces are elsewhere: a Show's screen and a Spot's (feature/display), and the setup
// page (feature/setup), which is how a Dot or an Echo 2nd gen is set up, and works for any device.
//
// Each step is saved as it is done, so a restart picks up where it stopped. Nothing here reboots the
// device: renaming restarts the daemon, as it does from the setup page, and first run comes back on the
// same step.
package firstrun

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/api"
	"github.com/HuskerMinion/techo5/echod/internal/feature/firmware"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wifi"
)

// The steps, in order. Hello and Done are the cards at either end; the rest are counted.
const (
	Hello   = ""
	Wifi    = "wifi"
	Zone    = "zone"
	Name    = "name"
	Home    = "home"
	Updates = "updates"
	Sound   = "sound"
	Done    = "done"
)

// steps are the same on every device: each face draws them its own way.
var steps = []string{Hello, Wifi, Zone, Name, Home, Updates, Sound, Done}

// Steps is first run's steps, in order.
func Steps() []string { return steps }

// Rooms are the rooms offered on the name step, as Home Assistant's own areas are usually called;
// Other types one.
var Rooms = []string{"Kitchen", "Living Room", "Bedroom", "Office", "Dining Room", "Bathroom", "Garage", "Other"}

// NameLimit is the setup page's limit on a name (setup/rename.go), and a room's.
const NameLimit = 31

// Pending is whether first run still has the device.
func Pending() bool { return config.Get().FirstRunPending() }

// Step is the step in force: the saved one, or the first when nothing (or nothing known) is.
func Step() string {
	s := config.Get().FirstRun.Step
	for _, k := range steps {
		if k == s {
			return s
		}
	}
	return Hello
}

// After is the step that follows s.
func After(s string) string {
	for i, k := range steps {
		if k == s && i+1 < len(steps) {
			return steps[i+1]
		}
	}
	return Done
}

// Number is a step's place among the counted ones, 1 for Wi-Fi, and how many there are; 0 for the
// cards at either end.
func Number(s string) (n, of int) {
	counted := steps[1 : len(steps)-1]
	for i, k := range counted {
		if k == s {
			return i + 1, len(counted)
		}
	}
	return 0, len(counted)
}

// Go saves step as the one first run is on.
func Go(step string) {
	if err := config.Set().FirstRun().Step(step); err != nil {
		slog.Warn("saving the first-run step failed", "err", err)
	}
	slog.Info("first run", "step", step)
	Changed()
}

// Finish puts first run away for good, on every face at once.
func Finish() {
	if err := config.Set().FirstRun().Finish(); err != nil {
		slog.Warn("saving first run done failed", "err", err)
	}
	unmark()
	slog.Info("first run done")
	Changed()
}

// Settle is what every start does before anything asks whether first run is pending.
//
// A device with the marker a gift install or a reset leaves (reset.go) has first run to do, whatever
// it was before. Otherwise the one look, at the first start of a build with first run, at whether the
// device was set up before it existed: a Show whose welcome card was put away, or a device Home
// Assistant has already had (it has been told a version, config.Update.LastVersion). Either is done, so
// a device already in use never sees first run. Anything else, a device just installed, has it.
//
// First run still to do on a screen with no Wi-Fi network saved starts on the Wi-Fi step. A Dot with
// none gets one over Bluetooth: it offers Improv by itself 45 seconds after starting (feature/improv),
// and its ring breathes orange (led.Splash) while first run waits.
func Settle() {
	defer startOnWifi()
	_, err := os.Stat(resetPath())
	reset := err == nil
	if reset {
		wipe() // a reset for someone else asked for at the last run (reset.go)
	}
	c := config.Get()
	if marked() {
		// Once for each marker: a restart partway through (a rename restarts the daemon) keeps its step.
		if !c.FirstRun.Marked {
			// A gift install over a device that was in use leaves the marker but wipes nothing of the
			// giver's: Home Assistant's access, the logins, the phone book. The reset's wipe does that,
			// once, the first time the marker is seen (a reset has done it just above).
			if !reset {
				wipe()
			}
			if err := config.Set().FirstRun().Marked(); err != nil {
				slog.Warn("saving first run to do failed", "err", err)
			}
			slog.Info("first run: marked to do, from the start")
		}
		return
	}
	if c.FirstRun.Seen {
		return
	}
	done := c.FirstRun.Done || c.Screen.Welcomed || c.Update.LastVersion != ""
	if err := config.Set().FirstRun().Settle(done); err != nil {
		slog.Warn("saving the first-run look failed", "err", err)
		return
	}
	slog.Info("first run", "set up already", done)
}

// listeners are told when the step or done changes, so a face drawn from it redraws.
var (
	lmu       sync.Mutex
	listeners []func()
)

// OnChange adds f to what is told when first run moves.
func OnChange(f func()) {
	lmu.Lock()
	listeners = append(listeners, f)
	lmu.Unlock()
}

// Changed tells every face that first run moved.
func Changed() {
	lmu.Lock()
	ls := append([]func(){}, listeners...)
	lmu.Unlock()
	for _, f := range ls {
		f()
	}
}

// What first run reads from and does to the rest of the device. Variables, for the tests of every face.
var (
	WifiStatus     = func() wifi.Status { return wifi.Current(context.Background()) }
	HomeConnected  = func() bool { return api.Get().Connected() }
	Key            = func() string { return api.Get().Key() }
	AdoptUntil     = func() time.Time { return api.Get().AdoptionOpenUntil() }
	OpenAdoption   = func() (time.Time, error) { return api.Get().OpenAdoption() }
	SetArea        = func(room string) error { return api.Get().SetArea(room) }
	Rename         = func(to string) string { return renamer(to) }
	Chime          = func() { speaker.Sound().Chime(speaker.ToneAnnounce) }
	CheckUpdates   = func() { firmware.Get().Check(context.Background()) }
	Offered        = func() string { return firmware.Get().Offered() }
	AutoInstall    = func() bool { return firmware.Get().AutoInstall() }
	SetAutoInstall = func(on bool) { firmware.Get().SetAutoInstall(on) }
)

// screenless is a device with no screen (screen_dot.go), whose first run is the setup page's.
var screenless = false

// doneByHome finishes first run on a device with no screen once Home Assistant has connected with a
// key: one installed the usual way and added to Home Assistant is set up, whether or not anybody opened
// the setup page, and so is a gift install a Home Assistant has added (it keeps the name from its MAC
// until somebody gives it another). A screen shows its own first run until it is finished there. It
// reports whether it finished it.
func doneByHome() bool {
	if !screenless || !Pending() || !HomeConnected() || Key() == "" {
		return false
	}
	slog.Info("first run: done by Home Assistant, which has the device with a key and a name")
	Finish()
	return true
}

// renamer is the setup page's rename (setup.Rename), handed over by it: it imports this package, so
// this one cannot import it.
var renamer = func(string) string { return "renaming is not available" }

// SetRenamer is the setup page giving first run its rename.
func SetRenamer(f func(to string) string) { renamer = f }

// The updates step, once per run of the daemon: arriving turns updates installing by themselves on,
// since whoever has this device next is unlikely to be watching for releases, and looks for one.
// Nothing installs while first run is pending (firmware's overnight install waits for it).
var (
	updatesArrived atomic.Bool
	updatesChecked atomic.Bool
)

// ArriveAtUpdates does what arriving at the updates step does, the first time it is asked; done is
// called once the check has answered.
func ArriveAtUpdates(done func()) {
	if !updatesArrived.CompareAndSwap(false, true) {
		return
	}
	if !AutoInstall() {
		SetAutoInstall(true)
	}
	background.Add(1)
	go func() {
		defer background.Done()
		CheckUpdates()
		updatesChecked.Store(true)
		if done != nil {
			done()
		}
		Changed()
	}()
}

// UpdatesChecked is whether the updates step's check has answered.
func UpdatesChecked() bool { return updatesChecked.Load() }

// ResetForTests forgets what arriving at the updates step did.
func ResetForTests() {
	background.Wait()
	updatesArrived.Store(false)
	updatesChecked.Store(false)
}

// background is the updates step's look for an update, which a test waits for (ResetForTests) before it
// puts back what the look calls.
var background sync.WaitGroup

// startOnWifi moves a first run at its start to the Wi-Fi step when no network is saved, on a device
// whose first run has one (not a Panel's, whose tablet has the Wi-Fi).
func startOnWifi() {
	if !Pending() || Step() != Hello || !hasStep(Wifi) || !wifi.Available() || len(wifi.Saved()) > 0 {
		return
	}
	Go(Wifi)
}

func hasStep(s string) bool {
	for _, k := range steps {
		if k == s {
			return true
		}
	}
	return false
}
