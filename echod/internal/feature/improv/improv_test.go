//go:build linux && !panel

package improv

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	proto "github.com/HuskerMinion/techo5/echod/internal/lib/improv"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wifi"
)

// record keeps what a session set, in order, as a central reading the characteristics would see it.
type record struct{ sets []set }

type set struct {
	uuid  string
	value []byte
}

func (r *record) Set(uuid string, value []byte) {
	r.sets = append(r.sets, set{uuid, append([]byte(nil), value...)})
}

func (r *record) last(uuid string) []byte {
	for i := len(r.sets) - 1; i >= 0; i-- {
		if r.sets[i].uuid == uuid {
			return r.sets[i].value
		}
	}
	return nil
}

func (r *record) all(uuid string) [][]byte {
	var out [][]byte
	for _, s := range r.sets {
		if s.uuid == uuid {
			out = append(out, s.value)
		}
	}
	return out
}

func session(t *testing.T, st proto.State) (*run, *record) {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	r := &record{}
	return &run{p: r, state: st, scan: func(context.Context) ([]wifi.Network, error) { return nil, nil }}, r
}

func TestANetworkWaitsForAuthorization(t *testing.T) {
	s, r := session(t, proto.AuthorizationRequired)
	if _, ok := s.command(context.Background(), proto.WiFiFrame(proto.WiFi{SSID: "Home", Passphrase: "password1"})); ok {
		t.Fatal("joined before a button was pressed")
	}
	if got := r.last(proto.ErrorUUID); !bytes.Equal(got, []byte{byte(proto.ErrNotAuthorized)}) {
		t.Fatalf("error % x", got)
	}
}

func TestANetworkIsJoinedOnceAuthorized(t *testing.T) {
	s, r := session(t, proto.Authorized)
	w, ok := s.command(context.Background(), proto.WiFiFrame(proto.WiFi{SSID: "Home", Passphrase: "password1"}))
	if !ok || w.SSID != "Home" || w.Passphrase != "password1" {
		t.Fatalf("got %+v %v", w, ok)
	}
	if got := r.last(proto.StateUUID); !bytes.Equal(got, []byte{byte(proto.Provisioning)}) {
		t.Fatalf("state % x", got)
	}
	if got := r.last(proto.ErrorUUID); !bytes.Equal(got, []byte{byte(proto.ErrNone)}) {
		t.Fatalf("error % x", got)
	}

	// A second network while the first is being joined is not started too.
	if _, ok := s.command(context.Background(), proto.WiFiFrame(proto.WiFi{SSID: "Other"})); ok {
		t.Fatal("a second join started while the first ran")
	}

	// A failed join goes back to taking a network.
	s.setError(proto.ErrUnableToConnect)
	s.setState(proto.Authorized)
	if _, ok := s.command(context.Background(), proto.WiFiFrame(proto.WiFi{SSID: "Home", Passphrase: "password2"})); !ok {
		t.Fatal("no second try after a failure")
	}
}

func TestBadFrames(t *testing.T) {
	s, r := session(t, proto.Authorized)
	ctx := context.Background()

	s.command(ctx, []byte{0x01, 0x05, 0x00})
	if got := r.last(proto.ErrorUUID); !bytes.Equal(got, []byte{byte(proto.ErrInvalidRPC)}) {
		t.Fatalf("malformed: error % x", got)
	}
	s.command(ctx, proto.Frame{Command: proto.CmdWiFiSettings, Data: []byte{0x00, 0x00}}.Bytes())
	if got := r.last(proto.ErrorUUID); !bytes.Equal(got, []byte{byte(proto.ErrInvalidRPC)}) {
		t.Fatalf("empty ssid: error % x", got)
	}
	s.command(ctx, proto.Frame{Command: 0x7f}.Bytes())
	if got := r.last(proto.ErrorUUID); !bytes.Equal(got, []byte{byte(proto.ErrUnknownCommand)}) {
		t.Fatalf("unknown: error % x", got)
	}
	if got := r.last(proto.StateUUID); got != nil {
		t.Fatalf("state moved to % x", got)
	}
}

