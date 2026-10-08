package api

import (
	"crypto/rand"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// Letting a Home Assistant add the device, for a household whose own Home Assistant has no key for
// it: a device that left the Home Assistant it had (leave.go), or one handed on without its key. For
// adoptWindow the device serves with the all-zeros key, the library's "not provisioned": the first Home
// Assistant to add it sets a key of its own, and the device serves with that key from then on. If none
// does, the window shuts itself with a new random key. The key is never shown anywhere.
//
// Opened only from the setup page, after the press on the device that lets a browser in: somebody is
// standing at the device. Whatever Home Assistant has the device now loses it.

const adoptWindow = 15 * time.Minute

// keyFile is the device's key (a variable for the tests), and adoptPath holds when an open window
// shuts, so a restart inside the window keeps it and one after it shuts it rather than leaving the
// device open.
var (
	keyFile   = layout.KeyPath
	adoptPath = filepath.Join(filepath.Dir(layout.KeyPath), "adopt-until")
)

// OpenAdoption opens the window, and says when it shuts.
func (a *API) OpenAdoption() (time.Time, error) {
	until := time.Now().Add(adoptWindow).Truncate(time.Second)
	if err := os.WriteFile(adoptPath, []byte(until.UTC().Format(time.RFC3339)+"\n"), 0o600); err != nil {
		return time.Time{}, err
	}
	// Removed rather than written as zeros: a missing key is what loadPSK already reads as not
	// provisioned, and it is how every device started before the installer wrote one.
	if err := os.Remove(keyFile); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return time.Time{}, err
	}
	slog.Warn("api: a Home Assistant may add this device now", "until", until)
	a.serveWith(esphome.Unprovisioned())
	a.shutAdoptionAt(until)
	return until, nil
}

// AdoptionOpenUntil is when the open window shuts; zero when none is open.
func (a *API) AdoptionOpenUntil() time.Time {
	until, ok := readAdopt()
	if !ok || !time.Now().Before(until) {
		return time.Time{}
	}
	return until
}

// resumeAdoption is Start's look at a window left open across a restart: still open, it is shut on
// time; past its time, it is shut now and the key to serve with is the new one.
func (a *API) resumeAdoption(psk *esphome.PSK) *esphome.PSK {
	until, ok := readAdopt()
	if !ok {
		return psk
	}
	if !psk.IsZero() {
		_ = os.Remove(adoptPath) // a key was set before the restart: nothing left to shut
		return psk
	}
	if time.Now().Before(until) {
		a.shutAdoptionAt(until)
		return psk
	}
	k, err := shutAdoption()
	if err != nil {
		slog.Error("api: shutting the window a Home Assistant could add this device in", "err", err)
		return psk
	}
	return k
}

func (a *API) shutAdoptionAt(until time.Time) {
	time.AfterFunc(time.Until(until), func() {
		// Under a.mu, as keySet writes the key: a key set by a Home Assistant in the same instant must
		// not be written over by the window's own, or the device would serve one and keep the other.
		a.mu.Lock()
		if _, err := os.Stat(keyFile); err == nil {
			a.mu.Unlock()
			return // a Home Assistant set its key in the window
		}
		// A window opened again since has a later end, and this timer is not its to shut: without this
		// a first window's timer shut the second one early, with a random key.
		if t, ok := readAdopt(); ok && t.Unix() != until.Unix() {
			a.mu.Unlock()
			return
		}
		k, err := shutAdoption()
		if err != nil {
			a.mu.Unlock()
			slog.Error("api: shutting the window a Home Assistant could add this device in", "err", err)
			return
		}
		// Queued under the same lock, so a key a Home Assistant sets on its last moment's connection
		// (keySet, which clears the queue) wins over the window's: the device then serves the key it
		// keeps.
		a.nextPSK = k
		a.mu.Unlock()
		a.Reconnect()
	})
}

// shutAdoption ends a window nobody used: a new random key, which nobody holds.
func shutAdoption() (*esphome.PSK, error) {
	var k esphome.PSK
	if _, err := rand.Read(k[:]); err != nil {
		return nil, err
	}
	if k.IsZero() {
		return nil, errors.New("api: the new key came out as zeros")
	}
	if err := writePSK(keyFile, k); err != nil {
		return nil, err
	}
	_ = os.Remove(adoptPath)
	slog.Info("api: no Home Assistant added this device in time; the window is shut with a new key")
	return &k, nil
}

// keySet is a Home Assistant setting the device's key. It is kept, and is the key in force from now
// on: the server that took it serves with it, and so does every server after (Run makes a new one each
// time it listens). A device that was serving with no key is reconnected onto it: without that, the
// zero key would go on letting anybody in until the next restart.
func (a *API) keySet(k esphome.PSK) error {
	a.mu.Lock()
	if err := writePSK(keyFile, k); err != nil {
		a.mu.Unlock()
		return err
	}
	_ = os.Remove(adoptPath)
	// A key queued by the window's timer a moment ago is not the one kept now: this one is.
	a.nextPSK = nil
	unkeyed := a.unkeyed
	a.useKey(&k)
	a.mu.Unlock()
	if unkeyed {
		slog.Info("api: a Home Assistant added this device and set its key")
		// After the answer has gone back: the reconnect closes the connection it is going out on. It
		// serves the key in force when it runs, not this one: a second key set in the two seconds
		// between is the one saved, and serving this one would leave the device serving one key and
		// keeping another.
		time.AfterFunc(2*time.Second, func() {
			a.mu.Lock()
			now := a.key
			a.mu.Unlock()
			a.serveWith(now)
		})
	}
	return nil
}

func readAdopt() (time.Time, bool) {
	b, err := os.ReadFile(adoptPath)
	if err != nil {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(string(b)))
	return t, err == nil
}

// serveWith has the server take a new key: every client is dropped, and the server listens again with
// it (Run applies it between serving and listening, the one moment nothing reads it).
func (a *API) serveWith(k *esphome.PSK) {
	a.mu.Lock()
	a.nextPSK = k
	a.mu.Unlock()
	a.Reconnect()
}

func (a *API) takeNextPSK() *esphome.PSK {
	a.mu.Lock()
	defer a.mu.Unlock()
	k := a.nextPSK
	a.nextPSK = nil
	if k != nil {
		a.useKey(k)
	}
	return k
}
