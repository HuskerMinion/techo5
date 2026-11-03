package wifiband

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wifi"
)

// world is what the component sees: a flag file in a temporary directory, and the saves it made.
type world struct {
	t     *testing.T
	saved []bool
	fail  bool // the flag will not go up or come down
}

func newWorld(t *testing.T) *world {
	t.Helper()
	old := wifi.KeepBandFlag
	wifi.KeepBandFlag = filepath.Join(t.TempDir(), "run", "techo5", "wifi-keep-band")
	t.Cleanup(func() { wifi.KeepBandFlag = old })
	return &world{t: t}
}

func (w *world) band() *Band {
	b := New()
	b.save = func(stay bool) error { w.saved = append(w.saved, stay); return nil }
	b.set = func(keep bool) error {
		if w.fail {
			return errors.New("read-only file system")
		}
		return wifi.SetKeepBand(keep)
	}
	return b
}

func (w *world) flag() bool {
	_, err := os.Stat(wifi.KeepBandFlag)
	return err == nil
}

func TestItPrefers5GHzUntilSomebodyAsksOtherwise(t *testing.T) {
	w := newWorld(t)
	b := w.band()
	b.Restore(config.Config{})
	if !b.prefer.Get() {
		t.Fatal("the switch is off on a device nobody set anything on")
	}
	if w.flag() {
		t.Fatal("the keeper is told to leave the band on a device nobody set anything on")
	}
}

func TestTheSavedChoiceIsPutBackAtStart(t *testing.T) {
	w := newWorld(t)
	b := w.band()
	b.Restore(config.Config{WifiBand: config.WifiBand{Stay: true}})
	if b.prefer.Get() {
		t.Fatal("the switch is on though the device was left to stay on its band")
	}
	if !w.flag() {
		t.Fatal("the flag is not up after a start with Stay saved: /run was cleared by the reboot")
	}
}

func TestTheSwitchSavesAndMovesTheFlag(t *testing.T) {
	w := newWorld(t)
	b := w.band()
	b.Restore(config.Config{})

	b.prefer.OnCommand(false) // off: stay on the band
	if !w.flag() || b.prefer.Get() {
		t.Fatalf("after switching off: flag %v, switch %v; want flag up, switch off", w.flag(), b.prefer.Get())
	}
	b.prefer.OnCommand(true) // on: look for 5 GHz again
	if w.flag() || !b.prefer.Get() {
		t.Fatalf("after switching on: flag %v, switch %v; want flag down, switch on", w.flag(), b.prefer.Get())
	}
	if len(w.saved) != 2 || w.saved[0] != true || w.saved[1] != false {
		t.Fatalf("saved %v, want [true false] (Stay)", w.saved)
	}
}

func TestAFlagThatWillNotGoUpLeavesTheSwitchOn(t *testing.T) {
	w := newWorld(t)
	w.fail = true
	b := w.band()
	b.Restore(config.Config{})

	b.prefer.OnCommand(false)
	if !b.prefer.Get() {
		t.Fatal("the switch shows off though the keeper was never told: it still looks for 5 GHz")
	}
}

func TestSetKeepBandIsIdempotent(t *testing.T) {
	newWorld(t)
	if err := wifi.SetKeepBand(false); err != nil {
		t.Fatalf("taking down a flag that is not there: %v", err)
	}
	if err := wifi.SetKeepBand(true); err != nil {
		t.Fatal(err)
	}
	if err := wifi.SetKeepBand(true); err != nil {
		t.Fatalf("putting up a flag that is up: %v", err)
	}
	if err := wifi.SetKeepBand(false); err != nil {
		t.Fatal(err)
	}
}
