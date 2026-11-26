package mic

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// A raw dump of the capture while the daemon runs, for measuring the array over the daemon's own
// playback (music from Home Assistant through the real speaker path), which `echod tools mic` cannot:
// it needs the daemon held, and then nothing plays.
//
// Writing a number of seconds into rawDumpTrigger starts one: the frames exactly as ListenRaw hands
// them (Channels at Rate, Bits wide little-endian: the microphones, then the reference) go to
// the dump folder as rawdump-<unix seconds>.s24, and the trigger is removed when it is done. Nothing is
// recorded while the trigger is absent: all that runs is a look for it once a second.
//
// rawNativeTrigger does the same for the converter's own stream before the decimators (RawBlock): on the
// Dot 5 every slot of UL9 at 96 kHz, S32 little-endian, as read. That is what localizing wants, where
// 16 kHz leaves the whole array's aperture inside a sample and a half. The file is
// rawnative-<unix>-<rate>-<channels>.s<bits>, with the privacy cut's blocks written as silence so
// the file keeps its time.
//
// The dumps go to a folder of their own rather than /tmp, which is a 32 MB tmpfs from v1.6.53 that a
// 10 s native dump on the Dot 5 (23 MB) all but fills, and which anyone on the device can list. The
// folder is root's alone (0700) and each file is created 0600 and exclusively, so nobody else on the
// device (the add-ons) can read a recording. The folder is on flash where the dump fits there
// (dumpSpots), and otherwise in memory under /run: the Dot 5's /data has only a few MB free. A dump
// is cut to the room its folder has (in memory, to what the kernel says is available as well), less
// a reserve, and a .part left by a dump the daemon did not finish is removed when the next one
// starts. The files stay until someone deletes them, or on /run until the next boot.
const (
	rawNativeTrigger = "/run/techo5/rawnative"
	rawDumpTrigger   = "/run/techo5/rawdump"
	rawDumpMax       = 300 // seconds; 72 MB at the Dot 5's 240 KB/s

	// rawNativeMax: 2.3 MB a second at 96 kHz, six slots of four bytes.
	rawNativeMax = 60

	// flashReserve is what a dump leaves free on flash, for the state and the log; ramReserve what it
	// leaves of the memory available, for the daemon and the rest.
	flashReserve = 16 << 20
	ramReserve   = 64 << 20
)

// dumpSpot is a folder a dump may go to; ram is a tmpfs, whose room is memory.
type dumpSpot struct {
	dir string
	ram bool
}

// dumpSpots are tried in order, the first that fits the whole dump taken. A variable for the tests.
var dumpSpots = []dumpSpot{
	{dir: "/data/techo5-linux/micdump"},
	{dir: "/run/techo5/micdump", ram: true},
}

// memAvailable is MemAvailable from /proc/meminfo, in bytes; a variable for the tests.
var memAvailable = func() (int64, error) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(line, "MemAvailable:"); ok {
			kb, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(rest), " kB"), 10, 64)
			if err != nil {
				return 0, err
			}
			return kb << 10, nil
		}
	}
	return 0, errors.New("no MemAvailable in /proc/meminfo")
}

// prepareDumpDir picks the folder for a dump of want bytes: the first of dumpSpots with room for
// all of it, or else the one with the most. It says how many bytes the dump may write there.
func prepareDumpDir(want int64) (string, int64, error) {
	var (
		best     string
		bestRoom int64 = -1
		errs     []error
	)
	for _, s := range dumpSpots {
		room, err := prepareSpot(s)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if room >= want {
			return s.dir, room, nil
		}
		if room > bestRoom {
			best, bestRoom = s.dir, room
		}
	}
	if bestRoom < 0 {
		return "", 0, errors.Join(errs...)
	}
	return best, bestRoom, nil
}

// prepareSpot makes s's folder root's alone, refusing one that is a link or someone else's, clears
// what an unfinished dump left, and says how many bytes a dump may write there.
func prepareSpot(s dumpSpot) (int64, error) {
	dir := s.dir
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return 0, err
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return 0, err
	}
	if !fi.IsDir() {
		return 0, fmt.Errorf("%s is not a folder", dir)
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Geteuid() {
		return 0, fmt.Errorf("%s belongs to uid %d", dir, st.Uid)
	}
	if fi.Mode().Perm() != 0o700 {
		if err := os.Chmod(dir, 0o700); err != nil {
			return 0, err
		}
	}
	parts, _ := filepath.Glob(filepath.Join(dir, "*.part"))
	for _, p := range parts {
		_ = os.Remove(p)
	}
	var fs syscall.Statfs_t
	if err := syscall.Statfs(dir, &fs); err != nil {
		return 0, err
	}
	avail := int64(fs.Bavail) * int64(fs.Bsize)
	if !s.ram {
		return dumpRoom(avail, flashReserve), nil
	}
	mem, err := memAvailable()
	if err != nil {
		return 0, err
	}
	return dumpRoom(min(avail, mem), ramReserve), nil
}

// dumpRoom is what a dump may write where avail bytes are free and reserve must stay so.
func dumpRoom(avail, reserve int64) int64 {
	return max(avail-reserve, 0)
}

// dumpLength is how many bytes of whole frames a dump of secs writes, frame bytes at rate, cut to
// room.
func dumpLength(secs, rate, frame int, room int64) int {
	want := int64(secs) * int64(rate) * int64(frame)
	if want > room {
		want = room / int64(frame) * int64(frame)
	}
	return int(want)
}

