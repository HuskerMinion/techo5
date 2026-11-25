//go:build linux

package bluez

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

// legacyPINs are the fixed PINs speakers and audio receivers too old for Secure Simple Pairing are
// shipped with, most common first. A device without a keyboard cannot be told which one a speaker
// wants, and the speaker cannot show it, so a pairing this side starts tries them in turn: 0000 is
// what most take, and a spa or car system can want 1234 or 5555 (the Gecko in.stream 2 does).
var legacyPINs = []string{"0000", "1234", "1111", "8888", "5555"}

// pinRetryPause lets bluetoothd drop the link a refused PIN left behind before the next try, so the
// retry is a fresh pairing rather than one refused as already in progress.
var pinRetryPause = time.Second

// pins is, per device path, which of legacyPINs the agent answers with next and whether it was asked
// for one during the try under way. A path not listed gets the first, which is also what a pairing
// the other device starts is answered with.
var pins = struct {
	sync.Mutex
	next  map[dbus.ObjectPath]int
	asked map[dbus.ObjectPath]bool
}{next: map[dbus.ObjectPath]int{}, asked: map[dbus.ObjectPath]bool{}}

// pinFor is the PIN the agent gives path now, noting that it was asked.
func pinFor(path dbus.ObjectPath) string {
	pins.Lock()
	defer pins.Unlock()
	pins.asked[path] = true
	if i := pins.next[path]; i < len(legacyPINs) {
		return legacyPINs[i]
	}
	return legacyPINs[0]
}

func pinTry(path dbus.ObjectPath, i int) {
	pins.Lock()
	defer pins.Unlock()
	pins.next[path] = i
	pins.asked[path] = false
}

func pinAsked(path dbus.ObjectPath) bool {
	pins.Lock()
	defer pins.Unlock()
	return pins.asked[path]
}

func pinDone(path dbus.ObjectPath) {
	pins.Lock()
	defer pins.Unlock()
	delete(pins.next, path)
	delete(pins.asked, path)
}

// bluezError is whether err is bluetoothd's org.bluez.Error.<name>. godbus's Error() is the message
// ("Authentication Failed"), not the name, so the name is read off the D-Bus error itself.
func bluezError(err error, name string) bool {
	if err == nil {
		return false
	}
	var de dbus.Error
	if errors.As(err, &de) {
		return de.Name == "org.bluez.Error."+name
	}
	return strings.Contains(err.Error(), name)
}

// pairWithPINs runs pair, and when a legacy-PIN pairing is refused with the PIN given, runs it again
// with the next of legacyPINs. Only a refusal after the agent was asked for a PIN is retried: a
// modern device that confirmed a number and still failed is not helped by another PIN.
func pairWithPINs(ctx context.Context, path dbus.ObjectPath, pair func() error) error {
	defer pinDone(path)
	for i := range legacyPINs {
		pinTry(path, i)
		err := pair()
		if bluezError(err, "AlreadyExists") {
			return nil
		}
		if err == nil || !bluezError(err, "AuthenticationFailed") || !pinAsked(path) || i == len(legacyPINs)-1 {
			return err
		}
		slog.Info("bluetooth: the speaker refused PIN "+legacyPINs[i]+", trying the next common one",
			"device", string(path), "next", legacyPINs[i+1])
		select {
		case <-ctx.Done():
			return err
		case <-time.After(pinRetryPause):
		}
	}
	return nil // not reached: the last try returns
}
