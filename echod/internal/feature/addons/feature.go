// Package addons installs, runs, updates and removes add-ons: programs others make for TECHO5, which
// run on the device from userdata (docs/setup.md, Add-ons). It installs only what the signed catalog
// names (feature/addons/catalog), only from the setup page, where the warning is read and the box is
// ticked, and runs each add-on as a user of its own with only the ports it declared opened.
//
// It is the managed way to do what rc.local does by hand, which keeps working as it did: the card lists
// what it can see of hand-installed programs, and changes nothing about them.
//
// Home Assistant gets an update entity for each installed add-on, the way the firmware's shows, and
// can install an update from it only when the update asks for nothing the owner has not agreed to.
package addons

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/peeruid"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
	"github.com/HuskerMinion/techo5/echod/internal/service"
)

func init() {
	if !Here {
		return
	}
	// Last of all, after the daemon is up, as rc.local starts last at boot: an add-on that talks to the
	// device's own services (the Plex add-on drives its DLNA renderer) finds them there.
	component.Register(component.Network, Get(), component.Order(98),
		component.Supervise(service.Restart(10*time.Second, 5*time.Minute)))
}

// Feature is the add-ons as a component of the device: the supervisor, and its Home Assistant
// entities.
type Feature struct {
	*Manager

	mu       sync.Mutex
	entities map[string]*esphome.Update // by add-on id
}

var (
	once   sync.Once
	shared *Feature
)

// Get is the device's add-ons.
func Get() *Feature {
	once.Do(func() {
		f := &Feature{Manager: newManager(Root, realClock{}, runTool), entities: map[string]*esphome.Update{}}
		f.changed = f.publish
		// A loopback controller of the DLNA renderer stands for the owner when it is an installed
		// add-on's: the owner agreed to it, and its warning says add-ons can drive the device.
		peeruid.Trusted = f.InstalledUID
		shared = f
	})
	return shared
}

func (f *Feature) Name() string { return "addons" }

// Supported is whether add-ons run on this device: not on a Panel (Here), and on a Dot only while it
// runs TECHO5's Linux, which has rc.local and the firewall this needs, rather than Fire OS.
func Supported() bool { return Here && !layout.OnAndroid() }

// Run is the supervisor's, on a device that runs add-ons.
func (f *Feature) Run(ctx context.Context) error {
	if !Supported() {
		<-ctx.Done()
		return nil
	}
	return f.Manager.Run(ctx)
}

// objectID is an add-on's update entity: addon_<id>, the id's dashes as underscores.
func objectID(id string) string { return "addon_" + strings.ReplaceAll(id, "-", "_") }

// Entities is an update entity for each installed add-on. Home Assistant reads the list once per
// connection, so an install or a removal asks for a reconnect (publish).
func (f *Feature) Entities() []esphome.Entity {
	if !Supported() {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	installed := f.Installed()
	keep := map[string]bool{}
	out := make([]esphome.Entity, 0, len(installed))
	for _, s := range installed {
		keep[s.ID] = true
		e, ok := f.entities[s.ID]
		if !ok {
			id := s.ID
			e = &esphome.Update{
				Base:      esphome.Base{ObjectID: objectID(id), Name: "Add-on " + s.Name, Icon: "mdi:puzzle"},
				OnCommand: func(cmd esphome.UpdateCommand) { f.command(id, cmd) },
			}
			f.entities[id] = e
		}
		e.Set(entityState(s))
		out = append(out, e)
	}
	for id := range f.entities {
		if !keep[id] {
			delete(f.entities, id)
		}
	}
	return out
}

// publish brings Home Assistant up to date: each entity's state, and when an add-on came or went, the
// entity list, which takes a reconnect.
func (f *Feature) publish(entitiesChanged bool) {
	if entitiesChanged {
		component.Reconnect.Emit(struct{}{})
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.Installed() {
		if e, ok := f.entities[s.ID]; ok {
			e.Set(entityState(s))
		}
	}
}

// entityState is what an add-on's update entity says: the version installed and the newest the catalog
// offers this device, and when that one cannot be installed from Home Assistant, why, in place of the
// release summary.
func entityState(s Status) esphome.UpdateState {
	st := esphome.UpdateState{CurrentVersion: s.Version, LatestVersion: s.Version, Title: s.Name,
		ReleaseURL: s.Repo}
	switch {
	case s.Withdrawn != "":
		st.ReleaseSummary = "No longer listed in TECHO5's add-on catalog: " + s.Withdrawn +
			". Removing it from the device's setup page is suggested."
	case s.Update != nil:
		st.LatestVersion = s.Update.Version
		switch {
		case s.RolledBack == s.Update.Version:
			st.ReleaseSummary = s.RolledBack + " did not stay up on this device, so " + s.Version + " was put back."
		case s.Update.Problem != "":
			st.ReleaseSummary = "This update cannot be installed here: " + s.Update.Problem + "."
		case len(s.Update.More) > 0:
			st.ReleaseSummary = "This update asks for more than the version installed (" + strings.Join(s.Update.More, ", ") +
				"). Install it from the device's setup page, where its warning is shown."
		}
	case s.RolledBack != "":
		st.ReleaseSummary = s.RolledBack + " did not stay up, so " + s.Version + " was put back."
	}
	// An update being installed shows as one, with its download's progress, whichever page asked for
	// it: the card in Home Assistant otherwise sits on Install for the minute of the download and the
	// minute of the trial.
	if s.Working != "" && s.Working != "removing" {
		st.InProgress = true
		if s.Working == "downloading" {
			st.Progress = s.At * 100
		}
	}
	return st
}

// command is Home Assistant asking to check or to install an add-on's update.
func (f *Feature) command(id string, cmd esphome.UpdateCommand) {
	switch cmd {
	case esphome.UpdateCheck:
		safe.Go("add-on catalog check", func() { _ = f.Refresh(context.Background()) })
	case esphome.UpdateInstall:
		safe.Go("add-on update from Home Assistant", func() {
			err := f.Update(context.Background(), id, "", false, true)
			if err != nil {
				slog.Warn("addons: an update asked for from Home Assistant was not installed", "id", id, "err", err)
			}
			f.publish(false)
		})
	}
}