// createDump opens path+".part" for a dump: new, root's alone, and never through a link.
func createDump(path string) (*os.File, error) {
	return os.OpenFile(path+".part", os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
}

// finishDump closes out and gives it its final name.
func finishDump(out *os.File, path string) {
	_ = out.Close()
	if err := os.Rename(path+".part", path); err != nil {
		slog.Error("mic dump", "err", err)
	}
}

// nativeDump writes secs of the converter's stream. The capture loop hands blocks over a buffered
// channel, since a write to flash must not hold the capture up; a block the writer is too slow for is
// counted and dropped, which the log says.
func nativeDump(secs int) {
	type block struct {
		pcm                   []byte
		rate, channels, width int
	}
	ch := make(chan block, 64)
	var dropped atomic.Int64
	f := func(b RawBlock) {
		var pcm []byte
		if b.Cut {
			pcm = make([]byte, len(b.PCM)) // the mute: silence in its place, so the file keeps its time
		} else {
			pcm = append([]byte(nil), b.PCM...)
		}
		select {
		case ch <- block{pcm, b.Rate, b.Channels, b.Width}:
		default:
			dropped.Add(1)
		}
	}
	rawDumpTap.Store(&f)
	defer rawDumpTap.Store(nil)

	var (
		out    *os.File
		path   string
		got    int
		want   int
		failed bool
	)
	deadline := time.After(time.Duration(secs+5) * time.Second)
loop:
	for out == nil || got < want {
		select {
		case b := <-ch:
			if out == nil {
				if b.width <= 0 || b.channels <= 0 {
					slog.Error("native dump: a block without a sample width", "width", b.width, "channels", b.channels)
					failed = true
					break loop
				}
				frame := b.channels * b.width
				dir, room, err := prepareDumpDir(int64(secs) * int64(b.rate) * int64(frame))
				if err != nil {
					slog.Error("native dump", "err", err)
					failed = true
					break loop
				}
				path = dir + "/rawnative-" + strconv.FormatInt(time.Now().Unix(), 10) + "-" +
					strconv.Itoa(b.rate) + "-" + strconv.Itoa(b.channels) + ".s" + strconv.Itoa(b.width*8)
				want = dumpLength(secs, b.rate, frame, room)
				if want == 0 {
					slog.Error("native dump: no room", "dir", dir, "free", room)
					failed = true
					break loop
				}
				if out, err = createDump(path); err != nil {
					slog.Error("native dump", "err", err)
					failed = true
					break loop
				}
				slog.Info("native dump started", "path", path, "seconds", secs, "bytes", want)
			}
			n := min(len(b.pcm), want-got)
			if _, err := out.Write(b.pcm[:n]); err != nil {
				slog.Error("native dump", "err", err)
				break loop
			}
			got += n
		case <-deadline:
			break loop
		}
	}
	if out == nil {
		if !failed {
			slog.Warn("native dump: this board hands no native stream")
		}
		return
	}
	finishDump(out, path)
	slog.Info("native dump done", "path", path, "bytes", got, "short_bytes", want-got, "dropped_blocks", dropped.Load())
}

var rawDumpOnce sync.Once

// startRawDump watches for both triggers, once per process: one look a second, each dump in its
// own goroutine so the two can overlap, and a trigger that is still running is not started again.
func (s *Source) startRawDump() {
	rawDumpOnce.Do(func() {
		var stock, native atomic.Bool
		safe.Go("raw dump", func() {
			for {
				time.Sleep(time.Second)
				watchTrigger(&stock, rawDumpTrigger, 30, rawDumpMax, "raw dump", s.rawDump)
				watchTrigger(&native, rawNativeTrigger, 10, rawNativeMax, "native dump", nativeDump)
			}
		})
	})
}

// watchTrigger starts dump when trigger holds a number of seconds (def when it holds none, at most
// most) and none is running, and removes the trigger when it is done.
func watchTrigger(busy *atomic.Bool, trigger string, def, most int, name string, dump func(int)) {
	if busy.Load() {
		return
	}
	b, err := os.ReadFile(trigger)
	if err != nil {
		return
	}
	secs, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || secs <= 0 {
		secs = def
	}
	busy.Store(true)
	safe.Go(name, func() {
		defer busy.Store(false)
		defer func() { _ = os.Remove(trigger) }()
		dump(min(secs, most))
	})
}

func (s *Source) rawDump(secs int) {
	frame := Channels * Bits / 8
	dir, room, err := prepareDumpDir(int64(secs) * int64(Rate) * int64(frame))
	if err != nil {
		slog.Error("raw dump", "err", err)
		return
	}
	want := dumpLength(secs, Rate, frame, room)
	if want == 0 {
		slog.Error("raw dump: no room", "dir", dir, "free", room)
		return
	}
	path := dir + "/rawdump-" + strconv.FormatInt(time.Now().Unix(), 10) + ".s" + strconv.Itoa(Bits)
	f, err := createDump(path)
	if err != nil {
		slog.Error("raw dump", "err", err)
		return
	}
	frames, done := s.ListenRaw()
	defer done()
	slog.Info("raw dump started", "path", path, "seconds", secs, "channels", Channels, "rate", Rate, "bytes", want)
	got := 0
	deadline := time.After(time.Duration(secs+5) * time.Second)
	why := ""
loop:
	for got < want {
		select {
		case buf, ok := <-frames:
			if !ok {
				why = "the capture closed"
				break loop
			}
			n := min(len(buf), want-got)
			if _, err := f.Write(buf[:n]); err != nil {
				slog.Error("raw dump", "err", err)
				why = "write failed"
				break loop
			}
			got += n
		case <-deadline:
			// ListenRaw drops what a listener is too slow for without a word: frames missing
			// here are those, or a capture that stalled.
			why = "deadline"
			break loop
		}
	}
	finishDump(f, path)
	if got < want {
		slog.Warn("raw dump done short", "path", path, "bytes", got, "short_bytes", want-got, "why", why)
		return
	}
	slog.Info("raw dump done", "path", path, "bytes", got)
}
