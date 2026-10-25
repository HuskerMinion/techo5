package kernel

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The Skylight's command lines, as tools/linux/skylight/kernel/build.sh gives them.
const (
	skyBootCmdline      = "console=ttyS2,1500000n8 panic=10 ro rootwait root=PARTLABEL=techo5-store init=/init"
	skyInstallerCmdline = "console=ttyS2,1500000n8 panic=10 rdinit=/init techo5.installer=1"
)

// mkSkyImage is a header version 2 boot image like pack-sky.py's: a kernel part that tells kernels
// apart by tag, an empty ramdisk, a resource image and a device tree, each page aligned, the id over
// all of them.
func mkSkyImage(t *testing.T, tag, cmdline string) []byte {
	t.Helper()
	k := append([]byte("arm64 kernel "+tag+" "), bytes.Repeat([]byte{0x5a}, 5000)...)
	r := []byte("ramdisk")
	s := []byte("RSCE resource " + tag)
	d := []byte("\xd0\x0d\xfe\xed device tree skylight,cal150")
	img := make([]byte, page+pad(len(k), page)+pad(len(r), page)+pad(len(s), page)+pad(len(d), page))
	copy(img, "ANDROID!")
	le := binary.LittleEndian
	le.PutUint32(img[8:], uint32(len(k)))
	le.PutUint32(img[16:], uint32(len(r)))
	le.PutUint32(img[24:], uint32(len(s)))
	le.PutUint32(img[36:], page)
	le.PutUint32(img[40:], 2)
	copy(img[64:], cmdline)
	le.PutUint32(img[1644:], 1660)
	le.PutUint32(img[1648:], uint32(len(d)))
	at := page
	for _, part := range [][]byte{k, r, s, d} {
		copy(img[at:], part)
		at += pad(len(part), page)
	}
	// The id as pack-sky.py makes it, written out here rather than taken from computedID, so the test
	// holds the code to the packer's algorithm and not to itself.
	h := sha1.New()
	var n [4]byte
	for _, b := range [][]byte{k, r, s, nil, d} {
		h.Write(b)
		le.PutUint32(n[:], uint32(len(b)))
		h.Write(n[:])
	}
	copy(img[headerID:], h.Sum(nil))
	return img
}

// newSkyRig is a Skylight: its recovery partition (64 KiB more than the image, holding what was there
// before) with running in it, and a slot carrying shipped (nil: none).
func newSkyRig(t *testing.T, running, shipped []byte) *rig {
	t.Helper()
	d := t.TempDir()
	p := Paths{
		Board:      Skylight,
		Shipped:    filepath.Join(d, "slot", "boot-skylight.img"),
		ShippedSum: filepath.Join(d, "slot", "boot-skylight.img.sha256"),
		Boot:       filepath.Join(d, "recovery-partition"),
		Dir:        filepath.Join(d, "data", "kernel"),
	}
	part := bytes.Repeat([]byte{0xee}, len(running)+65536)
	copy(part, running)
	if err := os.WriteFile(p.Boot, part, 0o644); err != nil {
		t.Fatal(err)
	}
	if shipped != nil {
		os.MkdirAll(filepath.Dir(p.Shipped), 0o755)
		os.WriteFile(p.Shipped, shipped, 0o644)
		os.WriteFile(p.ShippedSum, []byte(sha(shipped)+"  boot-skylight.img\n"), 0o644)
	}
	return &rig{t, p}
}

func TestSkylightPathsAreTheUnits(t *testing.T) {
	if SkylightDefault.Board != Skylight || SkylightDefault.Board.Partition() != "recovery" {
		t.Fatal("the Skylight's kernel is not taken from recovery")
	}
	if Default.Board != Athena || Default.Board.Partition() != "boot" {
		t.Fatal("athena's kernel is not taken from boot")
	}
	// One state directory: a system update waits for a kernel on trial on either board.
	if SkylightDefault.Dir != Default.Dir {
		t.Fatal("the Skylight keeps its kernel state somewhere else")
	}
	if !strings.HasSuffix(SkylightDefault.Shipped, "/boot-skylight.img") ||
		SkylightDefault.ShippedSum != SkylightDefault.Shipped+".sha256" {
		t.Fatalf("the slot's kernel is %s, %s", SkylightDefault.Shipped, SkylightDefault.ShippedSum)
	}
}

