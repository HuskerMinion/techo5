package kernel

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ubootImage is an image shaped as pack-uboot.py makes it: gzip'd U-Boot (with its marker), then the
// boot partition's device tree, tuning slot filled.
func ubootImage(t *testing.T, raw bool) []byte {
	return payloadImage(t, raw, "U-Boot 2026.10 "+string(ubootMarker))
}

// payloadImage is a boot image whose kernel part is an arm64 payload saying text (gzip'd unless raw).
func payloadImage(t *testing.T, raw bool, text string) []byte {
	img := mkImage(t, "uboot", tableB)
	im, _ := parseImage(img)
	payload := append(make([]byte, 64), []byte(text)...)
	copy(payload[56:], "ARM\x64")
	var k []byte
	if raw {
		k = payload
	} else {
		k = gzKernel(t, "x", string(payload[64:]))
	}
	// The kernel part becomes U-Boot followed by the old part's tail from the slot on.
	old := im.part(im.kernelAt, im.kernelSize)
	at := bytes.Index(old, slotMagic)
	k = append(k, old[at:]...)
	r := im.part(im.ramdiskAt, im.ramdiskSize)
	out := make([]byte, page+pad(len(k), page)+pad(len(r), page))
	copy(out, img[:page])
	binary.LittleEndian.PutUint32(out[8:], uint32(len(k)))
	copy(out[page:], k)
	copy(out[page+pad(len(k), page):], r)
	nim, err := parseImage(out)
	if err != nil {
		t.Fatal(err)
	}
	copy(out[headerID:], nim.computedID())
	return out
}

// The OS does not run the kernel update where the boot layer is (boot.sh); a restore asked for by
// hand there still finds the guard: the kept previous kernel is not written over U-Boot, and the
// state is left as it was.
func TestARestoreOverUBootIsRefused(t *testing.T) {
	ub := ubootImage(t, false)
	r := newRig(t, ub, mkImage(t, "new", nil))
	prev := mkImage(t, "old", tableB)
	os.MkdirAll(r.p.Dir, 0o755)
	os.WriteFile(filepath.Join(r.p.Dir, "prev.img"), prev, 0o644)
	os.WriteFile(filepath.Join(r.p.Dir, "prev.sha256"), []byte(sha(prev)+"\n"), 0o644)
	before, st := r.boot(), r.state()
	if err := Restore(r.p); err == nil || !strings.Contains(err.Error(), "U-Boot") {
		t.Fatalf("Restore over U-Boot = %v", err)
	}
	if !bytes.Equal(before, r.boot()) || r.state() != st {
		t.Fatal("the boot partition or the state changed")
	}
}

func TestUBootInTheBootPartitionIsNeverOverwritten(t *testing.T) {
	for _, raw := range []bool{false, true} {
		ub := ubootImage(t, raw)
		if im, _ := parseImage(ub); athenaKernel(im) == nil {
			t.Fatalf("raw %v: U-Boot taken for a kernel", raw)
		}
		r := newRig(t, ub, mkImage(t, "new", nil))
		before := r.boot()
		w, err := Apply(r.p)
		if w || err == nil || !strings.Contains(err.Error(), "U-Boot") {
			t.Fatalf("raw %v: Apply over U-Boot = %v, %v", raw, w, err)
		}
		// Even a write asked for directly (a restore, a trial put back) is refused.
		if err := r.p.writeBoot(mkImage(t, "new", tableB)); err == nil {
			t.Fatalf("raw %v: writeBoot over U-Boot did not refuse", raw)
		}
		if !bytes.Equal(before, r.boot()) {
			t.Fatalf("raw %v: U-Boot was overwritten", raw)
		}
	}
}

func TestAKernelThatIsNotLinuxIsRefused(t *testing.T) {
	img := mkImage(t, "old", tableB)
	if im, _ := parseImage(img); athenaKernel(im) != nil {
		t.Fatal("a TECHO5 kernel is refused")
	}
	// A gzip'd arm64 payload that does not say Linux (an older U-Boot without the marker, say).
	other := payloadImage(t, false, "U-Boot 2026.07 some bootloader")
	if im, err := parseImage(other); err != nil || athenaKernel(im) == nil {
		t.Fatal("a payload that is not Linux is taken for a kernel")
	}
}

// TestRealImages checks the guard on real images, when named (not in the repository: unit data):
// TECHO5_ATHENA_KERNEL_IMG a released boot-athena.img or a p20 dump, TECHO5_ATHENA_UBOOT_IMG an
// image from a TECHO5 athena build of u-boot-amazon-echo.
func TestRealImages(t *testing.T) {
	for env, want := range map[string]bool{"TECHO5_ATHENA_KERNEL_IMG": true, "TECHO5_ATHENA_UBOOT_IMG": false} {
		path := os.Getenv(env)
		if path == "" {
			continue
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		im, err := parseImage(b)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if err := athenaKernel(im); (err == nil) != want {
			t.Fatalf("%s: athenaKernel = %v, want a kernel: %v", path, err, want)
		}
	}
}
