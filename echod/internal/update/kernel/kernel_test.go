package kernel

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const page = 2048

// mkImage is a header version 1 boot image like pack.py's: a kernel part holding a tuning slot
// (with table in it, or empty) after some bytes that tell kernels apart, and a small ramdisk.
func mkImage(t *testing.T, tag string, table []byte) []byte {
	t.Helper()
	slot := make([]byte, slotSize)
	copy(slot, slotMagic)
	if len(table) > 0 {
		binary.LittleEndian.PutUint32(slot[8:], uint32(len(table)))
		s := sha256.Sum256(table)
		copy(slot[12:], s[:])
		copy(slot[slotHead:], table)
	}
	k := append(gzKernel(t, tag, "Linux version test "+tag), slot...)
	r := []byte("ramdisk " + tag)
	img := make([]byte, page+pad(len(k), page)+pad(len(r), page))
	copy(img, "ANDROID!")
	binary.LittleEndian.PutUint32(img[8:], uint32(len(k)))
	binary.LittleEndian.PutUint32(img[16:], uint32(len(r)))
	binary.LittleEndian.PutUint32(img[36:], page)
	binary.LittleEndian.PutUint32(img[40:], 1)
	copy(img[page:], k)
	copy(img[page+pad(len(k), page):], r)
	im, err := parseImage(img)
	if err != nil {
		t.Fatal(err)
	}
	copy(img[headerID:], im.computedID())
	return img
}

// gzKernel is a kernel part as athena's are: an arm64 Image (here only its header and a string),
// gzip'd. The tuning slot (in the DTB, in a real one) goes after the gzip stream.
func gzKernel(t *testing.T, tag, text string) []byte {
	t.Helper()
	img := make([]byte, 64)
	copy(img[56:], "ARM\x64")
	img = append(img, []byte(text+" ")...)
	img = append(img, bytes.Repeat([]byte(tag), 300)...)
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	w.Write(img)
	w.Close()
	return b.Bytes()
}

var tableB = bytes.Repeat([]byte{0x01, 0x02}, 600)

type rig struct {
	t *testing.T
	p Paths
}

// newRig is a unit: its boot partition (16 KiB more than the image, as a partition is bigger)
// holding a kernel filled with tableB, and a slot carrying shipped (nil: none).
func newRig(t *testing.T, running, shipped []byte) *rig {
	t.Helper()
	old := knownTables
	knownTables = map[string]string{sha(tableB): "test table"}
	t.Cleanup(func() { knownTables = old })
	d := t.TempDir()
	p := Paths{
		Shipped:    filepath.Join(d, "slot", "boot-athena.img"),
		ShippedSum: filepath.Join(d, "slot", "boot-athena.img.sha256"),
		Boot:       filepath.Join(d, "boot-partition"),
		Dir:        filepath.Join(d, "data", "kernel"),
	}
	part := make([]byte, len(running)+16384)
	copy(part, running)
	for i := len(running); i < len(part); i++ {
		part[i] = 0xee // what was there before: not zeros
	}
	if err := os.WriteFile(p.Boot, part, 0o644); err != nil {
		t.Fatal(err)
	}
	if shipped != nil {
		os.MkdirAll(filepath.Dir(p.Shipped), 0o755)
		os.WriteFile(p.Shipped, shipped, 0o644)
		os.WriteFile(p.ShippedSum, []byte(sha(shipped)+"  boot-athena.img\n"), 0o644)
	}
	return &rig{t, p}
}

func (r *rig) boot() []byte {
	b, err := os.ReadFile(r.p.Boot)
	if err != nil {
		r.t.Fatal(err)
	}
	return b
}

func (r *rig) holds(img []byte) bool { return bytes.HasPrefix(r.boot(), img) }

func (r *rig) state() string {
	b, _ := os.ReadFile(r.p.file("state"))
	return strings.TrimSpace(string(b))
}

