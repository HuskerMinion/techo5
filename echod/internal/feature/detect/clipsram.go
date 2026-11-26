package detect

// Wake clips waiting in RAM while they are sent (#140).
//
// A clip of a kind that is sent to a trainer is only waiting on the device: the trainer is where it is
// kept. Written to eMMC it would cost the flash 128 kB, and a sent clip's sidecar rewritten as it went
// another write, for a copy nobody needs once it has gone. eMMC wears with what is written to it. So
// while sending is on, a new clip of a kind that is sent waits in a folder on a tmpfs (ram) and is let
// go of once the trainer has it. Everything else is kept on eMMC as before: the kinds not sent (nothing
// else takes them off the device), every clip while sending is off, and the takes of a Record wake word
// run, which are few and asked for.
//
// RAM is not where a clip is safe, so a clip is moved to eMMC, to wait there as clips always did, when
// it has waited ramWait (the trainer cannot be reached, or will not take it), when the folder is full
// (ramCap), when its kind is no longer sent or sending is turned off, and when the daemon stops, so a
// reboot keeps it. A power cut loses what was waiting in RAM: at most ramWait of clips, of kinds that
// were being sent anyway. The counts, the actions, the unsent count and Keep wake clips going off all
// see both folders.
//
// Only where the folder is on a tmpfs: on Fire OS and on a panel there is no /run, and clips are kept
// on the device as they always were.

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/HuskerMinion/techo5/echod/internal/lib/rootonly"
)

const (
	// ramWait is the longest a clip waits in RAM to be sent before it is moved to eMMC. A trainer that is
	// up takes a clip within seconds; one that is down for half an hour is down for longer, and the clips
	// are then safer on eMMC.
	ramWait = 30 * time.Minute

	// clipBytes is about what a clip takes: four seconds at 16 kHz, 16-bit, and its sidecar.
	clipBytes = 2*clipRate*clipSeconds + 1024
)

// tmpfsMagic is TMPFS_MAGIC from the kernel's magic.h.
const tmpfsMagic = 0x01021994

// ramDirFor is dir if it is on a tmpfs, made if need be; "" otherwise.
//
// The folder is used only if it is what root would have made (rootonly): /run/techo5 is root's and
// nobody else can write there, so nothing else can have made it first, but the clips and what is read
// back from it are the house's voices, and a folder another user made, or a link, would be theirs.
func ramDirFor(dir string) string {
	var st unix.Statfs_t
	if err := unix.Statfs(filepath.Dir(dir), &st); err != nil || int64(st.Type) != tmpfsMagic {
		return ""
	}
	if !rootonly.MkdirAll(dir, 0o700) {
		return ""
	}
	return dir
}

// ramCapFor is how much the clips waiting in RAM may take on a board with total bytes of memory: a
// sixty-fourth of it, from 4 MB to 16 MB. 8 MB (about 60 clips) on a board of 512 MB, 16 MB (about 120)
// on a board of 1 GB or more.
func ramCapFor(total int64) int64 {
	return min(max(total/64, 4<<20), 16<<20)
}

// memTotal is the board's memory in bytes, from /proc/meminfo; 0 when it cannot be read.
func memTotal() int64 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "MemTotal:"); ok {
			kb, _ := strconv.ParseInt(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(v), "kB")), 10, 64)
			return kb << 10
		}
	}
	return 0
}

// dirs is where clips are: eMMC, and the folder in RAM when there is one.
func (c *clips) dirs() []string {
	if c.ram == "" {
		return []string{c.dir}
	}
	return []string{c.dir, c.ram}
}

// glob is the files of a kind matching pattern, in every folder.
func (c *clips) glob(kind, pattern string) []string {
	var out []string
	for _, d := range c.dirs() {
		files, _ := filepath.Glob(filepath.Join(d, kind, pattern))
		out = append(out, files...)
	}
	return out
}

// inRAM is whether a clip's file is in the folder in RAM.
func (c *clips) inRAM(path string) bool {
	return c.ram != "" && strings.HasPrefix(path, c.ram+string(filepath.Separator))
}

// ramUsed is what the clips waiting in RAM take. Held with disk.
func (c *clips) ramUsed() int64 {
	if c.ram == "" {
		return 0
	}
	var n int64
	for _, k := range clipKinds {
		files, _ := filepath.Glob(filepath.Join(c.ram, k, "*"))
		for _, f := range files {
			if fi, err := os.Stat(f); err == nil {
				n += fi.Size()
			}
		}
	}
	return n
}

