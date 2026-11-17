package api

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wifi"
)

// A device installed with --let-home-assistant-add: the installer wrote its name, Wi-Fi and SSH key
// but no key for Home Assistant, and left adoptMarker instead. On the device's first start with a
// network, the marker becomes a window of firstInstallWindow for a Home Assistant to add it, the same
// window the setup page opens (adopt.go), and is deleted: it is honored once. If nobody adds the
// device in time, the window shuts with a random key, and the setup page opens another.
//
// The installer cannot write the window's end itself: the unit's clock may be wrong until it has a
// network. The window is longer than the setup page's, since a first install often takes a while to
// get to Home Assistant.
//
// From Start until the window opens, the device already serves with the zero key, as any device
// without a key does: anybody on its network who connects first may set one. That stretch lasts as
// long as the device takes to get a network, and is unbounded only on a device that never gets one,
// where nobody can reach it either. The window then bounds the rest.
//
// A device that already has a key ignores the marker and deletes it: the marker never takes a key
// away. The idea of a fresh device handing itself to the first Home Assistant that asks comes from
// Dashboard Assistant (https://github.com/ajfriesen/DashboardAssistant/blob/feffd7de8b6880ceb7521a7b373425c38c845ed0/daemon/pairing.go);
// none of its code is used here.

const firstInstallWindow = 30 * time.Minute

// adoptMarker is the installer's marker (a variable for the tests).
var adoptMarker = filepath.Join(filepath.Dir(layout.KeyPath), "adopt-on-first-start")

// adoptOnline is whether the device has an address to be reached at, and adoptPoll how often that is
// asked while it has none (variables for the tests).
var (
	adoptOnline = func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return wifi.Current(ctx).Address != ""
	}
	adoptPoll = 5 * time.Second
)

// adoptOnFirstStart is Start's look at the marker, after resumeAdoption and firstStart.
func (a *API) adoptOnFirstStart(ctx context.Context, psk *esphome.PSK) {
	if _, err := os.Stat(adoptMarker); err != nil {
		return
	}
	if !psk.IsZero() {
		slog.Info("api: this device has a key; the installer's marker to let a Home Assistant add it is ignored")
		dropAdoptMarker()
		return
	}
	done := make(chan struct{})
	a.firstWindow = done
	safe.Go("api first window", func() {
		defer close(done)
		for !adoptOnline() {
			select {
			case <-ctx.Done():
				return // the marker stays for the next start
			case <-time.After(adoptPoll):
			}
		}
		if _, err := a.openFirstWindow(); err != nil {
			slog.Error("api: opening the window a Home Assistant could add this new device in", "err", err)
		}
	})
}

// openFirstWindow turns the marker into the window, and says when it shuts; zero when the marker is
// gone or a key or a window is there by now. Under a.mu, as keySet writes the key: a key a Home
// Assistant set while the device waited for its network is never left with a window open behind it.
func (a *API) openFirstWindow() (time.Time, error) {
	a.mu.Lock()
	if _, err := os.Stat(adoptMarker); err != nil {
		a.mu.Unlock()
		return time.Time{}, nil // honored already, by a start before this one
	}
	if _, err := os.Stat(keyFile); !errors.Is(err, fs.ErrNotExist) {
		a.mu.Unlock()
		dropAdoptMarker()
		return time.Time{}, nil // a key, set since
	}
	if _, ok := readAdopt(); ok {
		a.mu.Unlock()
		dropAdoptMarker()
		return time.Time{}, nil // a window open already (resumeAdoption, or the setup page's)
	}
	until, err := writeAdoptUntil(firstInstallWindow)
	if err != nil {
		a.mu.Unlock()
		return time.Time{}, err
	}
	a.mu.Unlock()
	dropAdoptMarker()
	slog.Warn("api: new device, a Home Assistant may add it now", "until", until)
	a.shutAdoptionAt(until)
	return until, nil
}

func dropAdoptMarker() {
	if err := os.Remove(adoptMarker); err != nil && !errors.Is(err, fs.ErrNotExist) {
		slog.Warn("api: removing the installer's marker", "err", err)
	}
}
