// Package kernel updates the kernel of a unit that runs TECHO5's own mainline kernel (the 2nd gen
// Show 8, the Skylight Calendar 15) with the root filesystem.
//
// athena boots TECHO5's mainline kernel from its boot partition, which an update of the system slots
// never touched: a kernel fix reached a unit only through the installer. So the athena rootfs carries
// the kernel it was tested with (the released boot image, its tuning slot empty), and once that slot
// has proved itself (committed good) the kernel goes to the boot partition the way the installer puts
// it there: the unit's own amp tuning, taken from the boot partition as it is, in the slot, the header
// id made again.
//
// A new kernel is on trial, as a new slot is: it has three boots to settle, which boot.sh decides (the
// daemon up five minutes, with a network and the hardware that comes up late). Every boot takes a try
// (Boot); one that settles commits it (Settled). One that runs out of tries puts the previous boot
// image back, which is kept for that, and is not written again. A kernel that never gets as far as
// boot.sh (a hang or a panic before it, an /init that cannot find the store) cannot be caught here;
// the only way back from one is the way the unit was installed (mtkclient), so kernels are tested on
// units before they are released.
//
// The Skylight's kernel (Board Skylight) is the same story in another partition: the stock U-Boot
// starts the recovery partition (misc says boot-recovery), and that is where TECHO5's kernel lives.
// Nothing of the unit's own is in it, so the slot's image is written as it is, and only over an image
// that is TECHO5's Skylight kernel already: never over Android's recovery, never as the installer
// image. A kernel on trial that reaches the store's /init but cannot hand over to a slot is put back
// by that /init (tools/linux/skylight/init). Recovery is the only partition the stock U-Boot starts
// TECHO5 from, and the trial kernel is written over the previous one there, so two cases have no way
// back from the unit itself: a power cut during writeBoot (recovery holds part of each image), and a
// kernel that panics before /init (panic=10 restarts it for good). Both need Loader mode
// (docs/install-skylight.md). A spare partition with a one-shot boot from misc would close that
// window, but it rests on U-Boot behavior no unit has tried.
//
// Except when every try failed for want of a network alone: the daemon settled and the hardware was
// there, but there was never a default route (NoRoute, from boot.sh). That may be the kernel's Wi-Fi,
// or only the router being down, which says nothing about the kernel. Its tries are used up and the
// previous kernel is put back all the same, since a kernel that breaks Wi-Fi would otherwise leave
// the unit unreachable for good; but it is not marked bad, and is written again RetryAfter later.
//
// The state is a directory on the data partition, which every slot shares:
//
//	state       trial <tries> <written sha256> <written length> <shipped sha256>
//	            | good <written sha256> <written length> <shipped sha256>
//	            | failed <shipped sha256>   (the previous kernel could not be put back: nothing more
//	                                         is done until someone looks; Restore, or the installer)
//	prev.img    the kernel partition's image before the last write (its own length)
//	prev.sha256 its sha256
//	bad         shipped kernels that failed their trial, one sha256 a line
//	noroute     <shipped sha256> <n> <tries>: boots of the kernel on trial that had everything but a
//	            network, and the tries left at the last one counted (a boot is counted once)
//	retry       <shipped sha256> <unix time>: a kernel put back for want of a network alone, and when
//	            (0: before the clock was set; the first Apply with a set clock puts the time in)
package kernel

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Tries is how many boots a new kernel has to settle in, as slotctl gives a new slot.
const Tries = 3

// RetryAfter is how long a kernel put back for want of a network alone waits before it is written
// again. Not sooner: one whose Wi-Fi is broken would take the unit off the network for its three
// tries every time, and the kernel before it brings the network back within minutes.
const RetryAfter = 24 * time.Hour

// now is the clock; tests set their own.
var now = time.Now

// Board is whose kernel it is: which partition holds it, and how the slot's image becomes the one
// written there.
type Board int

