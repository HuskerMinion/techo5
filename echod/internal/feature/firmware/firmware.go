// Package firmware is what the device says about its own version, and the channel it follows to
// learn about newer ones.
//
// Home Assistant decides whether an update is worth offering — it compares the two version strings
// itself — and asks for one with a command. All the device does is say what it is running, say what
// it found, and act when told. There is no list of versions in the protocol and no way for Home
// Assistant to ask for a particular one.
package firmware

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/feedback"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/led"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
	"github.com/HuskerMinion/techo5/echod/internal/update"
)

func init() {
	component.Register(component.Network, Get(), component.Order(10))
}

// Event types for the ways an attempt ends.
const (
	EventInstalled  = "installed"
	EventRolledBack = "rolled_back"
	EventFailed     = "failed"
)

type Firmware struct {
	entity  *esphome.Update
	channel *esphome.Select
	look    *esphome.Button
	status  *esphome.TextSensor
	events  *esphome.Event
	auto    *esphome.Switch

	mu    sync.Mutex
	found update.Manifest
	// appOffered is what a panel's app last said its release server offers, newer than this build, or
	// "": a panel has no release stream of its own (see askApp).
	appOffered string

	announced  sync.Once
	rolledBack string

	// at is how far the running install's download has got, 0 to 1, and failed why the last one
	// did not install: for the setup page, which has no update card to watch.
	at     float32
	failed string

	// phase and target describe the update currently being installed.
	phase  string
	target string
	// progressListener reports updates to an optional on-device display.
	progressListener func(bool, float32, string, string, string)

	// busy counts the Install calls running, which re-read the channel before the download starts.
	busy atomic.Int32
}

// Installing says whether an install is running, and how far its download has got (0 to 1); Failed
// why the last one did not install, empty when it did or none was tried.
func (u *Firmware) Installing() (bool, float32) {
	if u.busy.Load() == 0 && !update.Installing() {
		return false, 0
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	return true, u.at
}

func (u *Firmware) Failed() string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.failed
}

// SetProgressListener registers a listener for the on-device update dialog.
func (u *Firmware) SetProgressListener(listener func(bool, float32, string, string, string)) {
	u.mu.Lock()
	u.progressListener = listener

	active := u.busy.Load() > 0 || update.Installing()
	at, phase, target, message := u.at, u.phase, u.target, u.failed
	u.mu.Unlock()

	// A display starting after an update has begun should see the current state too.
	if listener != nil && (active || message != "") {
		listener(active, at, phase, target, message)
	}
}

var (
	once   sync.Once
	shared *Firmware
)

func Get() *Firmware {
	once.Do(func() { shared = build() })
	return shared
}

