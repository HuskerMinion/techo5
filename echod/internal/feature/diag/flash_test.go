package diag

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/metrics"
)

func emmc(t *testing.T, files map[string]string) metrics.Reader {
	t.Helper()
	root := t.TempDir()
	for path, body := range files {
		full := filepath.Join(root, "sys/block/mmcblk0", path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return metrics.Reader{Root: root}
}

func ids(es []esphome.Entity) []string {
	var out []string
	for _, e := range es {
		switch e := e.(type) {
		case *esphome.Sensor:
			out = append(out, e.ObjectID)
		case *esphome.TextSensor:
			out = append(out, e.ObjectID)
		}
	}
	return out
}

// An eMMC 5.0 or later, as the devices in #771 report: all three sensors, the life and spare blocks
// set at once, the data written in MiB on the first sample and then not again for ten minutes.
func TestFlashSensorsFromTheChip(t *testing.T) {
	r := emmc(t, map[string]string{
		"device/type":         "MMC\n",
		"device/life_time":    "0x03 0x03\n",
		"device/pre_eol_info": "0x02\n",
		"stat":                "1 0 2 3 4 0 4096 5 0 6 7\n",
	})
	f := newFlash(r)
	if got := ids(f.entities()); len(got) != 3 {
		t.Fatalf("entities = %v, want all three", got)
	}
	if got := f.life.Get(); got != 30 {
		t.Errorf("life used = %v, want 30", got)
	}
	if got := f.spare.Get(); got != "Warning" {
		t.Errorf("spare blocks = %q, want Warning", got)
	}

	now := time.Now()
	f.sample(now)
	if got := f.written.Get(); got != 2 {
		t.Errorf("written = %v MiB, want 2", got)
	}

	// More written, but too soon to say so; then late enough.
	if err := os.WriteFile(filepath.Join(r.Root, "sys/block/mmcblk0/stat"), []byte("1 0 2 3 4 0 8192 5 0 6 7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.sample(now.Add(time.Minute))
	if got := f.written.Get(); got != 2 {
		t.Errorf("written a minute later = %v MiB, want still 2", got)
	}
	f.sample(now.Add(flashEvery))
	if got := f.written.Get(); got != 4 {
		t.Errorf("written ten minutes later = %v MiB, want 4", got)
	}
}

// An eMMC older than 5.0 keeps no estimate and reads 0x00; only the data written is offered.
func TestFlashSensorsLeaveOutWhatTheChipDoesNotSay(t *testing.T) {
	f := newFlash(emmc(t, map[string]string{
		"device/type":         "MMC\n",
		"device/life_time":    "0x00 0x00\n",
		"device/pre_eol_info": "0x00\n",
		"stat":                "1 0 2 3 4 0 4096 5 0 6 7\n",
	}))
	if got := ids(f.entities()); len(got) != 1 || got[0] != "flash_data_written" {
		t.Errorf("entities = %v, want only flash_data_written", got)
	}
}

// No eMMC to be found, as an app on a tablet may see it: nothing offered, and a sample does nothing.
func TestFlashSensorsWithNoEMMC(t *testing.T) {
	f := newFlash(metrics.Reader{Root: t.TempDir()})
	if got := f.entities(); len(got) != 0 {
		t.Errorf("entities = %v, want none", ids(got))
	}
	f.sample(time.Now())
}