const (
	// Athena is the 2nd gen Show 8: the boot partition, the slot's image filled with the unit's own amp
	// tuning from the image there.
	Athena Board = iota
	// Skylight is the Skylight Calendar 15: the recovery partition, the slot's image as it is.
	Skylight
)

// Partition is the name of the eMMC partition the board's kernel is in.
func (b Board) Partition() string {
	if b == Skylight {
		return "recovery"
	}
	return "boot"
}

// Paths are where everything is. Default and SkylightDefault are the units'; tests use their own.
type Paths struct {
	Board      Board
	Shipped    string // the slot's boot image, as released (athena's: its tuning slot empty)
	ShippedSum string // its sha256, hex (sha256sum's format is taken too)
	Boot       string // the partition the kernel is in (Board.Partition)
	Dir        string // the state directory
}

// Default are athena's paths. Boot is found by its partition name at run time (PartitionDevice).
var Default = Paths{
	Board:      Athena,
	Shipped:    "/usr/share/techo5/kernel/boot-athena.img",
	ShippedSum: "/usr/share/techo5/kernel/boot-athena.img.sha256",
	Dir:        "/data/techo5-linux/kernel",
}

// SkylightDefault are the Skylight's, with the same state directory: OnTrial reads either.
var SkylightDefault = Paths{
	Board:      Skylight,
	Shipped:    "/usr/share/techo5/kernel/boot-skylight.img",
	ShippedSum: "/usr/share/techo5/kernel/boot-skylight.img.sha256",
	Dir:        Default.Dir,
}

// Log takes the lines worth keeping in the boot log.
var Log = func(format string, a ...any) { fmt.Fprintf(os.Stderr, "kernel: "+format+"\n", a...) }

type state struct {
	kind    string // "", "trial", "good" or "failed"
	tries   int
	written string
	length  int
	shipped string
}

// OnTrial is whether a kernel is on trial now: a system update waits until it is over, so that a bad
// kernel does not take a new slot down with it (update.installRootfs).
func OnTrial(p Paths) bool {
	s, err := p.readState()
	return err == nil && s.kind == "trial"
}

func (p Paths) file(name string) string { return filepath.Join(p.Dir, name) }

func isSum(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

func (p Paths) readState() (state, error) {
	b, err := os.ReadFile(p.file("state"))
	if errors.Is(err, os.ErrNotExist) {
		return state{}, nil
	}
	if err != nil {
		return state{}, err
	}
	f := strings.Fields(string(b))
	num := func(s string, lo, hi int) (int, bool) {
		n, err := strconv.Atoi(s)
		return n, err == nil && n >= lo && n <= hi
	}
	const maxLen = 64 << 20
	switch {
	case len(f) == 5 && f[0] == "trial":
		t, ok1 := num(f[1], 0, Tries)
		n, ok2 := num(f[3], 1, maxLen)
		if ok1 && ok2 && isSum(f[2]) && isSum(f[4]) {
			return state{kind: "trial", tries: t, written: f[2], length: n, shipped: f[4]}, nil
		}
	case len(f) == 4 && f[0] == "good":
		n, ok := num(f[2], 1, maxLen)
		if ok && isSum(f[1]) && isSum(f[3]) {
			return state{kind: "good", written: f[1], length: n, shipped: f[3]}, nil
		}
	case len(f) == 3 && f[0] == "good" && isSum(f[1]) && isSum(f[2]):
		// Written before good carried the length (the first test builds): no shortcut in Apply.
		return state{kind: "good", written: f[1], shipped: f[2]}, nil
	case len(f) == 2 && f[0] == "failed" && isSum(f[1]):
		return state{kind: "failed", shipped: f[1]}, nil
	}
	return state{}, fmt.Errorf("state file says %q", strings.TrimSpace(string(b)))
}

func (p Paths) writeState(s state) error {
	var line string
	switch s.kind {
	case "trial":
		line = fmt.Sprintf("trial %d %s %d %s", s.tries, s.written, s.length, s.shipped)
	case "good":
		line = fmt.Sprintf("good %s %d %s", s.written, s.length, s.shipped)
	case "failed":
		line = "failed " + s.shipped
	default:
		err := os.Remove(p.file("state"))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err == nil {
			syncDir(p.Dir)
		}
		return err
	}
	return writeFile(p.file("state"), []byte(line+"\n"))
}

// writeFile writes atomically and durably: a temporary file, synced, renamed over the old one, and
// the directory synced, so that a power cut leaves the old file or the new one.
func writeFile(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	syncDir(filepath.Dir(path))
	return nil
}

func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
}

