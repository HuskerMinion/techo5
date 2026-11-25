//go:build linux

package bluez

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/godbus/dbus/v5"
)

var authFailed = dbus.Error{Name: "org.bluez.Error.AuthenticationFailed", Body: []any{"Authentication Failed"}}

// A speaker with a fixed PIN other than 0000 — the Gecko in.stream 2 wants 5555 — pairs: each
// refusal after a PIN was asked for is tried again with the next common PIN, and the PINs given are
// the ones the agent answers with.
func TestPairTriesCommonPINs(t *testing.T) {
	old := pinRetryPause
	pinRetryPause = 0
	defer func() { pinRetryPause = old }()

	g := agent{pairing: func() bool { return true }}
	const path = dbus.ObjectPath("/org/bluez/hci0/dev_34_81_F4_00_00_01")
	var given []string
	err := pairWithPINs(context.Background(), path, func() error {
		pin, derr := g.RequestPinCode(path)
		if derr != nil {
			t.Fatalf("PIN refused in pairing mode: %v", derr)
		}
		given = append(given, pin)
		if pin != "5555" {
			return authFailed
		}
		return nil
	})
	if err != nil {
		t.Fatalf("pairing failed: %v", err)
	}
	if want := []string{"0000", "1234", "1111", "8888", "5555"}; !slices.Equal(given, want) {
		t.Errorf("PINs given %v, want %v", given, want)
	}
	if pin, _ := g.RequestPinCode(path); pin != "0000" {
		t.Errorf("after the pairing the next PIN is %s, want 0000 again", pin)
	}
}

// A pairing that failed without a PIN being asked (a number confirmed, or no answer) is not run
// again; nor is any other error; and a speaker that refuses every PIN fails after the last.
func TestPairPINRetryOnlyForPINs(t *testing.T) {
	old := pinRetryPause
	pinRetryPause = 0
	defer func() { pinRetryPause = old }()
	const path = dbus.ObjectPath("/org/bluez/hci0/dev_x")

	runs := 0
	if err := pairWithPINs(context.Background(), path, func() error { runs++; return authFailed }); err == nil || runs != 1 {
		t.Errorf("no PIN asked: err=%v runs=%d, want a failure after one run", err, runs)
	}

	runs = 0
	other := dbus.Error{Name: "org.bluez.Error.ConnectionAttemptFailed", Body: []any{"Page Timeout"}}
	if err := pairWithPINs(context.Background(), path, func() error { runs++; pinFor(path); return other }); err == nil || runs != 1 {
		t.Errorf("other error: err=%v runs=%d, want a failure after one run", err, runs)
	}

	runs = 0
	if err := pairWithPINs(context.Background(), path, func() error { runs++; pinFor(path); return authFailed }); err == nil || runs != len(legacyPINs) {
		t.Errorf("every PIN refused: err=%v runs=%d, want a failure after %d", err, runs, len(legacyPINs))
	}

	already := dbus.Error{Name: "org.bluez.Error.AlreadyExists", Body: []any{"Already Exists"}}
	if err := pairWithPINs(context.Background(), path, func() error { return already }); err != nil {
		t.Errorf("an already paired device is an error: %v", err)
	}
}

func TestBluezError(t *testing.T) {
	if !bluezError(authFailed, "AuthenticationFailed") {
		t.Error("the D-Bus error name was not matched")
	}
	if bluezError(authFailed, "AlreadyExists") || bluezError(nil, "AuthenticationFailed") {
		t.Error("matched the wrong error")
	}
	if !bluezError(errors.New("org.bluez.Error.AuthenticationFailed"), "AuthenticationFailed") {
		t.Error("a plain error carrying the name was not matched")
	}
}
