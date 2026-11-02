//go:build linux

package bluez

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"github.com/godbus/dbus/v5/prop"
)

type fakeDevice struct {
	mu    sync.Mutex
	calls []string
	hang  bool
}

func (d *fakeDevice) note(s string)              { d.mu.Lock(); d.calls = append(d.calls, s); d.mu.Unlock() }
func (d *fakeDevice) Disconnect() *dbus.Error    { d.note("Disconnect"); return nil }
func (d *fakeDevice) CancelPairing() *dbus.Error { d.note("CancelPairing"); return nil }
func (d *fakeDevice) Pair() *dbus.Error {
	d.note("Pair")
	if d.hang {
		time.Sleep(2 * time.Second)
	}
	return nil
}
func (d *fakeDevice) has(s string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, c := range d.calls {
		if c == s {
			return true
		}
	}
	return false
}

type fakeAdapter struct {
	mu      sync.Mutex
	removed []string
}

func (a *fakeAdapter) RemoveDevice(p dbus.ObjectPath) *dbus.Error {
	a.mu.Lock()
	a.removed = append(a.removed, string(p))
	a.mu.Unlock()
	return nil
}

// fakeBluez runs a private bus with a bluetoothd stand-in holding the given devices.
func fakeBluez(t *testing.T, devs map[string]map[string]any) (*Central, map[string]*fakeDevice, *fakeAdapter) {
	t.Helper()
	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		t.Skip("no dbus-daemon")
	}
	cmd := exec.Command("dbus-daemon", "--session", "--nofork", "--print-address")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Skip(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	buf := make([]byte, 512)
	n, _ := out.Read(buf)
	addr := strings.TrimSpace(string(buf[:n]))
	conn, err := dbus.Connect(addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if _, err := conn.RequestName(service, 0); err != nil {
		t.Fatal(err)
	}
	ad := &fakeAdapter{}
	const adapter = dbus.ObjectPath("/org/bluez/hci0")
	if err := conn.Export(ad, adapter, adapterIfc); err != nil {
		t.Fatal(err)
	}
	fakes := map[string]*fakeDevice{}
	for mac, p := range devs {
		path := dbus.ObjectPath(string(adapter) + "/dev_" + strings.ReplaceAll(mac, ":", "_"))
		d := &fakeDevice{}
		fakes[mac] = d
		if err := conn.Export(d, path, deviceIfc); err != nil {
			t.Fatal(err)
		}
		props := map[string]*prop.Prop{"Address": {Value: mac}}
		for k, v := range p {
			props[k] = &prop.Prop{Value: v}
		}
		if _, err := prop.Export(conn, path, map[string]map[string]*prop.Prop{deviceIfc: props}); err != nil {
			t.Fatal(err)
		}
		_ = introspect.NewIntrospectable
	}
	return &Central{conn: conn, adapter: adapter, links: map[string]*link{}}, fakes, ad
}

func TestUnpairRefusesOwnPairings(t *testing.T) {
	c, _, ad := fakeBluez(t, map[string]map[string]any{
		"AA:00:00:00:00:01": {"Paired": true, "Icon": "phone", "UUIDs": []string{AudioSource}},
		"AA:00:00:00:00:02": {"Paired": true, "UUIDs": []string{"0000110b-0000-1000-8000-00805f9b34fb"}},
		"AA:00:00:00:00:03": {"Paired": true, "Class": uint32(0x5a020c)},
		"AA:00:00:00:00:04": {"Paired": true, "Icon": "lightbulb", "UUIDs": []string{"0000180f-0000-1000-8000-00805f9b34fb"}},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, a := range []string{"AA:00:00:00:00:01", "AA:00:00:00:00:02", "AA:00:00:00:00:03"} {
		if err := c.Unpair(ctx, a); err == nil {
			t.Errorf("%s: bonded phone or speaker was unpaired", a)
		}
	}
	if len(ad.removed) != 0 {
		t.Fatalf("removed %v", ad.removed)
	}
	if err := c.Unpair(ctx, "AA:00:00:00:00:04"); err != nil {
		t.Fatal(err)
	}
	if len(ad.removed) != 1 || !strings.HasSuffix(ad.removed[0], "dev_AA_00_00_00_00_04") {
		t.Errorf("removed %v, want the bulb", ad.removed)
	}
}

func TestPairTimeoutCancels(t *testing.T) {
	c, devs, _ := fakeBluez(t, map[string]map[string]any{"AA:00:00:00:00:04": {"Paired": false}})
	d := devs["AA:00:00:00:00:04"]
	d.hang = true
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := c.Pair(ctx, "AA:00:00:00:00:04"); err == nil {
		t.Fatal("pairing that ran out of time succeeded")
	}
	for i := 0; i < 50 && !d.has("CancelPairing"); i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if !d.has("CancelPairing") {
		t.Error("bluetoothd was not told to stop pairing")
	}
}
