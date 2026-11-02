package bluetooth

import (
	"path/filepath"
	"testing"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/ble"
)

type countingScanner struct {
	running bool
	starts  int
}

func (s *countingScanner) Start(bool, bool, []byte, func(ble.Advertisement)) error {
	s.starts++
	s.running = true
	return nil
}
func (s *countingScanner) Stop()           { s.running = false }
func (s *countingScanner) Running() bool   { return s.running }
func (s *countingScanner) Scanning() bool  { return s.running }
func (s *countingScanner) Reports() uint64 { return 0 }

// A controller powered off and on has lost its scan while the radio still reads as scanning: the
// next tune starts it again, and the one after that leaves it be.
func TestAPowerCycleStartsTheScanAgain(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "config.json"))
	if err := config.Set().Bluetooth().Proxy(true); err != nil {
		t.Fatal(err)
	}
	radio := &countingScanner{}
	b := &Proxy{proxy: &esphome.BluetoothProxy{}, radio: radio}

	b.tune(beaconState{minor: -1})
	b.tune(beaconState{minor: -1})
	if radio.starts != 1 {
		t.Fatalf("starts = %d before the power cycle, want 1", radio.starts)
	}

	b.lost.Store(true)
	b.tune(beaconState{minor: -1})
	if radio.starts != 2 {
		t.Fatalf("starts = %d after the power cycle, want 2", radio.starts)
	}
	b.tune(beaconState{minor: -1})
	if radio.starts != 2 {
		t.Fatalf("starts = %d on the tune after, want 2", radio.starts)
	}
}