func TestSkylightApplyWritesTheSlotsKernelAsItIs(t *testing.T) {
	old := mkSkyImage(t, "old", skyBootCmdline)
	shipped := mkSkyImage(t, "new", skyBootCmdline)
	r := newSkyRig(t, old, shipped)
	written, err := Apply(r.p)
	if err != nil || !written {
		t.Fatalf("Apply = %v, %v; want a write", written, err)
	}
	if !r.holds(shipped) {
		t.Fatal("recovery does not hold the slot's kernel byte for byte")
	}
	part := r.boot()
	if !bytes.Equal(part[len(shipped):], bytes.Repeat([]byte{0xee}, len(part)-len(shipped))) {
		t.Fatal("the write went past the image")
	}
	if got := r.state(); got != "trial 3 "+sha(shipped)+" "+strconv.Itoa(len(shipped))+" "+sha(shipped) {
		t.Fatalf("state %q", got)
	}
	prev, _ := os.ReadFile(r.p.file("prev.img"))
	if !bytes.Equal(prev, old) {
		t.Fatal("prev.img is not the kernel that was there")
	}
	if w, err := Apply(r.p); w || err != nil {
		t.Fatalf("Apply on trial = %v, %v", w, err)
	}
}

func TestSkylightApplyLeavesAnUpToDateKernel(t *testing.T) {
	same := mkSkyImage(t, "same", skyBootCmdline)
	r := newSkyRig(t, same, bytes.Clone(same))
	before := r.boot()
	if w, err := Apply(r.p); w || err != nil {
		t.Fatalf("Apply = %v, %v; want nothing to do", w, err)
	}
	if !bytes.Equal(before, r.boot()) || r.state() != "" {
		t.Fatal("an up-to-date kernel was touched")
	}
}

func TestSkylightApplyRefuses(t *testing.T) {
	cases := map[string]struct {
		running, shipped []byte
	}{
		// What a unit has in recovery when it is not as an install left it.
		"over Android's recovery": {
			mkSkyImage(t, "android", "console=ttyFIQ0 androidboot.mode=recovery"),
			mkSkyImage(t, "new", skyBootCmdline),
		},
		"over the installer image": {
			mkSkyImage(t, "installer", skyInstallerCmdline),
			mkSkyImage(t, "new", skyBootCmdline),
		},
		"over athena's kind of image": {
			mkImage(t, "old", tableB),
			mkSkyImage(t, "new", skyBootCmdline),
		},
		// What a slot must never carry.
		"the installer image as the slot's kernel": {
			mkSkyImage(t, "old", skyBootCmdline),
			mkSkyImage(t, "installer", skyInstallerCmdline),
		},
		"a kernel with installer words": {
			mkSkyImage(t, "old", skyBootCmdline),
			mkSkyImage(t, "new", skyBootCmdline+" rdinit=/init"),
		},
		"a kernel without the store as its root": {
			mkSkyImage(t, "old", skyBootCmdline),
			mkSkyImage(t, "new", "console=ttyS2,1500000n8 panic=10 ro rootwait root=/dev/mmcblk0p8"),
		},
		"athena's kernel as the slot's": {
			mkSkyImage(t, "old", skyBootCmdline),
			mkImage(t, "new", nil),
		},
		"a slot's kernel with bytes after its end": {
			mkSkyImage(t, "old", skyBootCmdline),
			append(mkSkyImage(t, "new", skyBootCmdline), 1),
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := newSkyRig(t, c.running, c.shipped)
			before := r.boot()
			if w, err := Apply(r.p); w || err == nil {
				t.Fatalf("Apply = %v, %v; want a refusal", w, err)
			}
			if !bytes.Equal(before, r.boot()) || r.state() != "" {
				t.Fatal("recovery or the state was written")
			}
		})
	}
	t.Run("a slot's kernel that does not match its sum", func(t *testing.T) {
		r := newSkyRig(t, mkSkyImage(t, "old", skyBootCmdline), mkSkyImage(t, "new", skyBootCmdline))
		os.WriteFile(r.p.ShippedSum, []byte(strings.Repeat("0", 64)+"\n"), 0o644)
		if w, err := Apply(r.p); w || err == nil {
			t.Fatalf("Apply = %v, %v; want a refusal", w, err)
		}
	})
	t.Run("a running kernel whose header id is wrong", func(t *testing.T) {
		r := newSkyRig(t, mkSkyImage(t, "old", skyBootCmdline), mkSkyImage(t, "new", skyBootCmdline))
		b := r.boot()
		b[page+3] ^= 1
		os.WriteFile(r.p.Boot, b, 0o644)
		if w, err := Apply(r.p); w || err == nil {
			t.Fatalf("Apply = %v, %v; want a refusal", w, err)
		}
	})
}