func build() *Firmware {
	u := &Firmware{}
	u.buildAuto()

	u.entity = &esphome.Update{
		Base: esphome.Base{
			ObjectID: "firmware",
			Name:     "Firmware",
			Icon:     "mdi:package-up",
		},
		DeviceClass: "firmware",
		OnCommand:   u.command,
	}

	// A stamp Home Assistant cannot rank is the one failure this entity cannot report: the card comes
	// on, and nothing installed here ever clears it, because Home Assistant only asks whether the two
	// strings differ. Nothing can be done about it from the device - the version was decided by
	// whatever built this binary - so say so once, where the next person to wonder will look.
	if err := update.ValidVersion(layout.Version); err != nil {
		slog.Error("this build's version is not one Home Assistant can rank, so it will offer an update that never settles",
			"running", layout.Version, "err", err)
	}

	// Up to date until a check says otherwise, rather than an entity with no versions in it.
	u.publish(update.Manifest{Version: layout.Version})

	u.channel = &esphome.Select{
		Base: esphome.Base{
			ObjectID: "update_channel",
			Name:     "Update channel",
			Icon:     "mdi:source-branch",
			Category: esphome.CategoryDiagnostic,
		},
	}
	component.Bind(u.channel, update.Channels(),
		func(c update.Channel) update.Channel { return c },
		func(c update.Channel) error {
			if err := config.Set().Update().Channel(c.Label()); err != nil {
				return err
			}
			// The card still shows what the channel we just left was serving: nothing fetches the new
			// one on a selection change, so ask as soon as the choice is saved.
			safe.Go("update check", func() { u.Check(context.Background()) })
			return nil
		})

	// Published now, or Home Assistant shows a select with no value until somebody changes it — and a
	// device that has never been asked is on the stable channel, not on nothing.
	u.channel.Set(u.Channel().Label())

	// Home Assistant only asks the device to look when somebody calls homeassistant.update_entity, which
	// is a service call rather than anything on screen. This is that, where it can be found.
	u.look = &esphome.Button{
		Base: esphome.Base{
			ObjectID: "check_for_updates",
			Name:     "Check for updates",
			Icon:     "mdi:cloud-search",
			Category: esphome.CategoryDiagnostic,
		},
		OnPress: func() { safe.Go("update check", func() { u.Check(context.Background()) }) },
	}

	u.status = &esphome.TextSensor{
		Base: esphome.Base{
			ObjectID: "update_status",
			Name:     "Update status",
			Icon:     "mdi:package-variant",
			Category: esphome.CategoryDiagnostic,
		},
	}
	// Empty until an attempt settles: no outcome claimed for one this process did not see (Settled),
	// and not unknown in Home Assistant either.
	u.status.Set("")

	u.events = &esphome.Event{
		Base: esphome.Base{
			ObjectID: "update_outcome",
			Name:     "Update outcome",
			Icon:     "mdi:package-up",
		},
		Types: []string{EventInstalled, EventRolledBack, EventFailed},
	}

	// What the boot hook took out, if anything. Read once: it describes this boot, and reading clears
	// the property.
	u.rolledBack = update.RolledBack()

	// Off the hook's goroutine, which is the connection's read loop. The check goes out with the
	// announce: Home Assistant reads the versions the moment it connects, and nothing else refreshes
	// them while the device is alone, so a unit that has not been asked keeps offering whatever it
	// last heard rather than what the channel is serving now.
	component.Subscribed.Listen(func(struct{}) {
		safe.Go("update announce", u.announce)
		safe.Go("update check", func() { u.Check(context.Background()) })
	})
	return u
}

// announce tells Home Assistant about a build it has not been told about, once there is somebody to
// tell. However the binary got there — an update, an install by hand, a rollback — it either differs
// from what was last reported or it does not, so nothing has to remember to call this and an ordinary
// restart says nothing.
//
// LastVersion moves only once the event has gone out, so a change made while the device was alone is
// reported on the next connection rather than lost.
func (u *Firmware) announce() {
	u.announced.Do(func() {
		if config.Get().Update.LastVersion == layout.Version {
			return
		}

		event, detail := EventInstalled, "installed "+layout.Version
		if u.rolledBack != "" {
			event, detail = EventRolledBack, "rolled back from "+u.rolledBack
		}
		u.Settled(event, detail)

		if err := config.Set().Update().LastVersion(layout.Version); err != nil {
			slog.Error("saving the reported version failed", "err", err)
		}
	})
}

// Settled records how an attempt ended, for a device somebody looks at later and for an automation
// that wants to hear about it now.
//
// The sensor is not saved. It is the outcome of something this process saw, and a restart has not
// seen it — republishing it every boot turns one event into a permanent claim, and one that a manual
// install makes untrue.
func (u *Firmware) Settled(event, status string) {
	u.status.Set(component.Fit(status))
	u.events.Trigger(event)
}

// Offered is the version the last check found to install, or "" when there is nothing newer.
func (u *Firmware) Offered() string {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !update.Published() {
		return u.appOffered
	}
	if u.found.Version == "" || u.found.Version == layout.Version || !u.found.Serves() ||
		!update.Newer(u.found.Version, layout.Version) {
		return ""
	}
	return u.found.Version
}