func (p Paths) bad() map[string]bool {
	m := map[string]bool{}
	b, _ := os.ReadFile(p.file("bad"))
	for _, l := range strings.Fields(string(b)) {
		m[l] = true
	}
	return m
}

func (p Paths) markBad(shipped string) error {
	if shipped == "" || p.bad()[shipped] {
		return nil
	}
	b, _ := os.ReadFile(p.file("bad"))
	return writeFile(p.file("bad"), append(b, []byte(shipped+"\n")...))
}

// readBoot is the whole partition the kernel is in.
func (p Paths) readBoot() ([]byte, error) {
	f, err := os.Open(p.Boot)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	dropCache(f)
	return io.ReadAll(f)
}

// writeBoot writes img at the start of the kernel's partition, syncs, and reads it back past the cache.
// On the Skylight this overwrites the only kernel the unit boots: a power cut before the sync leaves
// it needing Loader mode (see the package comment).
func (p Paths) writeBoot(img []byte) error {
	f, err := os.OpenFile(p.Boot, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	if size, err := f.Seek(0, io.SeekEnd); err != nil {
		return err
	} else if int64(len(img)) > size {
		return fmt.Errorf("%d bytes do not fit the %d-byte %s partition", len(img), size, p.Board.Partition())
	}
	if _, err := f.WriteAt(img, 0); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	dropCache(f)
	back := make([]byte, len(img))
	if _, err := f.ReadAt(back, 0); err != nil {
		return err
	}
	if !bytes.Equal(back, img) {
		return fmt.Errorf("the %s partition does not read back what was written", p.Board.Partition())
	}
	return nil
}

// shipped is the slot's kernel, checked against its sum and as an image. ok false: this slot carries
// none.
func (p Paths) shipped() (img []byte, sum string, ok bool, err error) {
	img, err = os.ReadFile(p.Shipped)
	if errors.Is(err, os.ErrNotExist) {
		return nil, "", false, nil
	}
	if err != nil {
		return nil, "", false, err
	}
	s, err := os.ReadFile(p.ShippedSum)
	if err != nil {
		return nil, "", false, fmt.Errorf("the slot's kernel has no sum: %w", err)
	}
	want := strings.Fields(string(s))
	sum = sha(img)
	if len(want) == 0 || !strings.EqualFold(want[0], sum) {
		return nil, "", false, fmt.Errorf("the slot's kernel is %s, its sum says %s", sum, strings.TrimSpace(string(s)))
	}
	if _, err := p.Board.checkShipped(img); err != nil {
		return nil, "", false, fmt.Errorf("the slot's kernel: %w", err)
	}
	return img, sum, true, nil
}

// sumNumbers reads a "<sha256> <number>..." file holding want numbers; ok false when it is missing,
// unreadable, of another shape or for another kernel.
func (p Paths) sumNumbers(name, shipped string, want int) (n []int64, ok bool) {
	b, err := os.ReadFile(p.file(name))
	if err != nil {
		return nil, false
	}
	f := strings.Fields(string(b))
	if len(f) != want+1 || f[0] != shipped {
		return nil, false
	}
	for _, x := range f[1:] {
		v, err := strconv.ParseInt(x, 10, 64)
		if err != nil {
			return nil, false
		}
		n = append(n, v)
	}
	return n, true
}

// noRoutes is how many boots of the kernel on trial had everything but a network, and the tries left
// at the last of them (-1 for none).
func (p Paths) noRoutes(shipped string) (n int64, tries int64) {
	if v, ok := p.sumNumbers("noroute", shipped, 2); ok {
		return v[0], v[1]
	}
	return 0, -1
}

// clockSet is whether t comes from a clock that has been set: athena's RTC has no battery, and early
// in a boot, before NTP, the clock can say 1970.
func clockSet(t time.Time) bool { return t.Year() >= 2020 }

func (p Paths) removeFile(name string) {
	if err := os.Remove(p.file(name)); err == nil {
		syncDir(p.Dir)
	}
}

// NoRoute says this boot of the kernel on trial had everything it needs to settle but a network: the
// daemon up, the hardware there, and never a default route. The boot has used its try all the same.
func NoRoute(p Paths) error {
	s, err := p.readState()
	if err != nil || s.kind != "trial" {
		return err
	}
	n, tries := p.noRoutes(s.shipped)
	if tries == int64(s.tries) {
		return nil // this boot is counted already
	}
	n++
	Log("this boot of the kernel on trial had no network, and nothing else wrong (%d so far)", n)
	return writeFile(p.file("noroute"), []byte(fmt.Sprintf("%s %d %d\n", s.shipped, n, s.tries)))
}

// Status says what the state is, for people.
func Status(p Paths) string {
	s, err := p.readState()
	var b strings.Builder
	switch {
	case err != nil:
		fmt.Fprintf(&b, "state: unreadable: %v\n", err)
	case s.kind == "trial":
		fmt.Fprintf(&b, "state: on trial, %d boots left; written %s, shipped %s\n", s.tries, s.written, s.shipped)
	case s.kind == "good":
		fmt.Fprintf(&b, "state: good; written %s, shipped %s\n", s.written, s.shipped)
	case s.kind == "failed":
		fmt.Fprintf(&b, "state: FAILED: kernel %s failed its trial and the previous one could not be put back; "+
			"nothing more is written (restore, or the installer)\n", s.shipped)
	default:
		b.WriteString("state: none (no kernel written by an update)\n")
	}
	if _, sum, ok, err := p.shipped(); err != nil {
		fmt.Fprintf(&b, "slot's kernel: %v\n", err)
	} else if ok {
		fmt.Fprintf(&b, "slot's kernel: %s\n", sum)
	} else {
		b.WriteString("slot's kernel: none\n")
	}
	for s := range p.bad() {
		fmt.Fprintf(&b, "bad: %s\n", s)
	}
	for _, name := range []string{"noroute", "retry"} {
		if f, err := os.ReadFile(p.file(name)); err == nil {
			fmt.Fprintf(&b, "%s: %s\n", name, strings.TrimSpace(string(f)))
		}
	}
	return b.String()
}

// Boot runs early at every boot. A kernel on trial takes a try; one with none left is replaced by
// the previous image and marked bad, and reboot says to reboot into it.
func Boot(p Paths) (reboot bool, err error) {
	s, err := p.readState()
	if err != nil || s.kind != "trial" {
		return false, err
	}
	boot, err := p.readBoot()
	if err != nil {
		return false, err
	}
	if len(boot) < s.length || sha(boot[:s.length]) != s.written {
		// The state went down before the write, and the write never happened (or the image was
		// replaced since by other means): there is nothing on trial.
		Log("the %s partition does not hold the kernel on trial; the trial is dropped", p.Board.Partition())
		return false, p.writeState(state{})
	}
	if s.tries > 0 {
		s.tries--
		Log("the kernel is on trial, %d boots left after this one", s.tries)
		return false, p.writeState(s)
	}
	n, _ := p.noRoutes(s.shipped)
	netOnly := n >= Tries
	if netOnly {
		Log("the kernel used all its tries without a network and nothing else wrong; putting the previous one back, "+
			"and this one is tried again in %v", RetryAfter)
	} else {
		Log("the kernel used all its tries without settling; putting the previous one back")
		if err := p.markBad(s.shipped); err != nil {
			return false, err
		}
	}
	if err := p.restore(); err != nil {
		Log("THE PREVIOUS KERNEL COULD NOT BE PUT BACK: %v; leaving the %s partition as it is", err, p.Board.Partition())
		return false, errors.Join(err, p.writeState(state{kind: "failed", shipped: s.shipped}))
	}
	if netOnly {
		// This runs early in the boot, often before the clock is set: then the time is left for
		// Apply to put in.
		var at int64
		if t := now(); clockSet(t) {
			at = t.Unix()
		}
		if err := p.stampRetry(s.shipped, at); err != nil {
			return false, err
		}
	}
	p.removeFile("noroute")
	return true, p.writeState(state{})
}

func (p Paths) stampRetry(shipped string, at int64) error {
	return writeFile(p.file("retry"), []byte(fmt.Sprintf("%s %d\n", shipped, at)))
}

// Settled commits a kernel on trial: it has run long enough, with what it drives working.
func Settled(p Paths) error {
	s, err := p.readState()
	if err != nil || s.kind != "trial" {
		return err
	}
	Log("the kernel settled; it is good")
	if err := p.writeState(state{kind: "good", written: s.written, length: s.length, shipped: s.shipped}); err != nil {
		return err
	}
	p.removeFile("noroute")
	p.removeFile("retry")
	return nil
}

// Apply writes the slot's kernel to its partition when that holds another one. reboot says it was
// written, and the unit should boot into it now. Nothing is written while a kernel is on trial or
// after a failed restore, for a kernel that failed, or when the partition does not hold what this
// board's installer put there: on athena, a tuning the installer knows; on the Skylight, TECHO5's own
// Skylight kernel.
func Apply(p Paths) (reboot bool, err error) {
	s, err := p.readState()
	if err != nil {
		return false, err
	}
	if s.kind == "trial" || s.kind == "failed" {
		return false, nil
	}
	img, sum, ok, err := p.shipped()
	if err != nil || !ok {
		return false, err
	}
	if p.bad()[sum] {
		Log("the slot's kernel %s failed its trial before; not written again", sum)
		return false, nil
	}
	if v, ok := p.sumNumbers("retry", sum, 1); ok {
		t := now()
		at := time.Unix(v[0], 0)
		since := t.Sub(at)
		switch {
		case !clockSet(t):
			Log("the slot's kernel %s had no network on any of its tries; it waits for the clock", sum)
			return false, nil
		case v[0] == 0 || !clockSet(at) || since < 0:
			// Put back before the clock was set, or with one that has since gone back: the day
			// starts now.
			Log("the slot's kernel %s had no network on any of its tries; it is tried again in %v", sum, RetryAfter)
			return false, p.stampRetry(sum, t.Unix())
		case since < RetryAfter:
			Log("the slot's kernel %s had no network on any of its tries; it is tried again in %v",
				sum, (RetryAfter - since).Round(time.Minute))
			return false, nil
		}
	}
	part, err := p.readBoot()
	if err != nil {
		return false, err
	}
	// The kernel this wrote and committed is still there: up to date, whatever else holds.
	if s.kind == "good" && s.shipped == sum && s.length > 0 && len(part) >= s.length && sha(part[:s.length]) == s.written {
		return false, nil
	}
	partName := p.Board.Partition()
	cur, err := parseImage(part)
	if err != nil {
		return false, fmt.Errorf("the %s partition: %w", partName, err)
	}
	if !cur.idOK() {
		return false, fmt.Errorf("the %s partition's header id does not match its contents; not touching it", partName)
	}
	filled, what, err := p.Board.toWrite(cur, img)
	if err != nil || filled == nil {
		return false, err // nil and no error: up to date
	}
	if f, err := parseImage(filled); err != nil || !f.idOK() {
		return false, errors.New("the kernel to write does not check out")
	}
	prev := part[:cur.extent]
	if err := writeFile(p.file("prev.img"), prev); err != nil {
		return false, fmt.Errorf("keeping the previous kernel: %w", err)
	}
	if err := writeFile(p.file("prev.sha256"), []byte(sha(prev)+"\n")); err != nil {
		return false, err
	}
	trial := state{kind: "trial", tries: Tries, written: sha(filled), length: len(filled), shipped: sum}
	if err := p.writeState(trial); err != nil {
		return false, err
	}
	// A new trial counts its own boots without a network.
	p.removeFile("noroute")
	p.removeFile("retry")
	// Everything about the trial is on the disk before the kernel's partition changes.
	syncAll()
	Log("writing the slot's kernel %s into %s%s", sum, partName, what)
	if err := p.writeBoot(filled); err != nil {
		Log("the write failed (%v); putting the previous kernel back", err)
		if rerr := p.restore(); rerr != nil {
			Log("THE PREVIOUS KERNEL COULD NOT BE PUT BACK: %v", rerr)
			return false, errors.Join(fmt.Errorf("writing the kernel: %w", err),
				fmt.Errorf("putting the previous one back: %w", rerr),
				p.writeState(state{kind: "failed", shipped: sum}))
		}
		return false, errors.Join(fmt.Errorf("writing the kernel: %w", err), p.writeState(s))
	}
	return true, nil
}

// Restore puts the kept previous kernel back by hand (from the USB shell, say): a kernel that settled
// but does not work. The kernel the slot carries is marked bad, so it is not written again.
func Restore(p Paths) error {
	s, err := p.readState()
	if err != nil {
		return err
	}
	if err := p.restore(); err != nil {
		return err
	}
	if s.shipped != "" {
		if err := p.markBad(s.shipped); err != nil {
			return err
		}
	} else if _, sum, ok, err := p.shipped(); err == nil && ok {
		if err := p.markBad(sum); err != nil {
			return err
		}
	}
	return p.writeState(state{})
}

// restore writes the kept previous image back, after checking it is the one kept.
func (p Paths) restore() error {
	prev, err := os.ReadFile(p.file("prev.img"))
	if err != nil {
		return fmt.Errorf("the previous kernel: %w", err)
	}
	want, _ := os.ReadFile(p.file("prev.sha256"))
	if f := strings.Fields(string(want)); len(f) == 0 || f[0] != sha(prev) {
		return errors.New("the kept previous kernel does not match its sum; not writing it")
	}
	if im, err := parseImage(prev); err != nil || !im.idOK() {
		return errors.New("the kept previous kernel is not a whole boot image; not writing it")
	}
	return p.writeBoot(prev)
}

// toWrite is what goes into the board's partition for the slot's kernel img (checked by
// checkShipped), given the image there now (parsed, its id right): nil when that is up to date, and
// a few words for the log on what was done to it.
func (b Board) toWrite(cur *image, img []byte) (out []byte, what string, err error) {
	if b == Skylight {
		// Only over TECHO5's own Skylight kernel: anything else there (Android's recovery, the
		// installer image) means the unit is not as an install left it.
		if !skylightBoot(cur) {
			return nil, "", errors.New("the recovery partition does not hold TECHO5's Skylight kernel; not touching it")
		}
		if bytes.Equal(cur.data[:cur.extent], img) {
			return nil, "", nil
		}
		return img, "", nil
	}
	table, err := cur.table()
	if err != nil {
		return nil, "", fmt.Errorf("the boot partition's amp tuning: %w", err)
	}
	if table == nil {
		return nil, "", errors.New("the boot partition's amp tuning slot is empty; run the installer")
	}
	name, known := knownTables[sha(table)]
	if !known {
		return nil, "", fmt.Errorf("the boot partition's amp tuning (%s) is not one the installer knows", sha(table))
	}
	if same, err := cur.withTable(nil); err == nil && bytes.Equal(same, img) {
		return nil, "", nil
	}
	ship, _ := parseImage(img)
	filled, err := ship.withTable(table)
	if err != nil {
		return nil, "", err
	}
	return filled, fmt.Sprintf(" with this unit's amp tuning (%s)", name), nil
}
