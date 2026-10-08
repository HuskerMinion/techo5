package config

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// Videos are off on a new device. Allowed addresses are kept once each, least recently used first, up
// to MostAllowed; each use starts their thirty days again, an allowance unused that long runs out and
// goes, and a snapshot does not share the store's list.
func TestVideoSettings(t *testing.T) {
	Use(filepath.Join(t.TempDir(), "state.json"))
	if v := Get().Video; v.On || v.DLNA || len(v.Allowed) != 0 {
		t.Fatalf("a new device has videos set: %+v", v)
	}
	at := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	for i := range MostAllowed + 3 {
		if err := Set().Video().Allow(fmt.Sprintf("192.168.1.%d", i), at.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	later := at.Add(2 * time.Hour)
	if err := Set().Video().Allow("192.168.1.10", later); err != nil { // used again
		t.Fatal(err)
	}
	v := Get().Video
	if len(v.Allowed) != MostAllowed || v.Allowed[0].Addr != "192.168.1.3" || v.Allowed[MostAllowed-1].Addr != "192.168.1.10" ||
		!v.Allowed[MostAllowed-1].Used.Equal(later) {
		t.Errorf("allowed: %+v", v.Allowed)
	}
	if !v.IsAllowed("192.168.1.10", later) || v.IsAllowed("192.168.1.0", later) || v.IsAllowed("", later) {
		t.Error("IsAllowed is wrong")
	}
	// Thirty days and an hour on: .10 (used again two hours in) still in; the rest out.
	month := at.Add(AllowedFor).Add(time.Hour)
	if !v.IsAllowed("192.168.1.10", month) || v.IsAllowed("192.168.1.5", month) {
		t.Error("an allowance did not run out, or ran out early")
	}
	if err := Set().Video().Allow("192.168.1.99", month); err != nil {
		t.Fatal(err)
	}
	if v := Get().Video; len(v.Allowed) != 2 {
		t.Errorf("run-out allowances kept: %+v", v.Allowed)
	}
	v = Get().Video
	v.Allowed[0].Addr = "changed"
	if Get().Video.Allowed[0].Addr == "changed" {
		t.Error("a snapshot shares the store's list")
	}
	if err := Set().Video().ForgetAllowed(); err != nil {
		t.Fatal(err)
	}
	if len(Get().Video.Allowed) != 0 {
		t.Error("forgetting kept some")
	}
}