func TestApplyWritesTheSlotsKernelWithTheUnitsTuning(t *testing.T) {
	old := mkImage(t, "old", tableB)
	newShipped := mkImage(t, "new", nil)
	r := newRig(t, old, newShipped)

	written, err := Apply(r.p)
	if err != nil || !written {
		t.Fatalf("Apply = %v, %v; want a write", written, err)
	}
	want := mkImage(t, "new", tableB)
	if !r.holds(want) {
		t.Fatal("the boot partition does not hold the new kernel with the unit's tuning")
	}
	if got := r.state(); !strings.HasPrefix(got, "trial 3 "+sha(want)) || !strings.HasSuffix(got, sha(newShipped)) {
		t.Fatalf("state %q", got)
	}
	prev, _ := os.ReadFile(r.p.file("prev.img"))
	if !bytes.Equal(prev, old) {
		t.Fatal("prev.img is not the kernel that was there")
	}
	// On trial: nothing more is written, even by another call.
	if w, err := Apply(r.p); w || err != nil {
		t.Fatalf("second Apply = %v, %v", w, err)
	}
}

func TestApplyLeavesAnUpToDateKernel(t *testing.T) {
	r := newRig(t, mkImage(t, "same", tableB), mkImage(t, "same", nil))
	before := r.boot()
	if w, err := Apply(r.p); w || err != nil {
		t.Fatalf("Apply = %v, %v; want nothing to do", w, err)
	}
	if !bytes.Equal(before, r.boot()) || r.state() != "" {
		t.Fatal("an up-to-date kernel was touched")
	}
}

func TestApplyWithoutAShippedKernelDoesNothing(t *testing.T) {
	r := newRig(t, mkImage(t, "old", tableB), nil)
	if w, err := Apply(r.p); w || err != nil {
		t.Fatalf("Apply = %v, %v", w, err)
	}
}

func TestApplyRefuses(t *testing.T) {
	cases := map[string]func(r *rig){
		"a shipped kernel that does not match its sum": func(r *rig) {
			os.WriteFile(r.p.ShippedSum, []byte(strings.Repeat("0", 64)+"\n"), 0o644)
		},
		"a running kernel with an unknown tuning": func(r *rig) {
			os.WriteFile(r.p.Boot, mkImage(r.t, "old", []byte{9, 9, 9, 9}), 0o644)
		},
		"a running kernel with an empty slot": func(r *rig) {
			os.WriteFile(r.p.Boot, mkImage(r.t, "old", nil), 0o644)
		},
		"a running kernel whose header id is wrong": func(r *rig) {
			b := r.boot()
			b[page+3] ^= 1
			os.WriteFile(r.p.Boot, b, 0o644)
		},
		"a shipped kernel with a filled slot": func(r *rig) {
			s := mkImage(r.t, "new", tableB)
			os.WriteFile(r.p.Shipped, s, 0o644)
			os.WriteFile(r.p.ShippedSum, []byte(sha(s)), 0o644)
		},
	}
	for name, spoil := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, mkImage(t, "old", tableB), mkImage(t, "new", nil))
			spoil(r)
			before := r.boot()
			if w, err := Apply(r.p); w || err == nil {
				t.Fatalf("Apply = %v, %v; want a refusal", w, err)
			}
			if !bytes.Equal(before, r.boot()) {
				t.Fatal("the boot partition was written")
			}
		})
	}
}

func TestTrialSettles(t *testing.T) {
	r := newRig(t, mkImage(t, "old", tableB), mkImage(t, "new", nil))
	if _, err := Apply(r.p); err != nil {
		t.Fatal(err)
	}
	if again, err := Boot(r.p); again || err != nil {
		t.Fatalf("Boot = %v, %v", again, err)
	}
	if !strings.HasPrefix(r.state(), "trial 2 ") {
		t.Fatalf("state %q after a boot", r.state())
	}
	if err := Settled(r.p); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(r.state(), "good ") {
		t.Fatalf("state %q after settling", r.state())
	}
	// Good and up to date: nothing more.
	if w, err := Apply(r.p); w || err != nil {
		t.Fatalf("Apply after good = %v, %v", w, err)
	}
	if again, err := Boot(r.p); again || err != nil {
		t.Fatalf("Boot after good = %v, %v", again, err)
	}
}

