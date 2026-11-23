package mic

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// A raw dump of the capture while the daemon runs, for measuring the array over the daemon's own
// playback (music from Home Assistant through the real speaker path), which `echod tools mic` cannot:
// it needs the daemon held, and then nothing plays.
//
// Writing a number of seconds into rawDumpTrigger starts one: the frames exactly as ListenRaw hands
// them (Channels at Rate, S24_3LE: the microphones, then the reference) go to rawDumpDir as
// rawdump-<unix seconds>.s24, and the trigger is removed when it is done. Both live on tmpfs, so
// nothing is written to flash and a reboot clears them. Nothing is recorded while the trigger is
// absent: all that runs is a look for it once a second.
//
// rawNativeTrigger does the same for the converter's own stream before the decimators (RawBlock): on the
// Dot 5 every slot of UL9 at 96 kHz, S32 little-endian, as read. That is what localizing wants, where
// 16 kHz leaves the whole array's aperture inside a sample and a half. The file is
// rawnative-<unix>-<rate>-<channels>.s32.
const (
	rawNativeTrigger = "/run/techo5/rawnative"
	rawDumpTrigger   = "/run/techo5/rawdump"
	rawDumpDir       = "/tmp"
	rawDumpMax       = 300 // seconds; 72 MB at the Dot 5's 240 KB/s

	// rawNativeMax: 2.3 MB a second at 96 kHz, six slots of four bytes.
	rawNativeMax = 60
)

// nativeDump writes secs of the converter's stream. The capture loop hands blocks over a buffered
// channel, since a write to tmpfs must not hold the capture up; a block the writer is too slow for is
// counted and dropped, which the log says.
func nativeDump(secs int) {
	type block struct {
		pcm            []byte
		rate, channels int
	}
	ch := make(chan block, 64)
	var dropped atomic.Int64
	f := func(b RawBlock) {
		select {
		case ch <- block{append([]byte(nil), b.PCM...), b.Rate, b.Channels}:
		default:
			dropped.Add(1)
		}
	}
	rawDumpTap.Store(&f)

	var (
		out  *os.File
		path string
		got  int
		want int
	)
	deadline := time.After(time.Duration(secs+5) * time.Second)
loop:
	for want == 0 || got < want {
		select {
		case b := <-ch:
			if out == nil {
				path = rawDumpDir + "/rawnative-" + strconv.FormatInt(time.Now().Unix(), 10) + "-" +
					strconv.Itoa(b.rate) + "-" + strconv.Itoa(b.channels) + ".s32"
				var err error
				if out, err = os.Create(path + ".part"); err != nil {
					slog.Error("native dump", "err", err)
					break loop
				}
				want = secs * b.rate * b.channels * 4
				slog.Info("native dump started", "path", path, "seconds", secs)
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
	rawDumpTap.Store(nil)
	if out == nil {
		slog.Warn("native dump: this board hands no native stream")
		return
	}
	_ = out.Close()
	_ = os.Rename(path+".part", path)
	slog.Info("native dump done", "path", path, "bytes", got, "dropped_blocks", dropped.Load())
}

var rawDumpOnce sync.Once

// startRawDump watches for the trigger, once per process.
func (s *Source) startRawDump() {
	rawDumpOnce.Do(func() {
		safe.Go("raw dump", func() {
			for {
				time.Sleep(time.Second)
				b, err := os.ReadFile(rawDumpTrigger)
				if err != nil {
					continue
				}
				secs, err := strconv.Atoi(strings.TrimSpace(string(b)))
				if err != nil || secs <= 0 {
					secs = 30
				}
				s.rawDump(min(secs, rawDumpMax))
				_ = os.Remove(rawDumpTrigger)
			}
		})
		safe.Go("native dump", func() {
			for {
				time.Sleep(200 * time.Millisecond)
				b, err := os.ReadFile(rawNativeTrigger)
				if err != nil {
					continue
				}
				secs, err := strconv.Atoi(strings.TrimSpace(string(b)))
				if err != nil || secs <= 0 {
					secs = 10
				}
				nativeDump(min(secs, rawNativeMax))
				_ = os.Remove(rawNativeTrigger)
			}
		})
	})
}

func (s *Source) rawDump(secs int) {
	path := rawDumpDir + "/rawdump-" + strconv.FormatInt(time.Now().Unix(), 10) + ".s24"
	f, err := os.Create(path + ".part")
	if err != nil {
		slog.Error("raw dump", "err", err)
		return
	}
	frames, done := s.ListenRaw()
	defer done()
	slog.Info("raw dump started", "path", path, "seconds", secs, "channels", Channels, "rate", Rate)
	want := secs * Rate * Channels * 3
	got := 0
	deadline := time.After(time.Duration(secs+5) * time.Second)
	for got < want {
		select {
		case buf, ok := <-frames:
			if !ok {
				want = got
				break
			}
			n := min(len(buf), want-got)
			if _, err := f.Write(buf[:n]); err != nil {
				slog.Error("raw dump", "err", err)
				want = got
			}
			got += n
		case <-deadline:
			want = got
		}
	}
	_ = f.Close()
	_ = os.Rename(path+".part", path)
	slog.Info("raw dump done", "path", path, "bytes", got)
}