// SetChannel follows another stream, by its label, as Home Assistant's select would.
func (u *Firmware) SetChannel(label string) { u.channel.OnCommand(label) }

// Channel is the stream this device follows, as last chosen.
func (u *Firmware) Channel() update.Channel {
	saved := config.Get().Update.Channel
	if c, ok := config.ByLabel(update.Channels(), saved); ok {
		return c
	}
	return update.Stable
}

// Check looks for something newer and publishes what it found. Nothing is downloaded and nothing is
// installed: Home Assistant reads the versions and decides whether to offer the update.
//
// A fetch that fails leaves the last answer in place, so a device that briefly cannot reach the channel
// keeps reporting what it knew rather than blanking the card.
func (u *Firmware) Check(ctx context.Context) {
	if !update.Published() {
		askApp(false)
		return
	}
	channel := u.Channel()

	found, err := update.Fetch(ctx, channel)
	if err != nil {
		// Checking before the clock is set is ordinary just after boot, and the next check follows.
		if errors.Is(err, update.ErrClock) {
			slog.Info("update check waits for the clock", "channel", channel.Label())
			return
		}
		slog.Error("checking for an update failed", "channel", channel.Label(), "err", err)
		return
	}

	// Checks can overlap - on connect, on a channel change, on the schedule - and a slow one for the
	// channel just left must not replace the answer for the one chosen since.
	if u.Channel() != channel {
		slog.Info("update check for a channel no longer followed; dropped", "channel", channel.Label())
		return
	}
	u.mu.Lock()
	u.found = found
	u.mu.Unlock()

	slog.Info("update check", "channel", channel.Label(), "running", layout.Version, "offered", found.Version)
	u.publish(found)
}

// command is Home Assistant asking for one of the two things it can ask for. Neither has a reply: what
// the device has to say goes out as entity state.
func (u *Firmware) command(cmd esphome.UpdateCommand) {
	switch cmd {
	case esphome.UpdateCheck:
		safe.Go("update check", func() { u.Check(context.Background()) })

	case esphome.UpdateInstall:
		safe.Go("update install", func() { u.Install(context.Background()) })
	}
}

// Install replaces this binary with what the channel is serving, and asks for the restart that puts it
// in service. Progress goes to Home Assistant as it downloads, since sixteen megabytes over a
// satellite's wifi is long enough to look stuck.
//
// Nothing is installed that was not offered: the version comes from the manifest this device fetched,
// not from Home Assistant, which has no way to name one.
func (u *Firmware) Install(ctx context.Context) {
	if !update.Published() {
		askApp(true) // a panel's app installs a newer build if its release server has one
		return
	}
	u.busy.Add(1)
	defer u.busy.Add(-1)

	found, err := update.Fetch(ctx, u.Channel())
	if err != nil {
		slog.Warn("re-reading the channel failed, using the last check", "err", err)
		u.mu.Lock()
		found = u.found
		u.mu.Unlock()
	} else {
		u.mu.Lock()
		u.found = found
		u.mu.Unlock()
	}

	if found.Version == "" || found.Version == layout.Version || !found.Serves() || !update.Newer(found.Version, layout.Version) {
		slog.Warn("an install was asked for with nothing to install", "running", layout.Version, "offered", found.Version)
		return
	}

	// The ring says it is working for the whole of it, which then hands over to the still frame the
	// restart leaves behind — so the device is never silently busy from the moment somebody presses
	// install to the moment the new binary is up.
	// A second press is turned away before it says anything: setting the progress back to nothing here
	// would put Home Assistant's bar back to empty halfway through the download already running.
	if update.Installing() {
		slog.Info("install already running; the second request was ignored", "version", found.Version)
		return
	}
	working := led.Get().Busy().Start(led.WorkUpdate)
	defer working.Done()

	u.progress(found, 0)
	err = update.Install(ctx, found,
		func(at float32) { u.progress(found, at) },
		func(phase string) { u.phaseReport(found, phase) })

	// Home Assistant learns nothing from the command it sent — the update entity has no way to say an
	// install failed, and the card just goes back to offering it. So the failure has to arrive as state,
	// and on the ring for somebody standing in front of the device.
	// A second press while the first install is still downloading is not a failure and must not be
	// reported as one. The update card gives no sign that anything is happening on a slow link, so
	// pressing it again is the natural thing to do - and the device answered by flashing the failure
	// pattern on the ring and telling Home Assistant the update had failed, while it was in fact
	// downloading perfectly well.
	if errors.Is(err, update.ErrInstalling) {
		slog.Info("install already running; the second request was ignored", "version", found.Version)
		return
	}
	if err != nil {
		slog.Error("installing an update failed", "version", found.Version, "err", err)

		message := err.Error()

		u.mu.Lock()
		u.failed = found.Version + ": " + message
		u.phase, u.target = "failed", found.Version
		listener := u.progressListener
		u.mu.Unlock()

		if listener != nil {
			listener(false, 0, "failed", found.Version, message)
		}

		u.publish(found)
		u.Settled(EventFailed, "installing "+found.Version+" failed: "+message)
		feedback.Failure()
		return
	}

	// update.Install ran the countdown and asked for the restart (or, on a slot device, the reboot)
	// itself, with the install lock held throughout. All that is left is to let the progress
	// listener's last phase stand.
}