func TestTrialThatNeverSettlesPutsThePreviousKernelBack(t *testing.T) {
	old := mkImage(t, "old", tableB)
	shipped := mkImage(t, "new", nil)
	r := newRig(t, old, shipped)
	if _, err := Apply(r.p); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < Tries; i++ {
		if again, err := Boot(r.p); again || err != nil {
			t.Fatalf("boot %d: %v, %v", i, again, err)
		}
	}
	again, err := Boot(r.p)
	if !again || err != nil {
		t.Fatalf("the last boot = %v, %v; want the previous kernel back and a reboot", again, err)
	}
	if !r.holds(old) {
		t.Fatal("the previous kernel is not back")
	}
	if r.state() != "" {
		t.Fatalf("state %q after the restore", r.state())
	}
	// The failed kernel is not written again.
	if w, err := Apply(r.p); w || err != nil {
		t.Fatalf("Apply of a bad kernel = %v, %v", w, err)
	}
	if !r.holds(old) {
		t.Fatal("the bad kernel was written again")
	}
	if !strings.Contains(Status(r.p), "bad: "+sha(shipped)) {
		t.Fatal("status does not name the bad kernel")
	}
}

func TestBootDropsATrialWhoseWriteNeverLanded(t *testing.T) {
	old := mkImage(t, "old", tableB)
	r := newRig(t, old, mkImage(t, "new", nil))
	if _, err := Apply(r.p); err != nil {
		t.Fatal(err)
	}
	// Power went down after the state was written and before the image was (simulated).
	part := r.boot()
	copy(part, old)
	os.WriteFile(r.p.Boot, part, 0o644)
	if again, err := Boot(r.p); again || err != nil {
		t.Fatalf("Boot = %v, %v", again, err)
	}
	if r.state() != "" || !r.holds(old) {
		t.Fatal("the dropped trial changed something")
	}
}

func TestRestoreRefusesADamagedPreviousKernel(t *testing.T) {
	r := newRig(t, mkImage(t, "old", tableB), mkImage(t, "new", nil))
	if _, err := Apply(r.p); err != nil {
		t.Fatal(err)
	}
	prev, _ := os.ReadFile(r.p.file("prev.img"))
	prev[page+5] ^= 1
	os.WriteFile(r.p.file("prev.img"), prev, 0o644)
	if err := r.p.restore(); err == nil {
		t.Fatal("a prev.img that does not match its sum was written")
	}
}

// TestTheInstallersFill checks the fill against the installer's on real images, when the test is
// given them: ATHENA_BOOT (a released image, slot empty), ATHENA_FILLED (what install-athena.py made
// of it for a unit) and ATHENA_STOCK_TABLE_FROM (a boot image holding the same tuning).
func TestTheInstallersFill(t *testing.T) {
	shipped, filled := os.Getenv("ATHENA_BOOT"), os.Getenv("ATHENA_FILLED")
	if shipped == "" || filled == "" {
		t.Skip("ATHENA_BOOT and ATHENA_FILLED not set")
	}
	s, err := os.ReadFile(shipped)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.ReadFile(filled)
	if err != nil {
		t.Fatal(err)
	}
	ship, err := checkShipped(s)
	if err != nil {
		t.Fatalf("the released image: %v", err)
	}
	fi, err := parseImage(f)
	if err != nil || !fi.idOK() {
		t.Fatalf("the installer's image: %v, id ok %v", err, err == nil && fi.idOK())
	}
	table, err := fi.table()
	if err != nil || table == nil {
		t.Fatalf("the installer's tuning: %v", err)
	}
	if _, ok := knownTables[sha(table)]; !ok {
		t.Fatalf("the installer's tuning %s is not a known one", sha(table))
	}
	mine, err := ship.withTable(table)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(mine, f) {
		t.Fatal("the fill differs from the installer's")
	}
	empty, err := fi.withTable(nil)
	if err != nil || !bytes.Equal(empty, s) {
		t.Fatal("emptying the installer's image does not give the released one back")
	}
}

// Sizes near 4 GiB (on the device an int is 32 bits) are refused, not a panic.
func TestHugeSizesAreRefused(t *testing.T) {
	img := mkImage(t, "x", nil)
	for _, at := range []int{8, 16, 24, 1632, 1648} {
		b := bytes.Clone(img)
		binary.LittleEndian.PutUint32(b[at:], 0xffffffff)
		if at == 1648 {
			binary.LittleEndian.PutUint32(b[40:], 2)
		}
		if _, err := parseImage(b); err == nil {
			t.Errorf("a size of 0xffffffff at %d was taken", at)
		}
	}
	b := bytes.Clone(img)
	binary.LittleEndian.PutUint32(b[1632:], 16)
	binary.LittleEndian.PutUint64(b[1636:], 0xfffffffffffffff0)
	if _, err := parseImage(b); err == nil {
		t.Error("a dtbo offset near 2^64 was taken")
	}
}