// ramFull is whether another clip would not fit in RAM. Held with disk.
func (c *clips) ramFull() bool { return c.ramUsed()+clipBytes > c.ramCap }

// baseFor is the folder a new clip of kind is written under: RAM while it is to be sent and there is
// room, eMMC otherwise. Held with disk.
func (c *clips) baseFor(kind string) string {
	if c.ram == "" || c.waits == nil || !c.waits(kind) || c.ramFull() {
		return c.dir
	}
	return c.ram
}

// spill moves the clips waiting in RAM that move says should go to eMMC, where they wait as clips always
// did. Run by the sender between sends, so no clip is moved while it is being sent, and as it stops.
func (c *clips) spill(move func(kind string, at time.Time) bool) {
	if c.ram == "" {
		return
	}
	c.disk.Lock()
	defer c.disk.Unlock()
	// Turned off, the purge has let go of them all, and nothing is made again on eMMC.
	if !c.on.Load() {
		return
	}
	moved := 0
	for _, k := range clipKinds {
		kindMoved := 0
		sides, _ := filepath.Glob(filepath.Join(c.ram, k, "*.json"))
		for _, side := range sides {
			blob, err := os.ReadFile(side)
			if err != nil {
				continue
			}
			var m clipMeta
			if json.Unmarshal(blob, &m) != nil {
				continue
			}
			at, err := time.Parse(time.RFC3339, m.At)
			if err != nil {
				at = time.Time{} // no time to go by: it has waited long enough
			}
			if !move(k, at) {
				continue
			}
			if err := c.moveToDisk(k, side); err != nil {
				slog.Warn("wake clip: not moved from RAM to the device", "err", err)
				continue
			}
			kindMoved++
		}
		moved += kindMoved
		if kindMoved > 0 {
			// Each kind keeps its newest clipsKept, wherever they are; the count is unchanged.
			c.countMu.Lock()
			c.counts[k] = c.pruneLocked(k)
			c.countMu.Unlock()
		}
	}
	if moved > 0 {
		slog.Info("wake clips: moved from RAM to the device to wait there", "clips", moved)
		c.published()
	}
}

// moveToDisk copies a clip and its sidecar from RAM to the kind's folder on eMMC, the sidecar last so a
// clip half copied is never offered, then lets go of the copy in RAM. Held with disk.
func (c *clips) moveToDisk(kind, side string) error {
	wav := strings.TrimSuffix(side, ".json") + ".wav"
	dir := filepath.Join(c.dir, kind)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	to := filepath.Join(dir, filepath.Base(wav))
	if err := copyFile(wav, to); err != nil {
		os.Remove(to)
		if errors.Is(err, fs.ErrNotExist) {
			os.Remove(side) // a sidecar with no clip: nothing to keep
			return nil
		}
		return err
	}
	if err := copyFile(side, sidecar(to)); err != nil {
		os.Remove(to)
		os.Remove(sidecar(to))
		return err
	}
	os.Remove(side)
	os.Remove(wav)
	return nil
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// dropSent lets go of a clip in RAM the trainer has: the device's copy was only waiting. The sidecar
// first, as dropTake does, so a clip whose audio could not be removed is never offered again. It says
// whether the sidecar went: one that did not is still in the queue, and the sender must not send it
// again.
func (c *clips) dropSent(wav string, kind string) bool {
	c.disk.Lock()
	defer c.disk.Unlock()
	if err := os.Remove(sidecar(wav)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		slog.Warn("wake clip: not removed from RAM after it was sent", "err", err)
		return false
	}
	os.Remove(wav)
	if slices.Contains(clipKinds, kind) {
		c.countMu.Lock()
		c.counts[kind] = len(c.glob(kind, "*.wav"))
		c.countMu.Unlock()
		c.published()
	}
	return true
}

// pruneLocked keeps the newest keep clips of a kind across both folders, and says how many are left.
// The names start with the time, so name order is age wherever a clip is. Held with disk.
func (c *clips) pruneLocked(kind string) int {
	for _, d := range c.dirs() {
		sweepTmp(filepath.Join(d, kind))
	}
	files := c.glob(kind, "*.wav")
	if len(files) <= c.keep {
		return len(files)
	}
	slices.SortFunc(files, func(a, b string) int { return strings.Compare(filepath.Base(a), filepath.Base(b)) })
	left := len(files)
	for _, f := range files[:len(files)-c.keep] {
		os.Remove(sidecar(f))
		if os.Remove(f) == nil {
			left--
		}
	}
	return left
}
