package setup

import (
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// fakeSecureConn is the settings kept in the config as btaudio keeps them, with no loop behind them.
type fakeSecureConn struct{ here, audio bool }

func (f fakeSecureConn) SecureConnectionsHere() bool { return f.here }
func (f fakeSecureConn) SecureConnections() bool     { return !config.Get().Bluetooth.SecureConnectionsOff }
func (f fakeSecureConn) SetSecureConnections(on bool) error {
	return config.Set().Bluetooth().SecureConnections(on)
}
func (f fakeSecureConn) AudioHere() bool  { return f.audio }
func (f fakeSecureConn) SetsVolume() bool { return !config.Get().Speaker.NoBluetoothVolume }
func (f fakeSecureConn) SetSetsVolume(on bool) error {
	return config.Set().Speaker().NoBluetoothVolume(!on)
}

func useSecureConn(t *testing.T, here bool) { useBluetooth(t, here, false) }

func useBluetooth(t *testing.T, here, audio bool) {
	was := secureConns
	secureConns = func() secureConnSetting { return fakeSecureConn{here, audio} }
	t.Cleanup(func() { secureConns = was })
}

// The Connections tab's Bluetooth section shows Secure Connections as it is, says what turning it off
// and changing it cost, and saves it both ways (#183).
func TestTheBluetoothSection(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	page := func() string {
		w := httptest.NewRecorder()
		bluetoothSection(w, "tok")
		return w.Body.String()
	}
	// A panel leaves the radio to Android, and offers no section at all.
	useSecureConn(t, false)
	if p := page(); p != "" {
		t.Errorf("a device without the setting showed a Bluetooth section: %s", p)
	}
	if msg := saveBluetooth(form(url.Values{})); msg == "" {
		t.Error("a device without the setting saved it")
	}

	useSecureConn(t, true)
	p := page()
	if !strings.Contains(p, `name="sc" value="yes" style="width:auto" checked`) ||
		!strings.Contains(p, "drops for a moment") || !strings.Contains(p, `value="bluetooth"`) {
		t.Fatalf("on by default, with what it costs: %s", p)
	}

	if msg := saveBluetooth(form(url.Values{})); msg != "" || !config.Get().Bluetooth.SecureConnectionsOff {
		t.Fatalf("unticked: %q, off %v", msg, config.Get().Bluetooth.SecureConnectionsOff)
	}
	if strings.Contains(page(), " checked") {
		t.Error("off, the box is still ticked")
	}
	if msg := saveBluetooth(form(url.Values{"sc": {"yes"}})); msg != "" || config.Get().Bluetooth.SecureConnectionsOff {
		t.Errorf("ticked: %q, off %v", msg, config.Get().Bluetooth.SecureConnectionsOff)
	}
}

// Bluetooth sets the volume is on the Bluetooth section of every device a phone plays to, ticked by
// default, and saves both ways without touching Secure Connections (#245). A device that plays what a
// phone sends but has no Secure Connections setting (athena on mainline) shows only it.
func TestTheBluetoothVolumeBox(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	page := func() string {
		w := httptest.NewRecorder()
		bluetoothSection(w, "tok")
		return w.Body.String()
	}
	useBluetooth(t, false, true)
	p := page()
	if !strings.Contains(p, `name="setsvolume" value="yes" style="width:auto" checked`) || strings.Contains(p, `name="sc"`) {
		t.Fatalf("only the volume box, ticked by default: %s", p)
	}

	useBluetooth(t, true, true)
	if msg := saveBluetooth(form(url.Values{"sc": {"yes"}})); msg != "" || !config.Get().Speaker.NoBluetoothVolume {
		t.Fatalf("unticked: %q, off %v", msg, config.Get().Speaker.NoBluetoothVolume)
	}
	if config.Get().Bluetooth.SecureConnectionsOff {
		t.Error("Secure Connections went off with the volume box")
	}
	if p := page(); strings.Contains(p, `name="setsvolume" value="yes" style="width:auto" checked`) ||
		!strings.Contains(p, `name="sc" value="yes" style="width:auto" checked`) {
		t.Errorf("off, the volume box is still ticked, or Secure Connections is not: %s", p)
	}
	if msg := saveBluetooth(form(url.Values{"sc": {"yes"}, "setsvolume": {"yes"}})); msg != "" || config.Get().Speaker.NoBluetoothVolume {
		t.Errorf("ticked: %q, off %v", msg, config.Get().Speaker.NoBluetoothVolume)
	}
}