// A shipped kernel whose empty slot has something left after its zero length is refused, as the
// installer refuses it: it would never be up to date, and would be written after every boot.
func TestAShippedSlotMustBeAllZeros(t *testing.T) {
	s := mkImage(t, "new", nil)
	im, _ := parseImage(s)
	at, _ := im.slotAt()
	s[at+slotHead+10] = 1
	im, _ = parseImage(s)
	copy(s[headerID:], im.computedID())
	if _, err := checkShipped(s); err == nil {
		t.Fatal("a slot with bytes after a zero length was taken for empty")
	}
}

// Committed and still there: nothing more is written, even if the image would not compare equal.
func TestAGoodKernelStillThereIsUpToDate(t *testing.T) {
	r := newRig(t, mkImage(t, "old", tableB), mkImage(t, "new", nil))
	if _, err := Apply(r.p); err != nil {
		t.Fatal(err)
	}
	if err := Settled(r.p); err != nil {
		t.Fatal(err)
	}
	st, _ := r.p.readState()
	if st.kind != "good" || st.length == 0 {
		t.Fatalf("state %+v", st)
	}
	before := r.boot()
	if w, err := Apply(r.p); w || err != nil {
		t.Fatalf("Apply = %v, %v", w, err)
	}
	if !bytes.Equal(before, r.boot()) {
		t.Fatal("a good kernel still on the partition was written again")
	}
}

// A restore that cannot be made ends the trial as failed: no more tries (so no reboots), nothing
// written again, said in the status, the kernel listed bad once.
func TestARestoreThatFailsEndsInFailed(t *testing.T) {
	r := newRig(t, mkImage(t, "old", tableB), mkImage(t, "new", nil))
	if _, err := Apply(r.p); err != nil {
		t.Fatal(err)
	}
	os.Remove(r.p.file("prev.img"))
	for i := 0; i < Tries; i++ {
		Boot(r.p)
	}
	if again, err := Boot(r.p); again || err == nil {
		t.Fatalf("Boot = %v, %v; want an error and no reboot", again, err)
	}
	if !strings.HasPrefix(r.state(), "failed ") {
		t.Fatalf("state %q", r.state())
	}
	if OnTrial(r.p) {
		t.Error("a failed kernel is still on trial")
	}
	for i := 0; i < 3; i++ {
		if again, err := Boot(r.p); again || err != nil {
			t.Fatalf("a boot after the failure = %v, %v", again, err)
		}
	}
	if w, err := Apply(r.p); w || err != nil {
		t.Fatalf("Apply after the failure = %v, %v", w, err)
	}
	b, _ := os.ReadFile(r.p.file("bad"))
	if n := strings.Count(string(b), "\n"); n != 1 {
		t.Errorf("bad has %d lines", n)
	}
	if !strings.Contains(Status(r.p), "FAILED") {
		t.Error("status does not say it failed")
	}
}

// Restore by hand puts the previous kernel back and marks the slot's one bad.
func TestRestoreByHand(t *testing.T) {
	old := mkImage(t, "old", tableB)
	shipped := mkImage(t, "new", nil)
	r := newRig(t, old, shipped)
	if _, err := Apply(r.p); err != nil {
		t.Fatal(err)
	}
	if !OnTrial(r.p) {
		t.Fatal("not on trial after Apply")
	}
	if err := Settled(r.p); err != nil {
		t.Fatal(err)
	}
	if err := Restore(r.p); err != nil {
		t.Fatal(err)
	}
	if !r.holds(old) || r.state() != "" || !r.p.bad()[sha(shipped)] {
		t.Fatalf("after Restore: old back %v, state %q, bad %v", r.holds(old), r.state(), r.p.bad())
	}
}

