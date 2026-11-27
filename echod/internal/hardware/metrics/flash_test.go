package metrics

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeSys writes the files given under a fresh root, as the kernel would have them.
func fakeSys(t *testing.T, files map[string]string) Reader {
	t.Helper()
	root := t.TempDir()
	for path, body := range files {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return Reader{Root: root}
}

// An SD card comes first by name and the eMMC's own boot area reads MMC too, and neither is the eMMC.
func TestFlashIsTheMMCAndNotACardOrABootArea(t *testing.T) {
	r := fakeSys(t, map[string]string{
		"sys/block/mmcblk0/device/type":      "SD\n",
		"sys/block/mmcblk1boot0/device/type": "MMC\n",
		"sys/block/mmcblk1rpmb/device/type":  "MMC\n",
		"sys/block/mmcblk1/device/type":      "MMC\n",
	})
	if got := r.Flash(); got != "mmcblk1" {
		t.Errorf("Flash() = %q, want mmcblk1", got)
	}
}

func TestNoFlashWithoutAnMMC(t *testing.T) {
	if got := fakeSys(t, map[string]string{"sys/block/sda/stat": "0\n"}).Flash(); got != "" {
		t.Errorf("Flash() = %q, want none", got)
	}
}

// The values the devices in #771 printed, from the 6.18 kernel with 0x and the older ones without.
func TestFlashReadingsAsTheKernelsPrintThem(t *testing.T) {
	r := fakeSys(t, map[string]string{
		"sys/block/mmcblk0/device/type":         "MMC\n",
		"sys/block/mmcblk0/device/life_time":    "0x03 0x05\n",
		"sys/block/mmcblk0/device/pre_eol_info": "01\n",
		"sys/block/mmcblk0/stat":                "   12345     6789   987654    43210    22000     5000  46923776   123456        0    54321   166666\n",
	})
	dev := r.Flash()
	if got := r.FlashLifeUsed(dev); got != known(50) {
		t.Errorf("life used = %+v, want 50 (the more worn of the two)", got)
	}
	if got := r.FlashSpareBlocks(dev); got != 1 {
		t.Errorf("spare blocks = %d, want 1", got)
	}
	if got := r.FlashWritten(dev); got != known(46923776*512) {
		t.Errorf("written = %+v, want %v bytes", got, 46923776.0*512)
	}
}

func TestFlashLifeUsed(t *testing.T) {
	for body, want := range map[string]Reading{
		"0x01 0x01": known(10),
		"0x0B 0x02": known(110),
		"0x00 0x00": {}, // older than eMMC 5.0: no estimate
		"0x0C 0x01": {}, // undefined
		"0x01":      {},
		"":          {},
		"junk 0x01": {},
	} {
		r := fakeSys(t, map[string]string{"sys/block/mmcblk0/device/life_time": body + "\n"})
		if got := r.FlashLifeUsed("mmcblk0"); got != want {
			t.Errorf("life_time %q = %+v, want %+v", body, got, want)
		}
	}
}

func TestFlashSpareBlocks(t *testing.T) {
	for body, want := range map[string]int{
		"0x01": 1, "02": 2, "0x03": 3, "0x00": 0, "0x04": 0, "": 0,
	} {
		r := fakeSys(t, map[string]string{"sys/block/mmcblk0/device/pre_eol_info": body + "\n"})
		if got := r.FlashSpareBlocks("mmcblk0"); got != want {
			t.Errorf("pre_eol_info %q = %d, want %d", body, got, want)
		}
	}
}

// A device without the files, or without an eMMC found, has no readings and does not fail.
func TestFlashReadingsWithNothingToRead(t *testing.T) {
	r := fakeSys(t, map[string]string{"sys/block/mmcblk0/device/type": "MMC\n"})
	for _, dev := range []string{"mmcblk0", ""} {
		if got := r.FlashLifeUsed(dev); got.Known {
			t.Errorf("%q: life used = %+v, want none", dev, got)
		}
		if got := r.FlashSpareBlocks(dev); got != 0 {
			t.Errorf("%q: spare blocks = %d, want none", dev, got)
		}
		if got := r.FlashWritten(dev); got.Known {
			t.Errorf("%q: written = %+v, want none", dev, got)
		}
	}
	short := fakeSys(t, map[string]string{"sys/block/mmcblk0/stat": "1 2 3\n"})
	if got := short.FlashWritten("mmcblk0"); got.Known {
		t.Errorf("short stat: written = %+v, want none", got)
	}
}