// The trial is athena's: three boots to settle, and the previous kernel back in recovery after a
// trial that never does, the kernel marked bad and not written again.
func TestSkylightTrialThatNeverSettlesPutsThePreviousKernelBack(t *testing.T) {
	old := mkSkyImage(t, "old", skyBootCmdline)
	shipped := mkSkyImage(t, "new", skyBootCmdline)
	r := newSkyRig(t, old, shipped)
	if again, err := r.failTrial(0); !again || err != nil {
		t.Fatalf("the last boot = %v, %v; want the previous kernel back and a reboot", again, err)
	}
	if !r.holds(old) || r.state() != "" || !r.p.bad()[sha(shipped)] {
		t.Fatalf("after the trial: old back %v, state %q, bad %v", r.holds(old), r.state(), r.p.bad())
	}
	if w, err := Apply(r.p); w || err != nil || !r.holds(old) {
		t.Fatalf("Apply of a bad kernel = %v, %v", w, err)
	}
}

func TestSkylightTrialSettles(t *testing.T) {
	shipped := mkSkyImage(t, "new", skyBootCmdline)
	r := newSkyRig(t, mkSkyImage(t, "old", skyBootCmdline), shipped)
	if _, err := Apply(r.p); err != nil {
		t.Fatal(err)
	}
	if again, err := Boot(r.p); again || err != nil || !strings.HasPrefix(r.state(), "trial 2 ") {
		t.Fatalf("Boot = %v, %v; state %q", again, err, r.state())
	}
	if err := Settled(r.p); err != nil || !strings.HasPrefix(r.state(), "good ") {
		t.Fatalf("Settled = %v; state %q", err, r.state())
	}
	if w, err := Apply(r.p); w || err != nil || !r.holds(shipped) {
		t.Fatalf("Apply after good = %v, %v", w, err)
	}
	if !strings.Contains(Status(r.p), "slot's kernel: "+sha(shipped)) {
		t.Fatalf("status:\n%s", Status(r.p))
	}
}

// A slot's kernel larger than the partition is refused before anything changes for good: the
// state goes back to what it was and the previous kernel stays.
func TestSkylightAKernelThatDoesNotFitIsNotWritten(t *testing.T) {
	old := mkSkyImage(t, "old", skyBootCmdline)
	big := mkSkyImage(t, strings.Repeat("big ", 40000), skyBootCmdline)
	r := newSkyRig(t, old, big)
	// The partition is the old image and 64 KiB; the new one is larger than that.
	if len(big) <= len(r.boot()) {
		t.Fatalf("test image %d bytes, partition %d", len(big), len(r.boot()))
	}
	if w, err := Apply(r.p); w || err == nil {
		t.Fatalf("Apply = %v, %v; want an error", w, err)
	}
	if !r.holds(old) || r.state() != "" {
		t.Fatalf("after a refused write: old there %v, state %q", r.holds(old), r.state())
	}
}

// TestSkylightReleasedImage checks a real boot image when the test is given one: SKYLIGHT_BOOT
// (build.sh's boot-skylight.img) must pass as a slot's kernel, and SKYLIGHT_INSTALLER
// (boot-skylight-installer.img) must not.
func TestSkylightReleasedImage(t *testing.T) {
	boot, inst := os.Getenv("SKYLIGHT_BOOT"), os.Getenv("SKYLIGHT_INSTALLER")
	if boot == "" {
		t.Skip("SKYLIGHT_BOOT not set")
	}
	b, err := os.ReadFile(boot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Skylight.checkShipped(b); err != nil {
		t.Fatalf("the released boot image: %v", err)
	}
	if _, err := Athena.checkShipped(b); err == nil {
		t.Fatal("the Skylight's boot image passed as athena's")
	}
	// SKYLIGHT_RUNNING: a kernel a unit runs now (an earlier build.sh's), which the update replaces.
	if running := os.Getenv("SKYLIGHT_RUNNING"); running != "" {
		old, err := os.ReadFile(running)
		if err != nil {
			t.Fatal(err)
		}
		r := newSkyRig(t, old, b)
		if w, err := Apply(r.p); !w || err != nil || !r.holds(b) {
			t.Fatalf("Apply from the running kernel = %v, %v", w, err)
		}
	}
	if inst != "" {
		i, err := os.ReadFile(inst)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Skylight.checkShipped(i); err == nil {
			t.Fatal("the installer image passed as a slot's kernel")
		}
		im, err := parseImage(i)
		if err != nil || !im.idOK() {
			t.Fatalf("the installer image: %v", err)
		}
	}
}