// phaseReport is a named step of the install with no progress of its own — the countdown before it
// goes away, and the restart itself. It goes to the on-device dialog, and the version fields are kept
// current for whoever reads them.
func (u *Firmware) phaseReport(found update.Manifest, phase string) {
	u.mu.Lock()
	u.at, u.phase, u.target = 1, phase, found.Version
	listener := u.progressListener
	u.mu.Unlock()

	if listener != nil {
		listener(true, 1, phase, found.Version, "")
	}
}

// progress republishes the state with how far the download has got. The version fields go out with it
// because Home Assistant reads the whole state each time.
func (u *Firmware) progress(found update.Manifest, at float32) {
	if at < 0 {
		at = 0
	}
	if at > 1 {
		at = 1
	}

	phase := "downloading"
	if at >= 1 {
		phase = "installing"
	}

	u.mu.Lock()
	u.at, u.failed = at, ""
	u.phase, u.target = phase, found.Version
	listener := u.progressListener
	u.mu.Unlock()

	state := u.state(found)
	state.InProgress, state.Progress = true, at*100
	u.entity.Set(state)

	if listener != nil {
		listener(true, at, phase, found.Version, "")
	}
}

// publish is the state with nothing happening, which is also how a failed install stops looking like one
// that is still running.
func (u *Firmware) publish(found update.Manifest) { u.entity.Set(u.state(found)) }

// state names the channel rather than the version, which Home Assistant already shows twice on its own.
// The channel is the device's, not the release's, so it is not something a manifest could say.
func (u *Firmware) state(found update.Manifest) esphome.UpdateState {
	// Home Assistant offers an update whenever the two versions differ. A release with nothing this
	// device can install reports the running version as the latest, so there is no card to press.
	latest := found.Version
	if latest != "" && !found.Serves() {
		latest = layout.Version
	}
	return esphome.UpdateState{
		CurrentVersion: layout.Version,
		LatestVersion:  latest,
		Title:          "TECHO5 (" + u.Channel().Label() + " channel)",
		ReleaseSummary: found.Notes,
		ReleaseURL:     found.ReleaseURL,
	}
}

func (u *Firmware) Name() string { return "firmware" }

func (u *Firmware) Entities() []esphome.Entity {
	if !update.Published() {
		// A panel follows no channel of its own (its app updates it): no Stable or Dev to choose.
		return []esphome.Entity{u.entity, u.look, u.status, u.events, u.auto}
	}
	return []esphome.Entity{u.entity, u.channel, u.look, u.status, u.events, u.auto}
}
