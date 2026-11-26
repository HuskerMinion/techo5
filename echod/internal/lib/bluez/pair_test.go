//go:build linux

package bluez

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/prop"
)

// fakeSpeaker is a legacy-PIN speaker as bluetoothd presents it: Pair asks the agent for a PIN, and
// a refused one leaves the device's object removed unless it is trusted, as BlueZ 5.86 does with a
// device it only knows from discovery (techo5-dot#56).
type fakeSpeaker struct {
	mu    sync.Mutex
	conn  *dbus.Conn
	path  dbus.ObjectPath
	props *prop.Properties
	pin   string
	given []string

	// always is bluetoothd removing the device after a refusal even when trusted, and found is
	// discovery finding it again a moment later: the fallback for a bluetoothd that does not keep
	// a trusted device.
	always bool
	found  func()
}

const speakerMAC = "34:81:F4:00:00:03"

// export puts the device on the bus, as bluetoothd does with one discovery found: not paired, and
// not trusted.
func (s *fakeSpeaker) export() error {
	if err := s.conn.Export(s, s.path, deviceIfc); err != nil {
		return err
	}
	props, err := prop.Export(s.conn, s.path, map[string]map[string]*prop.Prop{deviceIfc: {
		"Address": {Value: speakerMAC},
		"Paired":  {Value: false},
		"Trusted": {Value: false, Writable: true},
	}})
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.props = props
	s.mu.Unlock()
	return nil
}

func (s *fakeSpeaker) get(name string) any {
	s.mu.Lock()
	p := s.props
	s.mu.Unlock()
	return p.GetMust(deviceIfc, name)
}

func (s *fakeSpeaker) Pair() *dbus.Error {
	pin := pinFor(s.path)
	s.mu.Lock()
	s.given = append(s.given, pin)
	props, always, found := s.props, s.always, s.found
	s.mu.Unlock()
	if pin == s.pin {
		props.SetMust(deviceIfc, "Paired", true)
		return nil
	}
	if trusted, _ := props.GetMust(deviceIfc, "Trusted").(bool); !trusted || always {
		_ = s.conn.Export(nil, s.path, deviceIfc)
		_ = s.conn.Export(nil, s.path, propsIfc)
		if found != nil {
			found()
		}
	}
	return &dbus.Error{Name: "org.bluez.Error.AuthenticationFailed", Body: []any{"Authentication Failed"}}
}

func fakeSpeakerAdapter(t *testing.T, pin string) (*Adapter, *fakeSpeaker, *fakeAdapter) {
	t.Helper()
	addr := privateBus(t)
	bt := connect(t, addr)
	if _, err := bt.RequestName(service, 0); err != nil {
		t.Fatal(err)
	}
	const adapter = dbus.ObjectPath("/org/bluez/hci0")
	ad := &fakeAdapter{}
	if err := bt.Export(ad, adapter, adapterIfc); err != nil {
		t.Fatal(err)
	}
	path := dbus.ObjectPath(string(adapter) + "/dev_34_81_F4_00_00_03")
	s := &fakeSpeaker{conn: bt, path: path, pin: pin}
	if err := s.export(); err != nil {
		t.Fatal(err)
	}

	c := connect(t, addr)
	a := &Adapter{conn: c, path: adapter, obj: c.Object(service, adapter),
		devices: map[dbus.ObjectPath]*Device{path: {Path: path, Address: speakerMAC}},
		players: map[dbus.ObjectPath]map[string]map[string]dbus.Variant{}}
	return a, s, ad
}

// A bluetoothd that removes the speaker after a refusal though it is trusted: Pair waits for
// discovery to find it again, trusts the new object and tries the PIN that found the old one gone.
// The list keeps the device throughout, as it does until InterfacesRemoved is in: the wait is on
// bluetoothd's object, not the list.
func TestPairWaitsForASpeakerFoundAgain(t *testing.T) {
	old := pinRetryPause
	pinRetryPause = 0
	defer func() { pinRetryPause = old }()
	a, s, _ := fakeSpeakerAdapter(t, "1234")
	s.always = true
	s.found = func() {
		time.AfterFunc(300*time.Millisecond, func() {
			if err := s.export(); err != nil {
				t.Error(err)
			}
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.Pair(ctx, speakerMAC); err != nil {
		t.Fatalf("pairing failed: %v", err)
	}
	if want := []string{"0000", "1234"}; !slices.Equal(s.tried(), want) {
		t.Errorf("PINs given %v, want %v", s.tried(), want)
	}
	if trusted, _ := s.get("Trusted").(bool); !trusted {
		t.Error("the speaker found again was not trusted")
	}
}

// The Gecko in.stream 2 wants 5555: trusted before the first try, it is still there for every PIN
// after a refusal and pairs with the fifth.
func TestPairKeepsAFoundSpeakerAcrossRefusedPINs(t *testing.T) {
	old := pinRetryPause
	pinRetryPause = 0
	defer func() { pinRetryPause = old }()
	a, s, ad := fakeSpeakerAdapter(t, "5555")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.Pair(ctx, "34:81:F4:00:00:03"); err != nil {
		t.Fatalf("pairing failed: %v", err)
	}
	if want := []string{"0000", "1234", "1111", "8888", "5555"}; !slices.Equal(s.tried(), want) {
		t.Errorf("PINs given %v, want %v", s.tried(), want)
	}
	if trusted, _ := s.get("Trusted").(bool); !trusted {
		t.Error("the paired speaker is not trusted")
	}
	if r := ad.gone(); len(r) != 0 {
		t.Errorf("the paired speaker was removed: %v", r)
	}
}

// A speaker that refuses every PIN is left as bluetoothd would have left it: not trusted, and
// forgotten.
func TestPairThatFailsTakesTheTrustBack(t *testing.T) {
	old := pinRetryPause
	pinRetryPause = 0
	defer func() { pinRetryPause = old }()
	a, s, ad := fakeSpeakerAdapter(t, "9999")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.Pair(ctx, "34:81:F4:00:00:03"); !bluezError(err, "AuthenticationFailed") {
		t.Fatalf("err = %v, want the last refusal", err)
	}
	if g := s.tried(); len(g) != len(legacyPINs) {
		t.Errorf("PINs given %v, want all of them", g)
	}
	if trusted, _ := s.get("Trusted").(bool); trusted {
		t.Error("the speaker that never paired is still trusted")
	}
	if r := ad.gone(); len(r) != 1 || r[0] != string(s.path) {
		t.Errorf("removed %v, want the speaker", r)
	}
}

func (s *fakeSpeaker) tried() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.given)
}

func (a *fakeAdapter) gone() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.removed)
}