// A state file that does not say what this writes is an error, not a number taken on trust.
func TestAStrangeStateIsAnError(t *testing.T) {
	r := newRig(t, mkImage(t, "old", tableB), mkImage(t, "new", nil))
	z := strings.Repeat("a", 64)
	for _, s := range []string{
		"trial -1 " + z + " 100 " + z, "trial 2 " + z + " -5 " + z, "trial 9 " + z + " 100 " + z,
		"trial 2 xyz 100 " + z, "good " + z + " 0 " + z, "nonsense",
	} {
		os.MkdirAll(r.p.Dir, 0o755)
		os.WriteFile(r.p.file("state"), []byte(s+"\n"), 0o644)
		if _, err := r.p.readState(); err == nil {
			t.Errorf("%q was read", s)
		}
		if _, err := Boot(r.p); err == nil {
			t.Errorf("Boot on %q went on", s)
		}
	}
	os.WriteFile(r.p.file("state"), []byte("good "+z+" "+z+"\n"), 0o644)
	if st, err := r.p.readState(); err != nil || st.kind != "good" {
		t.Errorf("the old good form: %+v, %v", st, err)
	}
}

// failTrial runs a trial out: Tries boots, the first noRoutes of them with everything but a network,
// then the boot that finds no tries left.
func (r *rig) failTrial(noRoutes int) (again bool, err error) {
	r.t.Helper()
	if _, err := Apply(r.p); err != nil {
		r.t.Fatal(err)
	}
	for i := 0; i < Tries; i++ {
		if again, err := Boot(r.p); again || err != nil {
			r.t.Fatalf("boot %d: %v, %v", i, again, err)
		}
		if i < noRoutes {
			if err := NoRoute(r.p); err != nil {
				r.t.Fatal(err)
			}
		}
	}
	return Boot(r.p)
}

func TestATrialWithoutANetworkOnEveryTryIsPutBackButNotMarkedBad(t *testing.T) {
	clock := time.Date(2026, 10, 16, 6, 0, 0, 0, time.UTC)
	defer func(old func() time.Time) { now = old }(now)
	now = func() time.Time { return clock }
	old := mkImage(t, "old", tableB)
	shipped := mkImage(t, "new", nil)
	r := newRig(t, old, shipped)
	again, err := r.failTrial(Tries)
	if !again || err != nil {
		t.Fatalf("the last boot = %v, %v; want the previous kernel back and a reboot", again, err)
	}
	if !r.holds(old) {
		t.Fatal("the previous kernel is not back: one that breaks Wi-Fi would keep the unit off the network")
	}
	if strings.Contains(Status(r.p), "bad: ") {
		t.Fatal("a kernel that only never had a network was marked bad")
	}
	// Not straight away: the kernel before it has the network back within minutes, and writing the
	// same kernel again then would take the unit off the network again and again.
	clock = clock.Add(RetryAfter - time.Hour)
	if w, err := Apply(r.p); w || err != nil {
		t.Fatalf("Apply within RetryAfter = %v, %v", w, err)
	}
	if !r.holds(old) {
		t.Fatal("written again within RetryAfter")
	}
	clock = clock.Add(time.Hour)
	if w, err := Apply(r.p); !w || err != nil {
		t.Fatalf("Apply after RetryAfter = %v, %v; want it written again", w, err)
	}
	if !strings.HasPrefix(r.state(), "trial 3 ") {
		t.Fatalf("state %q: want a new trial", r.state())
	}
	if _, err := os.Stat(r.p.file("noroute")); !os.IsNotExist(err) {
		t.Fatal("the new trial starts with the last one's boots without a network")
	}
	// This time it settles, and nothing about the network is left over.
	if again, err := Boot(r.p); again || err != nil {
		t.Fatal(again, err)
	}
	if err := Settled(r.p); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"noroute", "retry"} {
		if _, err := os.Stat(r.p.file(f)); !os.IsNotExist(err) {
			t.Fatalf("%s left after the kernel settled", f)
		}
	}
}

func TestATrialWithAnyOtherFailureIsMarkedBad(t *testing.T) {
	old := mkImage(t, "old", tableB)
	shipped := mkImage(t, "new", nil)
	r := newRig(t, old, shipped)
	// One boot of the three ended some other way (it never reached the trial timeout with only the
	// network missing: a crash, a hang, missing hardware).
	again, err := r.failTrial(Tries - 1)
	if !again || err != nil {
		t.Fatalf("the last boot = %v, %v", again, err)
	}
	if !r.holds(old) {
		t.Fatal("the previous kernel is not back")
	}
	if !strings.Contains(Status(r.p), "bad: "+sha(shipped)) {
		t.Fatal("not marked bad")
	}
	if _, err := os.Stat(r.p.file("retry")); !os.IsNotExist(err) {
		t.Fatal("a bad kernel is down to be retried")
	}
}