func TestScanAnswersOnePerNetworkThenAnEmptyOne(t *testing.T) {
	s, r := session(t, proto.Authorized)
	s.scan = func(context.Context) ([]wifi.Network, error) {
		return []wifi.Network{{SSID: "Home", Signal: -48, Secured: true}, {SSID: "Cafe", Signal: -80}}, nil
	}
	s.command(context.Background(), proto.Frame{Command: proto.CmdScan}.Bytes())
	got := r.all(proto.RPCResultUUID)
	want := [][]byte{
		proto.Result(proto.CmdScan, "Home", "-48", "YES"),
		proto.Result(proto.CmdScan, "Cafe", "-80", "NO"),
		proto.Result(proto.CmdScan),
	}
	if len(got) != len(want) {
		t.Fatalf("%d results, want %d", len(got), len(want))
	}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Fatalf("result %d: % x, want % x", i, got[i], want[i])
		}
	}
}

func TestAFailedScanStillEnds(t *testing.T) {
	s, r := session(t, proto.Authorized)
	s.scan = func(context.Context) ([]wifi.Network, error) { return nil, errors.New("busy") }
	s.command(context.Background(), proto.Frame{Command: proto.CmdScan}.Bytes())
	if got := r.all(proto.RPCResultUUID); len(got) != 1 || !bytes.Equal(got[0], proto.Result(proto.CmdScan)) {
		t.Fatalf("results %v", got)
	}
}

func TestDeviceInfo(t *testing.T) {
	s, r := session(t, proto.Authorized)
	s.command(context.Background(), proto.Frame{Command: proto.CmdDeviceInfo}.Bytes())
	got := r.last(proto.RPCResultUUID)
	if len(got) < 3 || got[0] != byte(proto.CmdDeviceInfo) {
		t.Fatalf("result % x", got)
	}
	if !bytes.Contains(got, []byte("TECHO5")) {
		t.Fatalf("no firmware name in % x", got)
	}
}

// Before the press only Identify is answered: a scan would list the networks around the device to
// anyone in range, and device info its name.
func TestOnlyIdentifyBeforeAuthorization(t *testing.T) {
	s, r := session(t, proto.AuthorizationRequired)
	scanned := false
	s.scan = func(context.Context) ([]wifi.Network, error) { scanned = true; return nil, nil }
	ctx := context.Background()
	for _, cmd := range []proto.Command{proto.CmdScan, proto.CmdDeviceInfo} {
		s.command(ctx, proto.Frame{Command: cmd}.Bytes())
		if got := r.last(proto.ErrorUUID); !bytes.Equal(got, []byte{byte(proto.ErrNotAuthorized)}) {
			t.Fatalf("command %#x: error % x", cmd, got)
		}
	}
	if scanned || r.all(proto.RPCResultUUID) != nil {
		t.Fatalf("answered before the press: scanned %v, results %v", scanned, r.all(proto.RPCResultUUID))
	}
	s.command(ctx, proto.Frame{Command: proto.CmdIdentify}.Bytes())
	if got := r.last(proto.ErrorUUID); !bytes.Equal(got, []byte{byte(proto.ErrNone)}) {
		t.Fatalf("identify: error % x", got)
	}
}

// Once a network was joined, another is not taken in the seconds before the session closes.
func TestNoSecondNetworkOnceProvisioned(t *testing.T) {
	s, r := session(t, proto.Provisioned)
	if _, ok := s.command(context.Background(), proto.WiFiFrame(proto.WiFi{SSID: "Other", Passphrase: "password1"})); ok {
		t.Fatal("took a network after one was joined")
	}
	if got := r.last(proto.ErrorUUID); !bytes.Equal(got, []byte{byte(proto.ErrNotAuthorized)}) {
		t.Fatalf("error % x", got)
	}
}
