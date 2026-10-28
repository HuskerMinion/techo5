//go:build panel

package bluetooth

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"testing"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/ble"
)

type panelRetryScanner struct {
	err      error
	running  bool
	attempts int
}

func (s *panelRetryScanner) Start(bool, bool, []byte, func(ble.Advertisement)) error {
	s.attempts++
	s.running = s.err == nil
	return s.err
}
func (s *panelRetryScanner) Stop()           { s.running = false }
func (s *panelRetryScanner) Running() bool   { return s.running }
func (s *panelRetryScanner) Scanning() bool  { return s.running }
func (s *panelRetryScanner) Reports() uint64 { return 0 }

func TestPanelUnavailableScanRetriesWithoutErrorLog(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "config.json"))
	if err := config.Set().Bluetooth().Proxy(true); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelError})))
	t.Cleanup(func() { slog.SetDefault(old) })
	radio := &panelRetryScanner{err: fmt.Errorf("panel scan: %w", ble.ErrScanUnavailable)}
	b := &Proxy{proxy: &esphome.BluetoothProxy{}, radio: radio}
	if b.tune(beaconState{minor: -1}) {
		t.Fatal("unavailable scanner was considered settled")
	}
	if logs.Len() != 0 {
		t.Fatalf("permission/adapter wait logged as a fault: %s", &logs)
	}
	radio.err = nil
	if !b.tune(beaconState{minor: -1}) || radio.attempts != 2 {
		t.Fatal("scanner did not recover on retry")
	}
	radio.Stop()
	radio.err = errors.New("unexpected radio failure")
	if b.tune(beaconState{minor: -1}) || logs.Len() == 0 {
		t.Fatal("real scanner failure was hidden")
	}
}