func TestNoRouteOutsideATrialDoesNothing(t *testing.T) {
	r := newRig(t, mkImage(t, "old", tableB), mkImage(t, "new", nil))
	if err := NoRoute(r.p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(r.p.file("noroute")); !os.IsNotExist(err) {
		t.Fatal("counted a boot without a network with no kernel on trial")
	}
}

func TestARetryPutBackBeforeTheClockWasSetWaitsADayFromWhenItIs(t *testing.T) {
	// athena's RTC has no battery: early in a boot, before NTP, the clock says 1970.
	clock := time.Date(1970, 1, 1, 0, 0, 30, 0, time.UTC)
	defer func(old func() time.Time) { now = old }(now)
	now = func() time.Time { return clock }
	old := mkImage(t, "old", tableB)
	r := newRig(t, old, mkImage(t, "new", nil))
	if again, err := r.failTrial(Tries); !again || err != nil {
		t.Fatal(again, err)
	}
	if b, _ := os.ReadFile(r.p.file("retry")); !strings.HasSuffix(strings.TrimSpace(string(b)), " 0") {
		t.Fatalf("retry %q: want the time left at 0 with the clock unset", b)
	}
	// Still unset when the old kernel settles: held, whatever the clock says.
	if w, err := Apply(r.p); w || err != nil {
		t.Fatalf("Apply with the clock unset = %v, %v", w, err)
	}
	// Set: the day starts now.
	clock = time.Date(2026, 10, 16, 6, 0, 0, 0, time.UTC)
	if w, err := Apply(r.p); w || err != nil {
		t.Fatalf("Apply once the clock is set = %v, %v; want it held", w, err)
	}
	clock = clock.Add(RetryAfter - time.Minute)
	if w, err := Apply(r.p); w || err != nil {
		t.Fatalf("Apply a minute before the day is up = %v, %v", w, err)
	}
	if !r.holds(old) {
		t.Fatal("written again too soon")
	}
	clock = clock.Add(time.Minute)
	if w, err := Apply(r.p); !w || err != nil {
		t.Fatalf("Apply a day after the clock was set = %v, %v; want it written", w, err)
	}
}

func TestARetryWithTheClockGoneBackIsHeldADayFromNow(t *testing.T) {
	clock := time.Date(2026, 10, 16, 6, 0, 0, 0, time.UTC)
	defer func(old func() time.Time) { now = old }(now)
	now = func() time.Time { return clock }
	r := newRig(t, mkImage(t, "old", tableB), mkImage(t, "new", nil))
	if again, err := r.failTrial(Tries); !again || err != nil {
		t.Fatal(again, err)
	}
	clock = clock.Add(-48 * time.Hour)
	if w, err := Apply(r.p); w || err != nil {
		t.Fatalf("Apply with the clock behind the put-back = %v, %v; want it held", w, err)
	}
	clock = clock.Add(RetryAfter)
	if w, err := Apply(r.p); !w || err != nil {
		t.Fatalf("Apply a day later = %v, %v; want it written", w, err)
	}
}

func TestNoRouteCountsABootOnce(t *testing.T) {
	old := mkImage(t, "old", tableB)
	r := newRig(t, old, mkImage(t, "new", nil))
	if _, err := Apply(r.p); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < Tries; i++ {
		if again, err := Boot(r.p); again || err != nil {
			t.Fatal(again, err)
		}
		// The first boot reports twice; the third not at all (it ended some other way).
		if i == 0 {
			for range 2 {
				if err := NoRoute(r.p); err != nil {
					t.Fatal(err)
				}
			}
		} else if i == 1 {
			if err := NoRoute(r.p); err != nil {
				t.Fatal(err)
			}
		}
	}
	if n, _ := r.p.noRoutes(sha(mkImage(t, "new", nil))); n != 2 {
		t.Fatalf("%d boots counted without a network, want 2", n)
	}
	if again, err := Boot(r.p); !again || err != nil {
		t.Fatal(again, err)
	}
	if !strings.Contains(Status(r.p), "bad: ") {
		t.Fatal("a double report made a boot that failed otherwise count as one without a network")
	}
}
